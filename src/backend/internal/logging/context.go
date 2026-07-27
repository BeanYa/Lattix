package logging

import (
	"context"
	"net/http"
)

type requestIDKey struct{}

func WithRequestID(r *http.Request, requestID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}
