package middleware

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"athena/internal/obs"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Context keys
type contextKey string

const (
	requestIDKey contextKey = "request_id"
	userIDKey    contextKey = "user_id"
)

// LoggingMiddleware logs request/response details.
// The logger parameter is accepted for compatibility with tests; if it is a *slog.Logger,
// it will be used. Otherwise a default JSON logger to a buffer is created and discarded.
func LoggingMiddleware(logger interface{}) func(http.Handler) http.Handler {
	var base *slog.Logger
	switch v := logger.(type) {
	case *slog.Logger:
		base = v
	case io.Writer:
		base = obs.NewLogger("production", "info", v)
	default:
		// Fallback to in-memory buffer to avoid nil deref in tests
		base = obs.NewLogger("production", "info", &bytes.Buffer{})
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Capture status/size
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// Get or generate request ID
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				reqID = generateRequestID()
			}
			w.Header().Set("X-Request-ID", reqID)

			// Store request_id in context for downstream middleware
			ctx := context.WithValue(r.Context(), requestIDKey, reqID)
			r = r.WithContext(ctx)

			next.ServeHTTP(rw, r)

			// Check for user_id in context after handler runs
			var userID string
			if uid := r.Context().Value(userIDKey); uid != nil {
				if id, ok := uid.(string); ok {
					userID = id
				}
			}

			// Level based on status
			level := slog.LevelInfo
			if rw.status >= 500 {
				level = slog.LevelError
			}

			// Build attrs
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			}

			attrs = append(attrs, "request_id", reqID)

			if userID != "" {
				attrs = append(attrs, "user_id", userID)
			}

			// Log
			l := base
			if level == slog.LevelError {
				l.Error("http request", attrs...)
			} else {
				l.Info("http request", attrs...)
			}
		})
	}
}

// MetricsMiddleware records HTTP metrics using obs.Metrics
func MetricsMiddleware(m *obs.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			obs.RecordHTTPMetrics(m, r.Method, r.URL.Path, rw.status, time.Since(start), readContentLength(r), int64(rw.size))
		})
	}
}

// TracingMiddleware creates a span around the request using the provided tracer.
func TracingMiddleware(tracer oteltrace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spanName := r.Method + " " + r.URL.Path
			ctx, span := tracer.Start(r.Context(), spanName)
			defer span.End()

			// Wrap response writer to capture status
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// Add request ID to span if present (check context first, then header)
			var reqID string
			if id := r.Context().Value(requestIDKey); id != nil {
				if s, ok := id.(string); ok {
					reqID = s
				}
			}
			if reqID == "" {
				reqID = r.Header.Get("X-Request-ID")
			}
			if reqID != "" {
				span.SetAttributes(attribute.String("request_id", reqID))
			}

			// Call next handler with updated context
			next.ServeHTTP(rw, r.WithContext(ctx))

			// Record HTTP span attributes
			obs.RecordHTTPSpan(span, r, rw.status, "")

			// Mark span as error for 5xx responses
			if rw.status >= 500 {
				span.SetStatus(codes.Error, "HTTP "+strconv.Itoa(rw.status))
			}
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func readContentLength(r *http.Request) int64 {
	if r.ContentLength > 0 {
		return r.ContentLength
	}
	if s := r.Header.Get("Content-Length"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 0
}

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if entropy source fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
