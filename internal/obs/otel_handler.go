package obs

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// OTelHandler is a slog.Handler that injects OpenTelemetry trace context into
// each log record. Matches PeerTube's winston defaultMeta pattern of including
// traceId, spanId, and traceFlags in every log entry.
//
// Only injects when a valid (non-zero) span is active in the context.
// Fields: trace_id, span_id, trace_flags
type OTelHandler struct {
	inner slog.Handler
}

// NewOTelHandler wraps the given slog.Handler with OTel trace context injection.
func NewOTelHandler(inner slog.Handler) *OTelHandler {
	return &OTelHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *OTelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects trace context if a valid span is active, then delegates.
func (h *OTelHandler) Handle(ctx context.Context, r slog.Record) error {
	spanCtx := trace.SpanFromContext(ctx).SpanContext()
	if spanCtx.IsValid() {
		// Clone the record so we don't mutate the original
		r = r.Clone()
		r.AddAttrs(
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
			slog.Int("trace_flags", int(spanCtx.TraceFlags())),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a new OTelHandler with the given attrs on the inner handler.
func (h *OTelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &OTelHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a new OTelHandler with the given group on the inner handler.
func (h *OTelHandler) WithGroup(name string) slog.Handler {
	return &OTelHandler{inner: h.inner.WithGroup(name)}
}
