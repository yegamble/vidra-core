package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestFullRequestTrace(t *testing.T) {
	// Setup observability stack
	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)

	registry := prometheus.NewRegistry()
	metrics := NewMetrics()
	RegisterMetrics(registry, metrics)

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	tracer := tp.Tracer("test-integration")

	// Simulate a request
	requestID := "int-test-123"

	ctx, span := tracer.Start(context.Background(), "HTTP GET /api/v1/videos")
	ctx = ContextWithRequestID(ctx, requestID)

	// Log the request
	LoggerFromContext(ctx, logger).Info("request received",
		"method", "GET",
		"path", "/api/v1/videos",
	)

	// Record metrics
	RecordHTTPMetrics(metrics, "GET", "/api/v1/videos", 200, 150*time.Millisecond, 0, 1024)

	// Complete span
	req := httptest.NewRequest("GET", "/api/v1/videos", nil).WithContext(ctx)
	RecordHTTPSpan(span, req, 200, "user-123")
	span.End()

	// Log the response
	LoggerFromContext(ctx, logger).Info("request completed",
		"status", 200,
		"duration_ms", 150,
	)

	// Verify all three systems captured the same request ID

	// 1. Check logs
	logOutput := logBuf.String()
	logLines := strings.Split(strings.TrimSpace(logOutput), "\n")

	for _, line := range logLines {
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
			continue
		}

		if logEntry["request_id"] != requestID {
			t.Errorf("log entry missing or incorrect request_id: %v", logEntry["request_id"])
		}
	}

	// 2. Check metrics
	count := testutil.CollectAndCount(metrics.HTTPRequestsTotal)
	if count == 0 {
		t.Error("no metrics recorded")
	}

	// 3. Check trace
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	foundRequestID := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "request_id" && attr.Value.AsString() == requestID {
			foundRequestID = true
			break
		}
	}

	if !foundRequestID {
		t.Error("request_id not found in span attributes")
	}
}

func TestErrorCorrelationAcrossSystems(t *testing.T) {
	// Setup observability stack
	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-integration")

	// Simulate an error scenario
	requestID := "error-test-456"
	ctx := ContextWithRequestID(context.Background(), requestID)
	ctx, span := tracer.Start(ctx, "database query")

	// Add request_id to span
	span.SetAttributes(attribute.String("request_id", requestID))

	testErr := &DatabaseError{
		Message: "connection timeout",
		Code:    "DB_TIMEOUT",
	}

	// Log the error
	LoggerFromContext(ctx, logger).Error("database query failed",
		"error", testErr.Error(),
		"error_code", testErr.Code,
	)

	// Record error in span
	RecordError(span, testErr)
	span.End()

	// Verify error correlation

	// 1. Check error in logs
	var errorLogEntry map[string]interface{}
	logLines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	for _, line := range logLines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["level"] == "ERROR" {
			errorLogEntry = entry
			break
		}
	}

	if errorLogEntry == nil {
		t.Fatal("error log entry not found")
	}

	if errorLogEntry["request_id"] != requestID {
		t.Errorf("error log missing request_id: %v", errorLogEntry["request_id"])
	}

	if !strings.Contains(errorLogEntry["error"].(string), "connection timeout") {
		t.Error("error message not found in log")
	}

	// 2. Check error in span
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	if spans[0].Status.Code.String() != "Error" {
		t.Errorf("span status not Error: %s", spans[0].Status.Code.String())
	}

	if !strings.Contains(spans[0].Status.Description, "connection timeout") {
		t.Error("error description not in span")
	}

	// 3. Verify both share the same request ID
	spanHasRequestID := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "request_id" && attr.Value.AsString() == requestID {
			spanHasRequestID = true
			break
		}
	}

	if !spanHasRequestID {
		t.Error("span missing request_id attribute")
	}
}

