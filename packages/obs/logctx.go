package obs

import (
	"context"
	"log/slog"
)

// LoggerWithTrace 返回附带 trace_id 字段的 logger；无 trace 时原样返回。
func LoggerWithTrace(ctx context.Context, log *slog.Logger) *slog.Logger {
	if id := TraceFromCtx(ctx); id != "" {
		return log.With("trace_id", id)
	}
	return log
}
