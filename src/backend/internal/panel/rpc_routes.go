package panel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
	"lattix/shared"
)

const defaultRPCBodyLimit = int64(1 << 20)

type rpcRouteOptions struct {
	Auth           bool
	CSRF           bool
	Idempotent     bool
	LogPolicy      logging.LogPolicy
	AllowedQuery   []string
	SafeBodyFields []string
	BodyLimit      int64
	SameOrigin     bool
}

func (s *Server) registerRPC(
	mux *http.ServeMux,
	method, path string,
	options rpcRouteOptions,
	handler http.HandlerFunc,
) {
	pattern := method + " " + path
	if options.LogPolicy == "" {
		options.LogPolicy = logging.LogFull
	}
	s.routePolicies[pattern] = options.LogPolicy

	wrapped := handler
	if options.Idempotent {
		wrapped = s.requireIdempotency(pattern, wrapped)
	}
	if options.CSRF {
		wrapped = s.requireCSRF(wrapped)
	}
	if options.Auth {
		wrapped = s.requireAuth(wrapped)
	}
	if options.SameOrigin {
		wrapped = s.requireSameOrigin(wrapped)
	}
	if method == http.MethodPost {
		limit := options.BodyLimit
		if limit <= 0 {
			limit = defaultRPCBodyLimit
		}
		wrapped = validateRPCJSON(limit, options.SafeBodyFields, wrapped)
	} else {
		wrapped = validateRPCQuery(options.AllowedQuery, wrapped)
	}
	mux.HandleFunc(pattern, wrapped)
	mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", method)
		writeProtocolError(w, http.StatusMethodNotAllowed, "method not allowed")
	})
}

// LogPolicy 返回服务端路由注册时声明的请求日志策略。
func (s *Server) LogPolicy(r *http.Request) logging.LogPolicy {
	if policy, ok := s.routePolicies[r.Pattern]; ok {
		return policy
	}
	return logging.LogFull
}

func validateRPCJSON(limit int64, safeFields []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeProtocolError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeProtocolError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		if int64(len(data)) > limit {
			writeProtocolError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
			writeProtocolError(w, http.StatusBadRequest, "request body must be one JSON object")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		addSafeBodyAttributes(r, data, safeFields)
		next(w, r)
	}
}

func validateRPCQuery(allowed []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for key, values := range r.URL.Query() {
			if !slices.Contains(allowed, key) {
				writeProtocolError(w, http.StatusBadRequest, "unknown query parameter: "+key)
				return
			}
			if len(values) != 1 {
				writeProtocolError(w, http.StatusBadRequest, "query parameter must not repeat: "+key)
				return
			}
		}
		next(w, r)
	}
}

func addSafeBodyAttributes(r *http.Request, data []byte, allowed []string) {
	if len(allowed) == 0 {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return
	}
	for _, name := range allowed {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var scalar any
		if json.Unmarshal(raw, &scalar) != nil {
			continue
		}
		switch value := scalar.(type) {
		case string:
			logging.AddSafeAttribute(r.Context(), name, value)
		case float64, bool:
			logging.AddSafeAttribute(r.Context(), name, strings.TrimSpace(string(raw)))
		}
	}
}

type cachedRPCResponse struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (s *Server) requireIdempotency(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if !validIdempotencyKey(key) {
			writeRPC(w, shared.CodeInvalidArgument, "valid Idempotency-Key is required", nil)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeProtocolError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(body)
		requestHash := hex.EncodeToString(sum[:])
		operator, _ := s.currentUser(r)

		s.idempotencyMu.Lock()
		defer s.idempotencyMu.Unlock()

		record, err := s.st.IdempotencyRecord(r.Context(), operator, route, key)
		switch {
		case err == nil:
			if record.RequestHash != requestHash {
				writeRPC(w, shared.CodeConflict, "Idempotency-Key was already used with a different request", nil)
				return
			}
			var cached cachedRPCResponse
			if json.Unmarshal([]byte(record.ResponseJSON), &cached) != nil {
				writeRPC(w, shared.CodeInternalError, "stored idempotency result is invalid", nil)
				return
			}
			if replayWriter, ok := w.(interface{ SetIdempotencyReplayed(bool) }); ok {
				replayWriter.SetIdempotencyReplayed(true)
			}
			writeRPC(w, cached.Code, cached.Message, cached.Data)
			return
		case !errors.Is(err, store.ErrNotFound):
			writeRPC(w, shared.CodeInternalError, "failed to read idempotency state", nil)
			return
		}

		capture := newRPCCapture(w)
		next(capture, r)

		if capture.status != http.StatusOK {
			capture.flushTo(w)
			return
		}
		var response struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if json.Unmarshal(capture.body.Bytes(), &response) != nil {
			writeRPC(w, shared.CodeInternalError, "failed to capture idempotency result", nil)
			return
		}
		cached := cachedRPCResponse{Code: response.Code, Message: response.Message, Data: response.Data}
		encoded, _ := json.Marshal(cached)
		if err := s.st.SaveIdempotencyRecord(
			r.Context(), operator, route, key, requestHash, string(encoded),
		); err != nil {
			writeRPC(w, shared.CodeInternalError, "failed to persist idempotency result", nil)
			return
		}
		capture.flushTo(w)
		_ = s.st.DeleteExpiredIdempotencyRecords(r.Context())
	}
}

func validIdempotencyKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}

type rpcCapture struct {
	target http.ResponseWriter
	header http.Header
	body   bytes.Buffer
	status int
}

func newRPCCapture(target http.ResponseWriter) *rpcCapture {
	return &rpcCapture{target: target, header: make(http.Header)}
}

func (w *rpcCapture) Header() http.Header { return w.header }

func (w *rpcCapture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *rpcCapture) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *rpcCapture) SetRPCOutcome(code, message string) {
	if target, ok := w.target.(interface{ SetRPCOutcome(string, string) }); ok {
		target.SetRPCOutcome(code, message)
	}
}

func (w *rpcCapture) RPCIDs() (string, string) {
	if target, ok := w.target.(interface{ RPCIDs() (string, string) }); ok {
		return target.RPCIDs()
	}
	return "", ""
}

func (w *rpcCapture) flushTo(target http.ResponseWriter) {
	for key, values := range w.header {
		target.Header()[key] = append([]string(nil), values...)
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	target.WriteHeader(status)
	_, _ = target.Write(w.body.Bytes())
}