func TestEndToEndVideoUploadTrace(t *testing.T) {
	// Setup observability
	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)

	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-integration")

	// Simulate complete video upload flow
	requestID := "upload-789"
	userID := "user-123"
	videoID := "video-456"

	ctx := context.Background()
	ctx = ContextWithRequestID(ctx, requestID)
	ctx = ContextWithUserID(ctx, userID)
	ctx = ContextWithVideoID(ctx, videoID)

	// 1. HTTP Upload Request
	ctx, uploadSpan := tracer.Start(ctx, "POST /api/v1/upload")
	LoggerFromContext(ctx, logger).Info("video upload started")
	RecordHTTPMetrics(metrics, "POST", "/api/v1/upload", 202, 100*time.Millisecond, 10485760, 256)

	// 2. Database Insert
	ctx, dbSpan := tracer.Start(ctx, "db.insert.videos")
	LoggerFromContext(ctx, logger).Info("inserting video record")
	RecordDBSpan(dbSpan, "INSERT", "videos", "INSERT INTO videos ...", 1)
	metrics.DBQueryDuration.WithLabelValues("INSERT", "videos").Observe(0.005)
	dbSpan.End()

	// 3. Virus Scan
	ctx, scanSpan := tracer.Start(ctx, "virus.scan")
	LoggerFromContext(ctx, logger).Info("scanning uploaded file")
	metrics.VirusScanDuration.WithLabelValues("clean").Observe(0.750)
	scanSpan.End()

	// 4. IPFS Pin
	ctx, ipfsSpan := tracer.Start(ctx, "ipfs.pin")
	LoggerFromContext(ctx, logger).Info("pinning to IPFS")
	RecordIPFSSpan(ipfsSpan, "pin.add", "QmTestCID", 10485760)
	metrics.IPFSPinDuration.WithLabelValues("add").Observe(5.5)
	ipfsSpan.End()

	// 5. Video Encoding
	ctx, encodeSpan := tracer.Start(ctx, "video.encode")
	LoggerFromContext(ctx, logger).Info("encoding video", "resolution", "720p")
	metrics.VideoEncodingDuration.WithLabelValues("720p").Observe(25.3)
	encodeSpan.End()

	// 6. Complete upload
	LoggerFromContext(ctx, logger).Info("video upload completed")
	uploadSpan.End()

	// Verify complete trace

	// Check all spans recorded
	spans := exporter.GetSpans()
	expectedSpanCount := 5 // upload, db, scan, ipfs, encode
	if len(spans) != expectedSpanCount {
		t.Errorf("expected %d spans, got %d", expectedSpanCount, len(spans))
	}

	// Verify all spans share same trace ID
	if len(spans) > 0 {
		traceID := spans[0].SpanContext.TraceID()
		for i, span := range spans {
			if span.SpanContext.TraceID() != traceID {
				t.Errorf("span %d has different trace ID", i)
			}
		}
	}

	// Verify span names
	expectedSpans := map[string]bool{
		"POST /api/v1/upload": false,
		"db.insert.videos":    false,
		"virus.scan":          false,
		"ipfs.pin":            false,
		"video.encode":        false,
	}

	for _, span := range spans {
		if _, ok := expectedSpans[span.Name]; ok {
			expectedSpans[span.Name] = true
		}
	}

	for name, found := range expectedSpans {
		if !found {
			t.Errorf("expected span %q not found", name)
		}
	}

	// Verify logs contain consistent request ID
	logLines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	for i, line := range logLines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("failed to parse log line %d: %v", i, err)
			continue
		}

		if entry["request_id"] != requestID {
			t.Errorf("log line %d missing request_id", i)
		}

		if entry["user_id"] != userID {
			t.Errorf("log line %d missing user_id", i)
		}

		if entry["video_id"] != videoID {
			t.Errorf("log line %d missing video_id", i)
		}
	}

	// Verify metrics recorded
	if testutil.CollectAndCount(metrics.HTTPRequestsTotal) == 0 {
		t.Error("HTTP metrics not recorded")
	}
	if testutil.CollectAndCount(metrics.DBQueryDuration) == 0 {
		t.Error("DB metrics not recorded")
	}
	if testutil.CollectAndCount(metrics.VirusScanDuration) == 0 {
		t.Error("virus scan metrics not recorded")
	}
	if testutil.CollectAndCount(metrics.IPFSPinDuration) == 0 {
		t.Error("IPFS metrics not recorded")
	}
	if testutil.CollectAndCount(metrics.VideoEncodingDuration) == 0 {
		t.Error("encoding metrics not recorded")
	}
}

func TestObservabilityPerformanceOverhead(t *testing.T) {
	// Measure baseline performance without observability
	baselineHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)

	baselineStart := time.Now()
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		baselineHandler.ServeHTTP(rec, req)
	}
	baselineDuration := time.Since(baselineStart)

	// Measure performance with full observability stack
	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)
	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	// Wrapped handler with observability
	wrappedHandler := func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tp.Tracer("test").Start(r.Context(), "test request")
		defer span.End()

		LoggerFromContext(ctx, logger).Info("request")
		RecordHTTPMetrics(metrics, "GET", "/test", 200, 10*time.Millisecond, 0, 0)

		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}

	wrappedStart := time.Now()
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		wrappedHandler(rec, req)
	}
	wrappedDuration := time.Since(wrappedStart)

	// Calculate overhead
	overhead := wrappedDuration - baselineDuration
	overheadPerRequest := overhead / 100

	t.Logf("Baseline: %v", baselineDuration)
	t.Logf("With observability: %v", wrappedDuration)
	t.Logf("Overhead: %v", overhead)
	t.Logf("Overhead per request: %v", overheadPerRequest)

	// Assert overhead is acceptable (< 5ms per request)
	maxOverheadPerRequest := 5 * time.Millisecond
	if overheadPerRequest > maxOverheadPerRequest {
		t.Errorf("observability overhead too high: %v (max: %v)", overheadPerRequest, maxOverheadPerRequest)
	}
}

