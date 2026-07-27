package logging

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxCapturedResponse = 4096
	maxParameterValue   = 512
	maxParametersJSON   = 4096
)

var (
	pathParameterPattern = regexp.MustCompile(`\{([^}/]+)\}`)
	sensitiveNamePattern = regexp.MustCompile(`(?i)(token|password|secret|key|cookie|authorization|cert|private)`)
)

type OperatorFunc func(*http.Request) string

func RequestMiddleware(log *RequestLog, operator OperatorFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldLogRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		requestID := newID()
		r = WithRequestID(r, requestID)
		w.Header().Set("X-Request-ID", requestID)
		recorder := &responseRecorder{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				if recorder.status == 0 {
					http.Error(recorder, "internal server error", http.StatusInternalServerError)
				}
				log.Append(buildRequestEntry(r, recorder, started, operator, fmt.Sprint(recovered)))
				return
			}
			// 成功的 WebSocket 升级由 LogWebSocketUpgrade 在握手完成时立即记录。
			if r.URL.Path == "/api/agent/ws" && (recorder.status == 0 || recorder.status == http.StatusSwitchingProtocols) {
				return
			}
			log.Append(buildRequestEntry(r, recorder, started, operator, ""))
		}()
		next.ServeHTTP(recorder, r)
	})
}

func LogWebSocketUpgrade(log *RequestLog, r *http.Request, operator OperatorFunc) {
	entry := RequestEntry{
		Timestamp:     time.Now().UTC(),
		RequestID:     RequestID(r.Context()),
		Severity:      SeverityInfo,
		Method:        r.Method,
		Path:          safePath(r),
		Route:         routePattern(r),
		Params:        safeParameters(r),
		Status:        http.StatusSwitchingProtocols,
		DurationMS:    0,
		ResponseBytes: 0,
		IP:            ClientIP(r),
		UserAgent:     truncate(r.UserAgent(), maxParameterValue),
	}
	if operator != nil {
		entry.Operator = operator(r)
	}
	log.Append(entry)
}

func buildRequestEntry(r *http.Request, recorder *responseRecorder, started time.Time, operator OperatorFunc, panicSummary string) RequestEntry {
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	duration := time.Since(started)
	severity := SeverityInfo
	switch {
	case status >= 500 || panicSummary != "":
		severity = SeverityError
	case status >= 400 || duration > 2*time.Second:
		severity = SeverityWarning
	}
	errorSummary := panicSummary
	if errorSummary == "" && status >= 400 {
		errorSummary = responseError(recorder.capture)
	}
	entry := RequestEntry{
		Timestamp:     time.Now().UTC(),
		RequestID:     RequestID(r.Context()),
		Severity:      severity,
		Method:        r.Method,
		Path:          safePath(r),
		Route:         routePattern(r),
		Params:        safeParameters(r),
		Status:        status,
		DurationMS:    duration.Milliseconds(),
		ResponseBytes: recorder.bytes,
		IP:            ClientIP(r),
		UserAgent:     truncate(r.UserAgent(), maxParameterValue),
		ErrorSummary:  truncate(errorSummary, maxParameterValue),
	}
	if operator != nil {
		entry.Operator = operator(r)
	}
	return entry
}

func shouldLogRequest(r *http.Request) bool {
	if r.Method == http.MethodGet && r.URL.Path == "/api/logs/requests" {
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/sub/")
}

func routePattern(r *http.Request) string {
	if r.Pattern == "" {
		return r.URL.Path
	}
	if _, pattern, ok := strings.Cut(r.Pattern, " "); ok {
		return pattern
	}
	return r.Pattern
}

func safePath(r *http.Request) string {
	path := r.URL.Path
	if strings.HasPrefix(path, "/sub/") {
		token := r.PathValue("token")
		if token != "" {
			sum := sha256.Sum256([]byte(token))
			path = strings.Replace(path, token, "[token:"+hex.EncodeToString(sum[:4])+"]", 1)
		}
	}
	return truncate(path, maxParameterValue)
}

func safeParameters(r *http.Request) map[string]string {
	params := map[string]string{}
	for key, values := range r.URL.Query() {
		params["query."+key] = safeValue(key, strings.Join(values, ","))
	}
	for _, match := range pathParameterPattern.FindAllStringSubmatch(routePattern(r), -1) {
		name := strings.TrimSuffix(match[1], "...")
		value := r.PathValue(name)
		if value != "" {
			params["path."+name] = safeValue(name, value)
		}
	}
	if len(params) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(params)
	if len(encoded) <= maxParametersJSON {
		return params
	}
	trimmed := map[string]string{}
	size := 2
	for key, value := range params {
		item, _ := json.Marshal(map[string]string{key: value})
		if size+len(item) > maxParametersJSON-32 {
			trimmed["_truncated"] = "true"
			break
		}
		trimmed[key] = value
		size += len(item)
	}
	return trimmed
}

func safeValue(name, value string) string {
	if sensitiveNamePattern.MatchString(name) {
		return "[REDACTED]"
	}
	return truncate(value, maxParameterValue)
}

func responseError(data []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return body.Error
	}
	return strings.TrimSpace(string(data))
}

func ClientIP(r *http.Request) string {
	host, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		host = r.RemoteAddr
	}
	if remoteIP := net.ParseIP(host); remoteIP != nil && remoteIP.IsLoopback() {
		xff := r.Header.Get("X-Forwarded-For")
		if xff == "" {
			return host
		}
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	return host
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "[TRUNCATED]"
}

type responseRecorder struct {
	http.ResponseWriter
	status  int
	bytes   int64
	capture []byte
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if len(w.capture) < maxCapturedResponse {
		remaining := maxCapturedResponse - len(w.capture)
		w.capture = append(w.capture, data[:min(remaining, len(data))]...)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *responseRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *responseRecorder) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (w *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(reader)
		w.bytes += n
		return n, err
	}
	return io.Copy(struct{ io.Writer }{w}, reader)
}

func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func ParseLimit(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
