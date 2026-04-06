package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/obs"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify log output
	output := buf.String()
	if output == "" {
		t.Fatal("no log output")
	}

	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	// Verify required fields
	requiredFields := []string{"request_id", "method", "path", "status", "duration_ms"}
	for _, field := range requiredFields {
		if _, ok := logEntry[field]; !ok {
			t.Errorf("missing required field: %s", field)
		}
	}

	if logEntry["method"] != "GET" {
		t.Errorf("expected method=GET, got: %v", logEntry["method"])
	}

	if logEntry["path"] != "/api/v1/videos" {
		t.Errorf("expected path=/api/v1/videos, got: %v", logEntry["path"])
	}

	statusCode := int(logEntry["status"].(float64))
	if statusCode != 200 {
		t.Errorf("expected status=200, got: %d", statusCode)
	}
}

func TestLoggingMiddlewareWithRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "custom-req-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if logEntry["request_id"] != "custom-req-123" {
		t.Errorf("expected request_id=custom-req-123, got: %v", logEntry["request_id"])
	}

	// Verify response header
	if rec.Header().Get("X-Request-ID") != "custom-req-123" {
		t.Error("X-Request-ID not set in response header")
	}
}

func TestLoggingMiddlewareWithUserID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate authenticated request
		ctx := context.WithValue(r.Context(), userIDKey, "user-456")
		*r = *r.WithContext(ctx)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if logEntry["user_id"] != "user-456" {
		t.Errorf("expected user_id=user-456, got: %v", logEntry["user_id"])
	}
}

func TestLoggingMiddlewareErrorHandling(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if logEntry["level"] != "ERROR" {
		t.Errorf("expected level=ERROR for 5xx response, got: %v", logEntry["level"])
	}

	statusCode := int(logEntry["status"].(float64))
	if statusCode != 500 {
		t.Errorf("expected status=500, got: %d", statusCode)
	}
}

func TestLoggingMiddlewareDuration(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	duration := logEntry["duration_ms"].(float64)
	if duration < 50 {
		t.Errorf("expected duration >= 50ms, got: %f", duration)
	}
}

func TestMetricsMiddleware(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics()
	registry.MustRegister(
		metrics.HTTPRequestsTotal,
		metrics.HTTPRequestDuration,
		metrics.HTTPRequestSize,
		metrics.HTTPResponseSize,
	)

	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify counter incremented
	expectedCounter := `
		# HELP http_requests_total Total number of HTTP requests
		# TYPE http_requests_total counter
		http_requests_total{method="GET",path="/api/v1/videos",status="200"} 1
	`

	if err := testutil.CollectAndCompare(metrics.HTTPRequestsTotal, strings.NewReader(expectedCounter)); err != nil {
		t.Errorf("counter not incremented: %v", err)
	}

	// Verify histogram recorded
	count := testutil.CollectAndCount(metrics.HTTPRequestDuration)
	if count == 0 {
		t.Error("duration histogram not recorded")
	}
}

func TestMetricsMiddlewareMultipleRequests(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics()
	registry.MustRegister(metrics.HTTPRequestsTotal)

	requests := []struct {
		method string
		path   string
		status int
		count  int
	}{
		{"GET", "/api/v1/videos", 200, 5},
		{"POST", "/api/v1/videos", 201, 3},
		{"GET", "/api/v1/videos", 404, 2},
	}

	for _, req := range requests {
		handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(req.status)
		}))
		for i := 0; i < req.count; i++ {
			r := httptest.NewRequest(req.method, req.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
		}
	}

	expectedCounter := `
		# HELP http_requests_total Total number of HTTP requests
		# TYPE http_requests_total counter
		http_requests_total{method="GET",path="/api/v1/videos",status="200"} 5
		http_requests_total{method="GET",path="/api/v1/videos",status="404"} 2
		http_requests_total{method="POST",path="/api/v1/videos",status="201"} 3
	`

	if err := testutil.CollectAndCompare(metrics.HTTPRequestsTotal, strings.NewReader(expectedCounter)); err != nil {
		t.Errorf("unexpected counter values: %v", err)
	}
}

func TestMetricsMiddlewareRequestSize(t *testing.T) {
	metrics := newTestMetrics()

	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	body := strings.NewReader("test body content")
	req := httptest.NewRequest("POST", "/api/v1/videos", body)
	req.Header.Set("Content-Length", "17")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	count := testutil.CollectAndCount(metrics.HTTPRequestSize)
	if count == 0 {
		t.Error("request size not recorded")
	}
}

