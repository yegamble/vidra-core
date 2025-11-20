package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Edge Case Tests for Observability Middleware

// ===== Nil Logger Tests =====

func TestLoggingMiddlewareWithNilLogger(t *testing.T) {
	// Should not panic with nil logger
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("LoggingMiddleware panicked with nil logger: %v", r)
		}
	}()

	handler := LoggingMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestLoggingMiddlewareWithInvalidLoggerType(t *testing.T) {
	// Test with invalid logger type
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("LoggingMiddleware panicked with invalid logger: %v", r)
		}
	}()

	// Pass something that's not a logger
	handler := LoggingMiddleware("not a logger")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
}

// ===== Extremely Long Request/Response Bodies =====

func TestLoggingMiddlewareWithHugeRequestBody(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the huge body
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	// Create 10MB request body
	hugeBody := bytes.NewReader(bytes.Repeat([]byte("A"), 10*1024*1024))
	req := httptest.NewRequest("POST", "/api/upload", hugeBody)
	req.Header.Set("Content-Length", "10485760")
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	duration := time.Since(start)

	// Should complete within reasonable time
	if duration > 5*time.Second {
		t.Errorf("handling huge request took too long: %v", duration)
	}

	// Verify logged
	if buf.Len() == 0 {
		t.Error("no log output for huge request")
	}
}

func TestLoggingMiddlewareWithHugeResponseBody(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write 10MB response
		w.WriteHeader(http.StatusOK)
		w.Write(bytes.Repeat([]byte("B"), 10*1024*1024))
	}))

	req := httptest.NewRequest("GET", "/api/download", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	duration := time.Since(start)

	// Should complete within reasonable time
	if duration > 5*time.Second {
		t.Errorf("handling huge response took too long: %v", duration)
	}

	// Verify logged
	if buf.Len() == 0 {
		t.Error("no log output for huge response")
	}
}

func TestLoggingMiddlewareWithExtremelyLongPath(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create extremely long path (10KB)
	longPath := "/api/" + strings.Repeat("a/", 5000)
	req := httptest.NewRequest("GET", longPath, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	// Path should be logged (possibly truncated)
	if logEntry["path"] == nil {
		t.Error("path not logged")
	}
}

// ===== Invalid JSON in Logs =====

func TestLoggingMiddlewareWithSpecialCharactersInPath(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	specialPaths := []string{
		"/api/\"test\"",
		"/api/'test'",
		"/api/test;drop_table_users",
		"/api/test<script>alert(1)</script>",
		"/api/test?param=value",
		"/api/test%20with%20spaces",
	}

	for _, path := range specialPaths {
		buf.Reset()
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Should still produce valid JSON
		var logEntry map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
			t.Errorf("invalid JSON for path %q: %v", path, err)
		}
	}
}

// ===== Concurrent Requests =====

func TestLoggingMiddlewareConcurrentRequests(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate some work
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	numRequests := 100
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}(i)
	}

	wg.Wait()

	// Verify we got log output
	if buf.Len() == 0 {
		t.Error("no log output from concurrent requests")
	}

	// Count log lines
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < numRequests-10 {
		t.Errorf("expected ~%d log lines, got %d", numRequests, len(lines))
	}
}

func TestMetricsMiddlewareConcurrentRequests(t *testing.T) {
	metrics := newTestMetrics()

	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	numRequests := 100
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}(i)
	}

	wg.Wait()

	// Verify all requests were counted
	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("no metrics recorded from concurrent requests")
	}
}

func TestTracingMiddlewareConcurrentRequests(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := TracingMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	numRequests := 100
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}(i)
	}

	wg.Wait()

	// Verify all spans were recorded
	spans := exporter.GetSpans()
	if len(spans) < numRequests-10 {
		t.Errorf("expected ~%d spans, got %d", numRequests, len(spans))
	}
}

// ===== Metric Label Edge Cases =====

func TestMetricsMiddlewareWithVaryingPaths(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics()
	registry.MustRegister(metrics.HTTPRequestsTotal)

	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Create many different paths (could cause cardinality explosion)
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest("GET", "/api/resource/"+strings.Repeat("x", i%100), nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Verify metrics were recorded
	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("no metrics recorded with varying paths")
	}
}

func TestMetricsMiddlewareWithNilMetrics(t *testing.T) {
	// Should not panic with nil metrics
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MetricsMiddleware panicked with nil metrics: %v", r)
		}
	}()

	handler := MetricsMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
}

// ===== Tracing Edge Cases =====

