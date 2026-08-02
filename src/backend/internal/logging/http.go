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

	"lattix/shared"
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

type LogPolicy string

const (
	LogFull         LogPolicy = "full"
	LogFailuresOnly LogPolicy = "failures_only"
	LogNone         LogPolicy = "none"
)

type LogPolicyFunc func(*http.Request) LogPolicy

// DebugRouteFunc 声明路由为轮询/状态类：成功请求记录为 debug 级别。
type DebugRouteFunc func(*http.Request) bool

// SeverityFunc 返回请求日志最低记录级别（低于该级别不写入）。
type SeverityFunc func(*http.Request) Severity

func RequestMiddleware(
	log *RequestLog,
	operator OperatorFunc,
	policy LogPolicyFunc,
	debugRoute DebugRouteFunc,
	level SeverityFunc,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := validOrNewID(r.Header.Get("X-Request-ID"))
		traceID := validOrNewID(r.Header.Get("X-Trace-ID"))
		if r.Header.Get("X-Trace-ID") == "" {
			traceID = requestID
		}
		r = WithRequestMeta(r, requestID, traceID)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Trace-ID", traceID)

		if !shouldLogRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseRecorder{ResponseWriter: w, request: r}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				if recorder.status == 0 {
					http.Error(recorder, "internal server error", http.StatusInternalServerError)
				}
				entry := buildRequestEntry(r, recorder, started, operator, fmt.Sprint(recovered), debugRoute)
				if shouldAppend(policyFor(policy, r), levelFor(level, r), entry) {
					log.Append(entry)
				}
				return
			}
			// 成功的 WebSocket 升级由 LogWebSocketUpgrade 在握手完成时立即记录。
			if r.URL.Path == "/api/agent/ws" && (recorder.status == 0 || recorder.status == http.StatusSwitchingProtocols) {
				return
			}
			entry := buildRequestEntry(r, recorder, started, operator, "", debugRoute)
			if shouldAppend(policyFor(policy, r), levelFor(level, r), entry) {
				log.Append(entry)
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}