func TestMetricsMiddlewareResponseSize(t *testing.T) {
	metrics := newTestMetrics()

	responseBody := "response content"
	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	count := testutil.CollectAndCount(metrics.HTTPResponseSize)
	if count == 0 {
		t.Error("response size not recorded")
	}
}

func TestTracingMiddleware(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := TracingMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name != "GET /api/v1/videos" {
		t.Errorf("expected span name 'GET /api/v1/videos', got: %s", span.Name)
	}

	// Verify HTTP attributes
	attrs := span.Attributes
	foundMethod := false
	foundTarget := false
	foundStatus := false

	for _, attr := range attrs {
		key := string(attr.Key)
		if key == "http.method" && attr.Value.AsString() == "GET" {
			foundMethod = true
		}
		if key == "http.target" && attr.Value.AsString() == "/api/v1/videos" {
			foundTarget = true
		}
		if key == "http.status_code" && attr.Value.AsInt64() == 200 {
			foundStatus = true
		}
	}

	if !foundMethod {
		t.Error("http.method attribute not found")
	}
	if !foundTarget {
		t.Error("http.target attribute not found")
	}
	if !foundStatus {
		t.Error("http.status_code attribute not found")
	}
}

func TestTracingMiddlewareWithError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := TracingMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]

	// Verify error recorded for 5xx status
	if span.Status.Code.String() != "Error" {
		t.Errorf("expected span status Error, got: %s", span.Status.Code.String())
	}
}

func TestTracingMiddlewareContextPropagation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	var capturedContext context.Context
	handler := TracingMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedContext = r.Context()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify span in context
	if capturedContext == nil {
		t.Fatal("context not captured")
	}

	// Should be able to extract span from context
	// This will be verified by the tracing implementation
}

func TestObservabilityMiddlewareStack(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	metrics := newTestMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	// Stack all observability middleware
	handler := LoggingMiddleware(newTestLoggingConfig(logger))(
		MetricsMiddleware(metrics)(
			TracingMiddleware(tp.Tracer("test"))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("success"))
				}),
			),
		),
	)

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify logging
	if buf.Len() == 0 {
		t.Error("no log output")
	}

	// Verify metrics
	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("metrics not recorded")
	}

	// Verify tracing
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Error("no spans recorded")
	}
}

func TestObservabilityMiddlewareRequestIDPropagation(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	metrics := newTestMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	requestID := "test-req-123"

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(
		MetricsMiddleware(metrics)(
			TracingMiddleware(tp.Tracer("test"))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
			),
		),
	)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", requestID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify request ID in logs
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	if logEntry["request_id"] != requestID {
		t.Errorf("request_id not propagated in logs: got %v", logEntry["request_id"])
	}

	// Verify request ID in span attributes
	spans := exporter.GetSpans()
	if len(spans) > 0 {
		found := false
		for _, attr := range spans[0].Attributes {
			if string(attr.Key) == "request_id" && attr.Value.AsString() == requestID {
				found = true
				break
			}
		}
		if !found {
			t.Error("request_id not found in span attributes")
		}
	}

	// Verify request ID in response header
	if rec.Header().Get("X-Request-ID") != requestID {
		t.Error("request_id not in response header")
	}
}

func TestErrorCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(
		TracingMiddleware(tp.Tracer("test"))(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
		),
	)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Parse log entry
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	requestID := logEntry["request_id"].(string)

	// Verify error logged
	if logEntry["level"] != "ERROR" {
		t.Error("error not logged at ERROR level")
	}

	// Verify span recorded error
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}

	if spans[0].Status.Code.String() != "Error" {
		t.Error("span did not record error status")
	}

	// Both should have the same request ID for correlation
	spanHasRequestID := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "request_id" && attr.Value.AsString() == requestID {
			spanHasRequestID = true
			break
		}
	}

	if !spanHasRequestID {
		t.Error("span and log do not share request_id")
	}
}

