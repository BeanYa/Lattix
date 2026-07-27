package logging

import (
	"context"
	"net/http"
)

type requestMetaKey struct{}

// RequestMeta 是一次 HTTP 请求的可变结构化元数据。中间件创建后，RPC handler
// 只通过下列 helper 补充 outcome 和白名单属性。
type RequestMeta struct {
	RequestID           string
	TraceID             string
	RPCCode             string
	SafeMessage         string
	IdempotencyReplayed bool
	Attributes          map[string]string
}

func WithRequestMeta(r *http.Request, requestID, traceID string) *http.Request {
	meta := &RequestMeta{RequestID: requestID, TraceID: traceID}
	return r.WithContext(context.WithValue(r.Context(), requestMetaKey{}, meta))
}

func requestMeta(ctx context.Context) *RequestMeta {
	value, _ := ctx.Value(requestMetaKey{}).(*RequestMeta)
	return value
}

func RequestID(ctx context.Context) string {
	if meta := requestMeta(ctx); meta != nil {
		return meta.RequestID
	}
	return ""
}

func TraceID(ctx context.Context) string {
	if meta := requestMeta(ctx); meta != nil {
		return meta.TraceID
	}
	return ""
}

func SetRPCOutcome(ctx context.Context, code, safeMessage string) {
	if meta := requestMeta(ctx); meta != nil {
		meta.RPCCode = code
		meta.SafeMessage = safeMessage
	}
}

func SetIdempotencyReplayed(ctx context.Context, replayed bool) {
	if meta := requestMeta(ctx); meta != nil {
		meta.IdempotencyReplayed = replayed
	}
}

// AddSafeAttribute 只应由路由显式声明的字段调用，不得传入任意 body。
func AddSafeAttribute(ctx context.Context, name, value string) {
	if meta := requestMeta(ctx); meta != nil {
		if meta.Attributes == nil {
			meta.Attributes = make(map[string]string)
		}
		meta.Attributes[name] = safeValue(name, value)
	}
}
