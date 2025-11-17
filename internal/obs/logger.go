package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Context keys for common request metadata
type ctxKey string

const (
	ctxKeyRequestID ctxKey = "request_id"
	ctxKeyUserID    ctxKey = "user_id"
	ctxKeyVideoID   ctxKey = "video_id"
	ctxKeyIP        ctxKey = "ip"
)

// NewLogger constructs a slog logger.
// - env: "production" -> JSON; anything else -> text
// - level: debug|info|warn|error
// - w: destination writer (defaults to stderr when nil)
func NewLogger(env, level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}

	// Map string level to slog.Level
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	// Redact sensitive fields and normalize level to upper-case string
	replace := func(_ []string, a slog.Attr) slog.Attr {
		// Normalize level to upper-case string for tests that assert "ERROR"
		if a.Key == slog.LevelKey {
			// Convert to upper case text value
			if lv, ok := a.Value.Any().(slog.Level); ok {
				// slog.Level.String() yields lower-case; convert to upper
				s := strings.ToUpper(lv.String())
				return slog.Attr{Key: a.Key, Value: slog.StringValue(s)}
			}
			if s, ok := a.Value.Any().(string); ok {
				return slog.Attr{Key: a.Key, Value: slog.StringValue(strings.ToUpper(s))}
			}
		}

		// Redact sensitive keys
		keyLower := strings.ToLower(a.Key)
		switch keyLower {
		case "password", "token", "access_token", "refresh_token", "api_key", "secret", "authorization":
			return slog.Attr{Key: a.Key, Value: slog.StringValue("[REDACTED]")}
		}

		return a
	}

	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: replace}
	var handler slog.Handler
	if strings.ToLower(env) == "production" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	return slog.New(handler)
}

// Global logger helpers
var globalLogger *slog.Logger

func SetGlobalLogger(l *slog.Logger) { globalLogger = l }
func GetGlobalLogger() *slog.Logger  { return globalLogger }

// Context helpers
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}
func ContextWithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}
func ContextWithVideoID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyVideoID, id)
}
func ContextWithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyIP, ip)
}

// LoggerFromContext attaches common fields from context to the provided logger
func LoggerFromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		base = NewLogger("production", "info", os.Stderr)
	}
	attrs := make([]any, 0, 8)
	if v := ctx.Value(ctxKeyRequestID); v != nil {
		attrs = append(attrs, string(ctxKeyRequestID), v)
	}
	if v := ctx.Value(ctxKeyUserID); v != nil {
		attrs = append(attrs, string(ctxKeyUserID), v)
	}
	if v := ctx.Value(ctxKeyVideoID); v != nil {
		attrs = append(attrs, string(ctxKeyVideoID), v)
	}
	if v := ctx.Value(ctxKeyIP); v != nil {
		attrs = append(attrs, string(ctxKeyIP), v)
	}
	if len(attrs) == 0 {
		return base
	}
	return base.With(attrs...)
}
