package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"gopkg.in/natefinch/lumberjack.v2"
)

// RotationConfig holds log rotation settings (matches PeerTube's log.rotation.* config).
type RotationConfig struct {
	Enabled    bool
	MaxSizeMB  int
	MaxFiles   int
	MaxAgeDays int
}

// LoggerConfig holds all configuration for the enhanced logger.
type LoggerConfig struct {
	Level    string         // debug | info | warn | error
	Format   string         // json | text (controls stderr handler format)
	LogDir   string         // directory for log files; empty = stderr only
	Filename string         // log file name
	Rotation RotationConfig // rotation settings (only used when LogDir is set)
	Writer   io.Writer      // override stderr writer (for testing; nil = os.Stderr)
}

// nopCloser wraps an io.Writer with a no-op Close method.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// NewLoggerWithFile creates a logger with optional dual output (stderr + file with rotation).
// Returns the logger and an io.Closer that must be called on shutdown to flush buffered writes.
// When LogDir is empty, behaves like NewLogger (stderr only).
func NewLoggerWithFile(cfg LoggerConfig) (*slog.Logger, io.Closer) {
	w := cfg.Writer
	if w == nil {
		w = os.Stderr
	}

	opts := &slog.HandlerOptions{
		Level: parseLevel(cfg.Level),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			return redactAndNormalizeAttr(a)
		},
	}

	// Build stderr handler based on format
	var stderrHandler slog.Handler
	if strings.ToLower(cfg.Format) == "text" {
		stderrHandler = slog.NewTextHandler(w, opts)
	} else {
		stderrHandler = slog.NewJSONHandler(w, opts)
	}

	// If no log directory, use stderr only with OTel trace injection
	if cfg.LogDir == "" {
		return slog.New(NewOTelHandler(stderrHandler)), nopCloser{}
	}

	// Build file handler using lumberjack for rotation
	lj := &lumberjack.Logger{
		Filename: filepath.Join(cfg.LogDir, cfg.Filename),
	}
	if cfg.Rotation.Enabled {
		lj.MaxSize = cfg.Rotation.MaxSizeMB
		lj.MaxBackups = cfg.Rotation.MaxFiles
		lj.MaxAge = cfg.Rotation.MaxAgeDays
	}

	// File handler always uses JSON
	fileOpts := &slog.HandlerOptions{
		Level: parseLevel(cfg.Level),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			return redactAndNormalizeAttr(a)
		},
	}
	fileHandler := slog.NewJSONHandler(lj, fileOpts)

	multi := NewMultiHandler(stderrHandler, fileHandler)
	// Wrap with OTelHandler to inject trace_id/span_id from active spans
	return slog.New(NewOTelHandler(multi)), lj
}

// NewLoggerWithOTel wraps an existing slog.Logger with OTel trace context injection.
// Use when you have an already-configured logger and want to add trace context.
func NewLoggerWithOTel(base *slog.Logger) *slog.Logger {
	return slog.New(NewOTelHandler(base.Handler()))
}

// parseLevel converts a string level to slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redactAndNormalizeAttr is the shared ReplaceAttr function for all handlers.
func redactAndNormalizeAttr(a slog.Attr) slog.Attr {
	// Normalize level to upper-case string for tests that assert "ERROR"
	if a.Key == slog.LevelKey {
		if lv, ok := a.Value.Any().(slog.Level); ok {
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

	opts := &slog.HandlerOptions{
		Level: parseLevel(level),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			return redactAndNormalizeAttr(a)
		},
	}
	var handler slog.Handler
	if strings.ToLower(env) == "production" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}
	return slog.New(handler)
}

// Global logger helpers — race-safe using atomic.Pointer (safe for parallel tests).
var globalLogger atomic.Pointer[slog.Logger]

func SetGlobalLogger(l *slog.Logger) { globalLogger.Store(l) }
func GetGlobalLogger() *slog.Logger  { return globalLogger.Load() }

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