func LogWebSocketUpgrade(log *RequestLog, r *http.Request, operator OperatorFunc) {
	entry := RequestEntry{
		Timestamp:     time.Now().UTC(),
		RequestID:     RequestID(r.Context()),
		TraceID:       TraceID(r.Context()),
		Severity:      SeverityInfo,
		Transport:     "http",
		Method:        r.Method,
		Path:          safePath(r),
		Route:         routePattern(r),
		Attributes:    safeParameters(r),
		HTTPStatus:    http.StatusSwitchingProtocols,
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

func buildRequestEntry(r *http.Request, recorder *responseRecorder, started time.Time, operator OperatorFunc, panicSummary string, debugRoute DebugRouteFunc) RequestEntry {
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	duration := time.Since(started)
	meta := requestMeta(r.Context())
	rpcCode, safeMessage := "", ""
	idempotencyReplayed := false
	attributes := safeParameters(r)
	if meta != nil {
		rpcCode, safeMessage = meta.RPCCode, meta.SafeMessage
		idempotencyReplayed = meta.IdempotencyReplayed
		attributes = mergeAttributes(attributes, meta.Attributes)
	}
	severity := requestSeverity(status, rpcCode, duration, panicSummary != "")
	if debugRoute != nil && debugRoute(r) && status < 400 {
		severity = SeverityDebug
	}
	errorSummary := panicSummary
	if errorSummary == "" && rpcCode != "" && rpcCode != shared.CodeOK && rpcCode != shared.CodeAccepted {
		errorSummary = safeMessage
	} else if errorSummary == "" && status >= 400 {
		errorSummary = responseError(recorder.capture)
	}
	entry := RequestEntry{
		Timestamp:           time.Now().UTC(),
		RequestID:           RequestID(r.Context()),
		TraceID:             TraceID(r.Context()),
		Severity:            severity,
		Transport:           "http",
		Method:              r.Method,
		Path:                safePath(r),
		Route:               routePattern(r),
		Attributes:          attributes,
		HTTPStatus:          status,
		RPCCode:             rpcCode,
		DurationMS:          duration.Milliseconds(),
		ResponseBytes:       recorder.bytes,
		IP:                  ClientIP(r),
		UserAgent:           truncate(r.UserAgent(), maxParameterValue),
		ErrorSummary:        truncate(errorSummary, maxParameterValue),
		IdempotencyReplayed: idempotencyReplayed,
	}
	if operator != nil {
		entry.Operator = operator(r)
	}
	return entry
}

// LogWebSocketRPC 记录一条已完成的命令型 WS RPC。高频 event 由调用方的
// LogPolicy 在进入此函数前过滤。
func LogWebSocketRPC(log *RequestLog, entry RequestEntry) {
	entry.Timestamp = time.Now().UTC()
	entry.Transport = "websocket"
	entry.Severity = requestSeverity(0, entry.RPCCode, time.Duration(entry.DurationMS)*time.Millisecond, false)
	if entry.RPCCode != shared.CodeOK && entry.RPCCode != shared.CodeAccepted {
		entry.ErrorSummary = truncate(entry.ErrorSummary, maxParameterValue)
	}
	log.Append(entry)
}

func requestSeverity(status int, rpcCode string, duration time.Duration, panicked bool) Severity {
	if panicked || status >= 500 || rpcCode == shared.CodeInternalError ||
		rpcCode == shared.CodeUpstreamError || rpcCode == shared.CodeServiceUnavailable {
		return SeverityError
	}
	if status >= 400 || duration > 2*time.Second {
		return SeverityWarning
	}
	switch rpcCode {
	case shared.CodeAuthRequired, shared.CodeAuthInvalidCredentials, shared.CodeInvalidArgument,
		shared.CodeNotFound, shared.CodeConflict, shared.CodeOperationLocked,
		shared.CodeUnsupportedAction, shared.CodeServerOffline, shared.CodePortOutOfRange,
		shared.CodeUpdateInProgress:
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

func policyFor(resolve LogPolicyFunc, r *http.Request) LogPolicy {
	if resolve == nil {
		return LogFull
	}
	if policy := resolve(r); policy != "" {
		return policy
	}
	return LogFull
}

func levelFor(resolve SeverityFunc, r *http.Request) Severity {
	if resolve == nil {
		return SeverityDebug
	}
	if level := resolve(r); level != "" {
		return level
	}
	return SeverityDebug
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityDebug:
		return 0
	case SeverityWarning:
		return 2
	case SeverityError:
		return 3
	default:
		return 1
	}
}

func shouldAppend(policy LogPolicy, minSeverity Severity, entry RequestEntry) bool {
	switch policy {
	case LogNone:
		return false
	case LogFailuresOnly:
		threshold := severityRank(minSeverity)
		if warning := severityRank(SeverityWarning); warning > threshold {
			threshold = warning
		}
		return severityRank(entry.Severity) >= threshold
	default:
		return severityRank(entry.Severity) >= severityRank(minSeverity)
	}
}

func validOrNewID(value string) string {
	if shared.ValidMessageID(value) {
		return value
	}
	return newID()
}

func mergeAttributes(left, right map[string]string) map[string]string {
	if len(right) == 0 {
		return left
	}
	if left == nil {
		left = make(map[string]string, len(right))
	}
	for key, value := range right {
		left[key] = value
	}
	return left
}

func shouldLogRequest(r *http.Request) bool {
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
	request *http.Request
	status  int
	bytes   int64
	capture []byte
}

func (w *responseRecorder) SetRPCOutcome(code, safeMessage string) {
	SetRPCOutcome(w.request.Context(), code, safeMessage)
}

func (w *responseRecorder) SetIdempotencyReplayed(replayed bool) {
	SetIdempotencyReplayed(w.request.Context(), replayed)
}

func (w *responseRecorder) RPCIDs() (string, string) {
	return RequestID(w.request.Context()), TraceID(w.request.Context())
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
