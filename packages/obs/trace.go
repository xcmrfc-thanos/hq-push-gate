package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// trace_id 贯穿（docs/08 §7.1.3 U1：trace_id 贯穿 HTTP/Kafka/WS）。
// 传播方式：HTTP 用 X-Trace-Id 头；Kafka 用 x-trace-id 消息头（传输层，不改冻结的 proto 契约）。

type traceKey struct{}

// NewTraceID 生成 16 位随机 hex trace_id。
func NewTraceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// WithTrace 将 trace_id 存入 context。
func WithTrace(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// TraceFromCtx 读取 context 中的 trace_id，缺失返回空串。
func TraceFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}

// EnsureTrace 读取或生成 trace_id（用于链路入口）。
func EnsureTrace(ctx context.Context) (context.Context, string) {
	if id := TraceFromCtx(ctx); id != "" {
		return ctx, id
	}
	id := NewTraceID()
	return WithTrace(ctx, id), id
}

// TraceHeader HTTP 与 Kafka 通用的 trace 头名。
const TraceHeader = "X-Trace-Id"

// TraceMiddleware HTTP 中间件：入口读取/生成 trace_id，写入 context 与响应头。
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(TraceHeader)
		if id == "" {
			id = NewTraceID()
		}
		w.Header().Set(TraceHeader, id)
		next.ServeHTTP(w, r.WithContext(WithTrace(r.Context(), id)))
	})
}