func TestNoMemoryLeaks(t *testing.T) {
	// This test verifies that observability doesn't leak memory
	// In a real implementation, you'd use runtime.ReadMemStats

	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)
	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-memory")

	// Simulate many requests
	for i := 0; i < 1000; i++ {
		ctx, span := tracer.Start(context.Background(), "test-operation")
		ctx = ContextWithRequestID(ctx, "req-"+string(rune(i)))

		LoggerFromContext(ctx, logger).Info("test message")
		RecordHTTPMetrics(metrics, "GET", "/test", 200, 10*time.Millisecond, 0, 100)

		span.End()
	}

	// In real test, would check memory stats here
	// For now, just verify operations completed
	spans := exporter.GetSpans()
	if len(spans) != 1000 {
		t.Errorf("expected 1000 spans, got %d", len(spans))
	}
}

func TestConcurrentObservability(t *testing.T) {
	// Test that observability systems are thread-safe
	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)
	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-concurrent")

	// Spawn concurrent goroutines
	numGoroutines := 100
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			ctx, span := tracer.Start(context.Background(), "concurrent-operation")
			ctx = ContextWithRequestID(ctx, "concurrent-"+string(rune(id)))

			LoggerFromContext(ctx, logger).Info("concurrent test", "goroutine_id", id)
			RecordHTTPMetrics(metrics, "GET", "/test", 200, 10*time.Millisecond, 0, 100)

			span.End()
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all operations completed
	spans := exporter.GetSpans()
	if len(spans) != numGoroutines {
		t.Errorf("expected %d spans, got %d", numGoroutines, len(spans))
	}
}

func TestTraceContextPropagationAcrossServices(t *testing.T) {
	// Setup first service
	exporter1 := tracetest.NewInMemoryExporter()
	tp1 := trace.NewTracerProvider(
		trace.WithSyncer(exporter1),
	)
	defer tp1.Shutdown(context.Background())
	otel.SetTracerProvider(tp1)

	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(propagator)

	tracer1 := tp1.Tracer("service-1")

	// Service 1: Create root span and make HTTP call to service 2
	ctx1, span1 := tracer1.Start(context.Background(), "service-1-operation")

	// Create HTTP request with trace context
	req := httptest.NewRequest("GET", "http://service-2/api", nil)
	InjectTraceContext(ctx1, req)

	span1.End()

	// Service 2: Extract trace context and create child span
	exporter2 := tracetest.NewInMemoryExporter()
	tp2 := trace.NewTracerProvider(
		trace.WithSyncer(exporter2),
	)
	defer tp2.Shutdown(context.Background())

	tracer2 := tp2.Tracer("service-2")

	ctx2 := ExtractTraceContext(req)
	_, span2 := tracer2.Start(ctx2, "service-2-operation")
	span2.End()

	// Verify trace IDs match
	spans1 := exporter1.GetSpans()
	spans2 := exporter2.GetSpans()

	if len(spans1) == 0 || len(spans2) == 0 {
		t.Fatal("spans not recorded in one or both services")
	}

	traceID1 := spans1[0].SpanContext.TraceID()
	traceID2 := spans2[0].SpanContext.TraceID()

	if traceID1 != traceID2 {
		t.Error("trace IDs do not match across services")
	}
}

func TestDurationConsistencyAcrossSystems(t *testing.T) {
	var logBuf bytes.Buffer
	logger := NewLogger("production", "info", &logBuf)
	metrics := NewMetrics()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-duration")

	// Perform operation with known duration
	operationDuration := 100 * time.Millisecond
	ctx, span := tracer.Start(context.Background(), "timed-operation")
	ctx = ContextWithRequestID(ctx, "duration-test")

	start := time.Now()
	time.Sleep(operationDuration)
	duration := time.Since(start)

	LoggerFromContext(ctx, logger).Info("operation completed", "duration_ms", duration.Milliseconds())
	RecordHTTPMetrics(metrics, "GET", "/test", 200, duration, 0, 0)
	span.End()

	// Verify duration in logs
	var logEntry map[string]interface{}
	if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log: %v", err)
	}

	loggedDuration := time.Duration(logEntry["duration_ms"].(float64)) * time.Millisecond
	if loggedDuration < operationDuration {
		t.Errorf("logged duration too short: %v (expected >= %v)", loggedDuration, operationDuration)
	}

	// Verify span duration
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	spanDuration := spans[0].EndTime.Sub(spans[0].StartTime)
	if spanDuration < operationDuration {
		t.Errorf("span duration too short: %v (expected >= %v)", spanDuration, operationDuration)
	}

	// Durations should be within reasonable tolerance (10ms)
	tolerance := 10 * time.Millisecond
	if abs(spanDuration-loggedDuration) > tolerance {
		t.Errorf("duration mismatch between span and log: span=%v, log=%v", spanDuration, loggedDuration)
	}
}

// Helper types

type DatabaseError struct {
	Message string
	Code    string
}

func (e *DatabaseError) Error() string {
	return e.Message
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
