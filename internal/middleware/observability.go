package middleware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"vidra-core/internal/obs"

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

// LoggingConfig configures the LoggingMiddleware behavior.
type LoggingConfig struct {
	// Logger is the slog logger to write to. If nil, a no-op fallback is used.
	Logger *slog.Logger
	// AnonymizeIP zeros the last octet of IPv4 (or last 80 bits of IPv6) in logs.
	// Also activated per-request by the DNT: 1 header.
	AnonymizeIP bool
	// LogHTTPRequests controls whether HTTP request log entries are emitted at all.
	// When false, no request logging occurs (useful for very high traffic deployments).
	LogHTTPRequests bool
	// LogPingRequests controls whether /api/v1/ping and /health requests are logged.
	// When false, health check requests are silently skipped.
	LogPingRequests bool
}

// LoggingMiddleware logs request/response details using the provided config.
func LoggingMiddleware(cfg LoggingConfig) func(http.Handler) http.Handler {
	var base *slog.Logger
	if cfg.Logger != nil {
		base = cfg.Logger
	} else {
		// Fallback to in-memory buffer to avoid nil deref
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

			// Skip logging if HTTP request logging is disabled
			if !cfg.LogHTTPRequests {
				return
			}

			// Skip ping/health requests when LogPingRequests is false
			if !cfg.LogPingRequests {
				path := r.URL.Path
				if path == "/api/v1/ping" || path == "/health" || path == "/ready" {
					return
				}
			}

			// Check for user_id in context after handler runs
			var userID string
			if uid := r.Context().Value(userIDKey); uid != nil {
				if id, ok := uid.(string); ok {
					userID = id
				}
			}

			// Determine log level by status code
			level := slog.LevelInfo
			if rw.status >= 500 {
				level = slog.LevelError
			} else if rw.status >= 400 {
				level = slog.LevelWarn
			}

			// Determine client IP, applying anonymization when needed
			clientIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(clientIP); err == nil {
				clientIP = host
			}
			if cfg.AnonymizeIP || r.Header.Get("DNT") == "1" {
				clientIP = obs.AnonymizeIP(clientIP)
			}

			// Build attrs
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", reqID,
				"ip", clientIP,
				"response_size", rw.size,
				"request_content_length", readContentLength(r),
			}

			if userID != "" {
				attrs = append(attrs, "user_id", userID)
			}

			// Log at appropriate level
			switch level {
			case slog.LevelError:
				base.Error("http request", attrs...)
			case slog.LevelWarn:
				base.Warn("http request", attrs...)
			default:
				base.Info("http request", attrs...)
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

// responseWriter wraps http.ResponseWriter to capture status code and response size.
// Implements http.Hijacker and http.Flusher so WebSocket upgrades and SSE streaming work.
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

// Hijack delegates to the underlying ResponseWriter's Hijacker if available.
// Required for WebSocket upgrade in chat and livestream handlers.
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// Flush delegates to the underlying ResponseWriter's Flusher if available.
// Required for SSE (Server-Sent Events) streaming.
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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

// Ensure responseWriter implements the io.Writer interface (used in benchmarks/tests)
var _ io.Writer = (*responseWriter)(nil)