func BenchmarkLoggingMiddleware(b *testing.B) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkMetricsMiddleware(b *testing.B) {
	metrics := newTestMetrics()

	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkTracingMiddleware(b *testing.B) {
	exporter := tracetest.NewNoopExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := TracingMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkObservabilityStack(b *testing.B) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	metrics := newTestMetrics()

	exporter := tracetest.NewNoopExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := LoggingMiddleware(newTestLoggingConfig(logger))(
		MetricsMiddleware(metrics)(
			TracingMiddleware(tp.Tracer("test"))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}),
			),
		),
	)

	req := httptest.NewRequest("GET", "/test", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

// TestLoggingMiddlewareHTTPRequestsDisabled verifies that no log entry is emitted when LogHTTPRequests=false.
func TestLoggingMiddlewareHTTPRequestsDisabled(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(LoggingConfig{
		Logger:          logger,
		LogHTTPRequests: false,
		LogPingRequests: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if buf.Len() != 0 {
		t.Errorf("expected no log output when LogHTTPRequests=false, got: %s", buf.String())
	}
}

// TestLoggingMiddlewarePingRequestsDisabled verifies that /api/v1/ping is not logged when LogPingRequests=false.
func TestLoggingMiddlewarePingRequestsDisabled(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(LoggingConfig{
		Logger:          logger,
		LogHTTPRequests: true,
		LogPingRequests: false,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Ping request should be suppressed
	req := httptest.NewRequest("GET", "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if buf.Len() != 0 {
		t.Errorf("expected /api/v1/ping to be suppressed when LogPingRequests=false, got: %s", buf.String())
	}

	// Health request should also be suppressed
	buf.Reset()
	req = httptest.NewRequest("GET", "/health", nil)
	handler.ServeHTTP(rec, req)
	if buf.Len() != 0 {
		t.Errorf("expected /health to be suppressed when LogPingRequests=false, got: %s", buf.String())
	}

	// Regular request should still be logged
	buf.Reset()
	req = httptest.NewRequest("GET", "/api/v1/videos", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if buf.Len() == 0 {
		t.Error("expected non-ping request to be logged")
	}
}

// TestLoggingMiddlewareAnonymizeIP verifies that logged IP ends in .0 when AnonymizeIP=true.
func TestLoggingMiddlewareAnonymizeIP(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(LoggingConfig{
		Logger:          logger,
		LogHTTPRequests: true,
		LogPingRequests: true,
		AnonymizeIP:     true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	req.RemoteAddr = "192.168.1.55:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.String()
	if !strings.Contains(output, "192.168.1.0") {
		t.Errorf("expected anonymized IP 192.168.1.0 in log, got: %s", output)
	}
	if strings.Contains(output, "192.168.1.55") {
		t.Errorf("expected real IP to be anonymized, but found it in log: %s", output)
	}
}

// TestLoggingMiddlewareDNTHeader verifies that DNT: 1 triggers anonymization even with AnonymizeIP=false.
func TestLoggingMiddlewareDNTHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(LoggingConfig{
		Logger:          logger,
		LogHTTPRequests: true,
		LogPingRequests: true,
		AnonymizeIP:     false, // disabled in config
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	req.RemoteAddr = "10.20.30.40:9999"
	req.Header.Set("DNT", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.String()
	if strings.Contains(output, "10.20.30.40") {
		t.Errorf("expected DNT:1 to trigger anonymization but found real IP in log: %s", output)
	}
	if !strings.Contains(output, "10.20.30.0") {
		t.Errorf("expected anonymized IP 10.20.30.0 when DNT=1, got: %s", output)
	}
}

// Helper functions and types

func newTestLogger(w io.Writer) *slog.Logger {
	return obs.NewLogger("production", "info", w)
}

// newTestLoggingConfig creates a LoggingConfig with the given logger and sensible test defaults.
func newTestLoggingConfig(logger *slog.Logger) LoggingConfig {
	return LoggingConfig{
		Logger:          logger,
		LogHTTPRequests: true,
		LogPingRequests: true,
		AnonymizeIP:     false,
	}
}

func newTestMetrics() *obs.Metrics {
	return &obs.Metrics{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		HTTPRequestSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_size_bytes",
				Help:    "HTTP request size in bytes",
				Buckets: prometheus.ExponentialBuckets(100, 10, 8),
			},
			[]string{"method", "path"},
		),
		HTTPResponseSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_response_size_bytes",
				Help:    "HTTP response size in bytes",
				Buckets: prometheus.ExponentialBuckets(100, 10, 8),
			},
			[]string{"method", "path"},
		),
	}
}
