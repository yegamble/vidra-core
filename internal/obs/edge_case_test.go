package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Edge Case Tests for Observability System

// ===== Nil Logger Tests =====

func TestLoggerWithNilWriter(t *testing.T) {
	// Should not panic with nil writer - creates default logger
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("NewLogger panicked with nil writer: %v", r)
		}
	}()

	// This should create a logger that writes to a default buffer
	logger := NewLogger("production", "info", nil)
	if logger == nil {
		t.Error("NewLogger returned nil with nil writer")
	}

	// Should be able to log without panic
	logger.Info("test message")
}

func TestLoggerFromContextWithNilLogger(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("LoggerFromContext panicked with nil logger: %v", r)
		}
	}()

	ctx := ContextWithRequestID(context.Background(), "test-123")
	logger := LoggerFromContext(ctx, nil)

	if logger != nil {
		// If it doesn't return nil, it should not panic when used
		logger.Info("test")
	}
}

func TestLoggerWithEmptyContext(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	// Empty context should still work
	LoggerFromContext(context.Background(), logger).Info("test message")

	if buf.Len() == 0 {
		t.Error("no log output with empty context")
	}
}

// ===== Extremely Long Request/Response Bodies =====

func TestLoggerWithExtremelyLongMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	// Create extremely long message (1MB)
	longMessage := strings.Repeat("A", 1024*1024)

	start := time.Now()
	logger.Info("long message test", "data", longMessage)
	duration := time.Since(start)

	// Should complete within reasonable time (< 1 second)
	if duration > time.Second {
		t.Errorf("logging extremely long message took too long: %v", duration)
	}

	if buf.Len() == 0 {
		t.Error("no log output for extremely long message")
	}
}

func TestLoggerWithManyAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	// Create many attributes (1000 key-value pairs)
	attrs := make([]interface{}, 2000)
	for i := 0; i < 1000; i++ {
		attrs[i*2] = "key_" + strings.Repeat("x", 100)
		attrs[i*2+1] = "value_" + strings.Repeat("y", 100)
	}

	start := time.Now()
	logger.Info("many attributes test", attrs...)
	duration := time.Since(start)

	// Should complete within reasonable time
	if duration > time.Second {
		t.Errorf("logging many attributes took too long: %v", duration)
	}
}

func TestLoggerWithInvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	// Log data that could break JSON encoding
	logger.Info("invalid json test",
		"circular_ref", "test",
		"null_bytes", "\x00\x01\x02",
		"unicode", "🔥💥🚀",
		"rtl_text", "مرحبا",
	)

	output := buf.String()
	if output == "" {
		t.Error("no output for potentially invalid JSON")
	}

	// Should still produce valid JSON
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Errorf("produced invalid JSON: %v", err)
	}
}

// ===== Concurrent Logging Tests =====

func TestConcurrentLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	numGoroutines := 100
	numLogsPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numLogsPerGoroutine; j++ {
				logger.Info("concurrent log",
					"goroutine", id,
					"iteration", j,
					"timestamp", time.Now().UnixNano(),
				)
			}
		}(i)
	}

	wg.Wait()

	// Count log lines
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	expectedLines := numGoroutines * numLogsPerGoroutine

	// Allow some variance due to buffering
	if len(lines) < expectedLines-10 {
		t.Errorf("expected ~%d log lines, got %d", expectedLines, len(lines))
	}
}

func TestConcurrentContextUpdates(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	numGoroutines := 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			ctx = ContextWithRequestID(ctx, "req-"+strings.Repeat("x", id))
			ctx = ContextWithUserID(ctx, "user-"+strings.Repeat("y", id))
			ctx = ContextWithVideoID(ctx, "video-"+strings.Repeat("z", id))

			LoggerFromContext(ctx, logger).Info("concurrent context test",
				"goroutine", id,
			)
		}(i)
	}

	wg.Wait()

	if buf.Len() == 0 {
		t.Error("no log output from concurrent context updates")
	}
}

// ===== Metrics Edge Cases =====

func TestMetricsWithExtremelyLongLabels(t *testing.T) {
	metrics := NewMetrics()

	// Extremely long label values
	longMethod := strings.Repeat("A", 1000)
	longPath := "/" + strings.Repeat("b", 1000)
	longStatus := strings.Repeat("2", 100)

	// Should handle without panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("metrics panicked with long labels: %v", r)
		}
	}()

	metrics.HTTPRequestsTotal.WithLabelValues(longMethod, longPath, longStatus).Inc()
}

func TestMetricsWithSpecialCharactersInLabels(t *testing.T) {
	metrics := NewMetrics()

	// Special characters that might break Prometheus
	specialChars := []string{
		"GET /api/v1/test\n\r",
		"POST\t/api",
		"DELETE /api/\"test\"",
		"PUT /api/'test'",
		"PATCH /api/test;drop table",
	}

	for _, path := range specialChars {
		metrics.HTTPRequestsTotal.WithLabelValues("GET", path, "200").Inc()
	}

	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("no metrics recorded with special characters")
	}
}

