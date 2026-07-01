package observability

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewLogger constructs the process logger from configuration. It is the single
// place logger construction lives (see .ralph/specs/observability.md): callers
// build one at startup, set it as slog.Default(), and inject it into the
// server/services.
//
// level is one of debug|info|warn|error (case-insensitive; empty = info).
// format selects the slog handler: "json" (default, production) or "text"
// (developer-friendly local runs). An unrecognised level or format is returned
// as an error so misconfiguration fails fast at startup rather than silently
// degrading observability.
func NewLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("observability: unknown log format %q (want json|text)", format)
	}
	return slog.New(h), nil
}

// ParseLevel maps a level name to an slog.Level. It accepts debug|info|warn|error
// (case-insensitive), treats "warning" as an alias for warn, and an empty string
// as info. Any other value is an error.
func ParseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("observability: unknown log level %q (want debug|info|warn|error)", level)
	}
}