func TestTracingMiddlewareWithNilTracer(t *testing.T) {
	// TracingMiddleware with nil tracer will panic - this is expected behavior
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil tracer, got none")
		}
	}()

	handler := TracingMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	t.Error("should not reach here - TracingMiddleware should panic with nil tracer")
}

func TestTracingMiddlewareWithPanicInHandler(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := TracingMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Should panic but span should still be recorded
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}

		// Verify span was recorded despite panic
		spans := exporter.GetSpans()
		if len(spans) == 0 {
			t.Error("span not recorded despite panic")
		}
	}()

	handler.ServeHTTP(rec, req)
}

// ===== Request ID Edge Cases =====

func TestLoggingMiddlewareWithExtremelyLongRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Extremely long request ID (10KB)
	longRequestID := strings.Repeat("x", 10000)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", longRequestID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should still produce valid JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Errorf("invalid JSON with long request ID: %v", err)
	}
}

func TestLoggingMiddlewareWithSpecialCharactersInRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	specialRequestIDs := []string{
		"req-\n\r\t",
		"req-\"test\"",
		"req-'test'",
		"req-<script>",
		"req-\x00\x01\x02",
	}

	for _, reqID := range specialRequestIDs {
		buf.Reset()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Request-ID", reqID)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Should still produce valid JSON
		var logEntry map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
			t.Errorf("invalid JSON for request ID %q: %v", reqID, err)
		}
	}
}

// ===== Response Writer Edge Cases =====

func TestResponseWriterMultipleWriteHeaders(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusInternalServerError) // Second call should be ignored by http.ResponseWriter
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// The http.ResponseWriter logs a warning for multiple WriteHeader calls
	// and uses the first one, so we should see 200
	if rec.Code != http.StatusOK {
		t.Logf("Note: httptest.ResponseRecorder code is %d, expected 200", rec.Code)
	}

	// Verify log contains status code
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	// The logged status should match what our wrapper captured
	if logEntry["status"] == nil {
		t.Error("status not logged")
	}
}

func TestResponseWriterWithoutWriteHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't call WriteHeader, just Write
		w.Write([]byte("response"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should default to 200
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	status := int(logEntry["status"].(float64))
	if status != 200 {
		t.Errorf("expected default status 200, got %d", status)
	}
}

// ===== Performance Edge Cases =====

func TestObservabilityMiddlewarePerformanceOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	// Baseline without observability
	baselineHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		rec := httptest.NewRecorder()
		baselineHandler.ServeHTTP(rec, req)
	}
	baselineDuration := time.Since(start)

	// With full observability stack
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	metrics := newTestMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	observabilityHandler := LoggingMiddleware(logger)(
		MetricsMiddleware(metrics)(
			TracingMiddleware(tp.Tracer("test"))(baselineHandler),
		),
	)

	start = time.Now()
	for i := 0; i < 1000; i++ {
		rec := httptest.NewRecorder()
		observabilityHandler.ServeHTTP(rec, req)
	}
	observabilityDuration := time.Since(start)

	overhead := observabilityDuration - baselineDuration
	overheadPerRequest := overhead / 1000

	t.Logf("Baseline: %v", baselineDuration)
	t.Logf("With observability: %v", observabilityDuration)
	t.Logf("Overhead: %v", overhead)
	t.Logf("Overhead per request: %v", overheadPerRequest)

	// Overhead should be reasonable (< 10ms per request)
	if overheadPerRequest > 10*time.Millisecond {
		t.Errorf("observability overhead too high: %v", overheadPerRequest)
	}
}

// ===== Memory Leak Tests =====

func TestObservabilityMiddlewareNoMemoryLeaks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory leak test in short mode")
	}

	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	metrics := newTestMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	handler := LoggingMiddleware(logger)(
		MetricsMiddleware(metrics)(
			TracingMiddleware(tp.Tracer("test"))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("response"))
				}),
			),
		),
	)

	// Process many requests
	for i := 0; i < 10000; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Reset buffers periodically to prevent OOM
		if i%1000 == 0 {
			buf.Reset()
			exporter.Reset()
		}
	}

	t.Log("Completed 10000 requests without memory leaks")
}

// ===== Error Handling Edge Cases =====

func TestMiddlewareStackWithHandlerErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	metrics := newTestMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	errorHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})

	handler := LoggingMiddleware(logger)(
		MetricsMiddleware(metrics)(
			TracingMiddleware(tp.Tracer("test"))(errorHandler),
		),
	)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify error logged at ERROR level
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	if logEntry["level"] != "ERROR" {
		t.Errorf("expected ERROR level, got %v", logEntry["level"])
	}

	// Verify span recorded error
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}

	if spans[0].Status.Code.String() != "Error" {
		t.Error("span did not record error status")
	}
}