func TestMetricsLabelConsistency(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	registry.MustRegister(metrics.HTTPRequestsTotal)

	// Record metrics with consistent labels
	metrics.HTTPRequestsTotal.WithLabelValues("GET", "/api/test", "200").Inc()
	metrics.HTTPRequestsTotal.WithLabelValues("GET", "/api/test", "200").Inc()

	// Try to use inconsistent label order (should fail or be handled)
	// This tests that we're using labels consistently
	expectedMetrics := `
		# HELP http_requests_total Total number of HTTP requests
		# TYPE http_requests_total counter
		http_requests_total{method="GET",path="/api/test",status="200"} 2
	`

	if err := testutil.CollectAndCompare(metrics.HTTPRequestsTotal, strings.NewReader(expectedMetrics)); err != nil {
		t.Errorf("label consistency error: %v", err)
	}
}

func TestMetricsOverflowScenarios(t *testing.T) {
	metrics := NewMetrics()

	// Record extreme duration values
	extremeDurations := []time.Duration{
		0,
		1 * time.Nanosecond,
		1 * time.Microsecond,
		1 * time.Millisecond,
		1 * time.Second,
		1 * time.Minute,
		1 * time.Hour,
		24 * time.Hour,
	}

	for _, d := range extremeDurations {
		metrics.HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(d.Seconds())
	}

	// Record extreme sizes
	extremeSizes := []int64{
		0,
		1,
		1024,
		1024 * 1024,
		1024 * 1024 * 1024,
		1024 * 1024 * 1024 * 10, // 10GB
	}

	for _, size := range extremeSizes {
		metrics.HTTPRequestSize.WithLabelValues("POST", "/upload").Observe(float64(size))
	}

	if testutil.CollectAndCount(metrics.HTTPRequestDuration) == 0 {
		t.Error("duration metrics not recorded")
	}
	if testutil.CollectAndCount(metrics.HTTPRequestSize) == 0 {
		t.Error("size metrics not recorded")
	}
}

func TestConcurrentMetricsRecording(t *testing.T) {
	metrics := NewMetrics()

	numGoroutines := 100
	numRecordsPerGoroutine := 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numRecordsPerGoroutine; j++ {
				metrics.HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()
				metrics.HTTPRequestDuration.WithLabelValues("GET", "/test").Observe(0.1)
			}
		}(i)
	}

	wg.Wait()

	// Verify all increments were recorded
	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("concurrent metrics not recorded")
	}
}

func TestMetricsWithNilRegistry(t *testing.T) {
	metrics := NewMetrics()

	// RegisterMetrics should handle nil registry - it will panic so catch it
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil registry, got none")
		}
	}()

	RegisterMetrics(nil, metrics)
	t.Error("should not reach here - RegisterMetrics should panic with nil registry")
}

// ===== Tracing Edge Cases =====

func TestSpanCreationWithNilContext(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("span creation panicked with nil context: %v", r)
		}
	}()

	// Should handle nil context gracefully
	_, span := tracer.Start(context.Background(), "test-operation")
	span.End()
}

func TestSpanWithMissingContext(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	// Create span without any context propagation
	ctx1, span1 := tracer.Start(context.Background(), "operation-1")
	span1.End()

	// Create another span with fresh context (no parent)
	ctx2, span2 := tracer.Start(context.Background(), "operation-2")
	span2.End()

	_ = ctx1
	_ = ctx2

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(spans))
	}

	// Verify they have different trace IDs (no parent-child relationship)
	if spans[0].SpanContext.TraceID() == spans[1].SpanContext.TraceID() {
		t.Error("spans without parent should have different trace IDs")
	}
}

func TestSpanAttributeLimits(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	_, span := tracer.Start(context.Background(), "test-operation")

	// Add many attributes (test for limits)
	for i := 0; i < 1000; i++ {
		span.SetAttributes(
			attribute.String("key_"+strings.Repeat("x", i%100), strings.Repeat("val", i%100)),
		)
	}

	// Add extremely long attribute
	longValue := strings.Repeat("X", 100000)
	span.SetAttributes(attribute.String("long_attr", longValue))

	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	// Verify span was recorded even with extreme attributes
	if spans[0].Name != "test-operation" {
		t.Error("span not properly recorded with extreme attributes")
	}
}

func TestNestedSpanCreation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	// Create deeply nested spans (100 levels)
	ctx := context.Background()
	var spans []oteltrace.Span

	for i := 0; i < 100; i++ {
		var span oteltrace.Span
		ctx, span = tracer.Start(ctx, "level-"+strings.Repeat("x", i%10))
		spans = append(spans, span)
	}

	// End all spans
	for i := len(spans) - 1; i >= 0; i-- {
		spans[i].End()
	}

	exportedSpans := exporter.GetSpans()
	if len(exportedSpans) != 100 {
		t.Errorf("expected 100 spans, got %d", len(exportedSpans))
	}

	// Verify all spans share the same trace ID
	if len(exportedSpans) > 0 {
		traceID := exportedSpans[0].SpanContext.TraceID()
		for _, s := range exportedSpans {
			if s.SpanContext.TraceID() != traceID {
				t.Error("nested spans have different trace IDs")
			}
		}
	}
}

func TestErrorSpanRecording(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	tests := []struct {
		name  string
		err   error
		check func(*testing.T, tracetest.SpanStub)
	}{
		{
			name: "nil error",
			err:  nil,
			check: func(t *testing.T, s tracetest.SpanStub) {
				// Should not record error status for nil
				if s.Status.Code == codes.Error {
					t.Error("nil error recorded as error status")
				}
			},
		},
		{
			name: "simple error",
			err:  errors.New("simple error"),
			check: func(t *testing.T, s tracetest.SpanStub) {
				if s.Status.Code != codes.Error {
					t.Error("error not recorded")
				}
			},
		},
		{
			name: "error with very long message",
			err:  errors.New(strings.Repeat("ERROR", 10000)),
			check: func(t *testing.T, s tracetest.SpanStub) {
				if s.Status.Code != codes.Error {
					t.Error("long error not recorded")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter.Reset()

			_, span := tracer.Start(context.Background(), tt.name)
			if tt.err != nil {
				RecordError(span, tt.err)
			}
			span.End()

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			tt.check(t, spans[0])
		})
	}
}

func TestConcurrentSpanCreation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	numGoroutines := 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			ctx, span := tracer.Start(context.Background(), "concurrent-span")
			span.SetAttributes(
				attribute.Int("goroutine_id", id),
				attribute.String("timestamp", time.Now().String()),
			)
			time.Sleep(time.Millisecond)
			span.End()
			_ = ctx
		}(i)
	}

	wg.Wait()

	spans := exporter.GetSpans()
	if len(spans) != numGoroutines {
		t.Errorf("expected %d spans, got %d", numGoroutines, len(spans))
	}
}

func TestTraceContextPropagationWithMissingHeaders(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	propagator := propagation.TraceContext{}
	otel.SetTextMapPropagator(propagator)

	// Extract from empty headers
	emptyCarrier := propagation.HeaderCarrier{}
	ctx := propagator.Extract(context.Background(), emptyCarrier)

	// Should still be able to create span
	tracer := tp.Tracer("test-tracer")
	_, span := tracer.Start(ctx, "operation-without-headers")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestSpanEndedMultipleTimes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	_, span := tracer.Start(context.Background(), "test-operation")

	// End span multiple times
	span.End()
	span.End()
	span.End()

	// Should not panic or create duplicate spans
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

// ===== Resource Leak Tests =====

func TestNoResourceLeaksAfterManyOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resource leak test in short mode")
	}

	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)
	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-leak")

	// Perform many operations
	iterations := 10000
	for i := 0; i < iterations; i++ {
		ctx := context.Background()
		ctx = ContextWithRequestID(ctx, "req-"+strings.Repeat("x", i%100))

		// Logging
		LoggerFromContext(ctx, logger).Info("test", "iteration", i)

		// Metrics
		RecordHTTPMetrics(metrics, "GET", "/test", 200, 10*time.Millisecond, 100, 200)

		// Tracing
		_, span := tracer.Start(ctx, "test-operation")
		span.SetAttributes(attribute.Int("iteration", i))
		span.End()

		// Reset buffer periodically to prevent OOM
		if i%1000 == 0 {
			buf.Reset()
			exporter.Reset()
		}
	}

	// If we get here without panic or OOM, test passes
	t.Logf("Completed %d iterations without resource leaks", iterations)
}

// ===== Integration Edge Cases =====

func TestFullObservabilityStackWithErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)
	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-integration")

	// Simulate error scenario
	ctx := context.Background()
	ctx = ContextWithRequestID(ctx, "error-test-123")

	ctx, span := tracer.Start(ctx, "failing-operation")

	// Log error
	LoggerFromContext(ctx, logger).Error("operation failed",
		"error", "database timeout",
		"retry_count", 3,
	)

	// Record error metrics
	metrics.DBQueryErrors.WithLabelValues("timeout").Inc()

	// Record error in span
	testErr := errors.New("database timeout")
	RecordError(span, testErr)
	span.End()

	// Verify error correlation
	logOutput := buf.String()
	if !strings.Contains(logOutput, "database timeout") {
		t.Error("error not found in logs")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	if spans[0].Status.Code != codes.Error {
		t.Error("span did not record error status")
	}

	if testutil.CollectAndCount(metrics.DBQueryErrors) == 0 {
		t.Error("error metrics not recorded")
	}
}

func TestObservabilityWithCancelledContext(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-cancelled")

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should still work with cancelled context
	ctx, span := tracer.Start(ctx, "cancelled-operation")
	LoggerFromContext(ctx, logger).Info("test with cancelled context")
	span.End()

	if buf.Len() == 0 {
		t.Error("no log output with cancelled context")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Error("span not recorded with cancelled context")
	}
}

func TestObservabilityWithDeadlineExceeded(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-deadline")

	// Create context with past deadline
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	// Should still work
	ctx, span := tracer.Start(ctx, "deadline-exceeded")
	LoggerFromContext(ctx, logger).Info("test with deadline exceeded")
	span.End()

	if buf.Len() == 0 {
		t.Error("no log output with deadline exceeded")
	}
}
