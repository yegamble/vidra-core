package obs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestNewTracerProvider(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		endpoint    string
		wantErr     bool
	}{
		{
			name:        "valid configuration",
			serviceName: "test-service",
			endpoint:    "http://localhost:4318/v1/traces",
			wantErr:     false,
		},
		{
			name:        "empty service name",
			serviceName: "",
			endpoint:    "http://localhost:4318/v1/traces",
			wantErr:     true,
		},
		{
			name:        "empty endpoint",
			serviceName: "test-service",
			endpoint:    "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp, err := NewTracerProvider(tt.serviceName, tt.endpoint)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tp == nil {
				t.Fatal("tracer provider is nil")
			}

			// Clean up
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tp.Shutdown(ctx); err != nil {
				t.Errorf("failed to shutdown tracer provider: %v", err)
			}
		})
	}
}

func TestTracerProviderShutdown(t *testing.T) {
	tp, err := NewTracerProvider("test-service", "http://localhost:4318/v1/traces")
	if err != nil {
		t.Fatalf("failed to create tracer provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = tp.Shutdown(ctx)
	if err != nil {
		t.Errorf("shutdown failed: %v", err)
	}

	// Verify shutdown is idempotent
	err = tp.Shutdown(ctx)
	if err != nil {
		t.Errorf("second shutdown failed: %v", err)
	}
}

func TestSpanCreation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	ctx, span := tracer.Start(context.Background(), "test-operation")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	if spans[0].Name != "test-operation" {
		t.Errorf("expected span name 'test-operation', got %q", spans[0].Name)
	}

	// Verify context contains span
	if oteltrace.SpanFromContext(ctx) == nil {
		t.Error("span not found in context")
	}
}

func TestHTTPSpanAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	// Simulate HTTP request
	req := httptest.NewRequest("POST", "/api/v1/videos", nil)
	req.Header.Set("User-Agent", "test-client")

	ctx, span := tracer.Start(context.Background(), "HTTP POST")
	RecordHTTPSpan(span, req, 201, "user-123")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrs := spans[0].Attributes
	expectedAttrs := map[string]interface{}{
		"http.method":      "POST",
		"http.target":      "/api/v1/videos",
		"http.status_code": int64(201),
		"user.id":          "user-123",
	}

	for key, expectedValue := range expectedAttrs {
		found := false
		for _, attr := range attrs {
			if string(attr.Key) == key {
				found = true
				switch v := expectedValue.(type) {
				case string:
					if attr.Value.AsString() != v {
						t.Errorf("attribute %s: expected %q, got %q", key, v, attr.Value.AsString())
					}
				case int64:
					if attr.Value.AsInt64() != v {
						t.Errorf("attribute %s: expected %d, got %d", key, v, attr.Value.AsInt64())
					}
				}
				break
			}
		}
		if !found {
			t.Errorf("attribute %s not found", key)
		}
	}
}

func TestDatabaseSpanAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	ctx, span := tracer.Start(context.Background(), "db.query")
	RecordDBSpan(span, "SELECT", "videos", "SELECT * FROM videos WHERE id = $1", 5)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrs := spans[0].Attributes
	expectedAttrs := map[string]interface{}{
		"db.system":        "postgresql",
		"db.operation":     "SELECT",
		"db.sql.table":     "videos",
		"db.statement":     "SELECT * FROM videos WHERE id = $1",
		"db.rows_affected": int64(5),
	}

	for key, expectedValue := range expectedAttrs {
		found := false
		for _, attr := range attrs {
			if string(attr.Key) == key {
				found = true
				switch v := expectedValue.(type) {
				case string:
					if attr.Value.AsString() != v {
						t.Errorf("attribute %s: expected %q, got %q", key, v, attr.Value.AsString())
					}
				case int64:
					if attr.Value.AsInt64() != v {
						t.Errorf("attribute %s: expected %d, got %d", key, v, attr.Value.AsInt64())
					}
				}
				break
			}
		}
		if !found {
			t.Errorf("attribute %s not found", key)
		}
	}
}

func TestIPFSSpanAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	ctx, span := tracer.Start(context.Background(), "ipfs.pin")
	RecordIPFSSpan(span, "pin.add", "QmTest123", 1048576)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrs := spans[0].Attributes
	expectedAttrs := map[string]interface{}{
		"ipfs.operation": "pin.add",
		"ipfs.cid":       "QmTest123",
		"ipfs.size":      int64(1048576),
	}

	for key, expectedValue := range expectedAttrs {
		found := false
		for _, attr := range attrs {
			if string(attr.Key) == key {
				found = true
				switch v := expectedValue.(type) {
				case string:
					if attr.Value.AsString() != v {
						t.Errorf("attribute %s: expected %q, got %q", key, v, attr.Value.AsString())
					}
				case int64:
					if attr.Value.AsInt64() != v {
						t.Errorf("attribute %s: expected %d, got %d", key, v, attr.Value.AsInt64())
					}
				}
				break
			}
		}
		if !found {
			t.Errorf("attribute %s not found", key)
		}
	}
}

func TestSpanErrorRecording(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	testErr := errors.New("database connection failed")

	ctx, span := tracer.Start(context.Background(), "db.query")
	RecordError(span, testErr)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	if spans[0].Status.Code != codes.Error {
		t.Errorf("expected status code Error, got %v", spans[0].Status.Code)
	}

	if spans[0].Status.Description != "database connection failed" {
		t.Errorf("expected error description 'database connection failed', got %q", spans[0].Status.Description)
	}

	// Verify error event recorded
	events := spans[0].Events
	found := false
	for _, event := range events {
		if event.Name == "exception" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected exception event not found")
	}
}

func TestContextPropagation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(propagator)

	tracer := tp.Tracer("test-tracer")

	// Create parent span
	ctx, parentSpan := tracer.Start(context.Background(), "parent-operation")
	defer parentSpan.End()

	// Simulate HTTP request with propagation headers
	req := httptest.NewRequest("GET", "/api/v1/videos", nil)
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	// Extract context from headers
	extractedCtx := propagator.Extract(context.Background(), propagation.HeaderCarrier(req.Header))

	// Create child span from extracted context
	_, childSpan := tracer.Start(extractedCtx, "child-operation")
	childSpan.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	// Verify parent-child relationship
	childSpanData := spans[0]
	parentSpanData := spans[1]

	if childSpanData.Parent.SpanID() != parentSpanData.SpanContext.SpanID() {
		t.Error("child span does not reference parent span")
	}

	if childSpanData.SpanContext.TraceID() != parentSpanData.SpanContext.TraceID() {
		t.Error("child and parent spans have different trace IDs")
	}
}

func TestTraceContextInHTTPHeaders(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)

	propagator := propagation.TraceContext{}
	otel.SetTextMapPropagator(propagator)

	tracer := tp.Tracer("test-tracer")

	ctx, span := tracer.Start(context.Background(), "test-operation")
	defer span.End()

	// Create HTTP request and inject trace context
	req := httptest.NewRequest("GET", "/test", nil)
	InjectTraceContext(ctx, req)

	// Verify traceparent header exists
	traceparent := req.Header.Get("traceparent")
	if traceparent == "" {
		t.Error("traceparent header not set")
	}

	// Extract and verify
	extractedCtx := ExtractTraceContext(req)
	extractedSpan := oteltrace.SpanFromContext(extractedCtx)
	if !extractedSpan.SpanContext().IsValid() {
		t.Error("extracted span context is invalid")
	}
}

func TestNestedSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	// Create nested spans
	ctx, rootSpan := tracer.Start(context.Background(), "root")

	_, level1Span := tracer.Start(ctx, "level1")

	_, level2Span := tracer.Start(ctx, "level2")
	level2Span.End()

	level1Span.End()
	rootSpan.End()

	spans := exporter.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(spans))
	}

	// Verify all spans share the same trace ID
	traceID := spans[0].SpanContext.TraceID()
	for _, span := range spans {
		if span.SpanContext.TraceID() != traceID {
			t.Error("spans have different trace IDs")
		}
	}
}

func TestEndToEndTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	// Simulate video upload flow
	ctx, uploadSpan := tracer.Start(context.Background(), "upload.video")
	uploadSpan.SetAttributes(
		attribute.String("video.id", "vid-123"),
		attribute.String("user.id", "user-456"),
	)

	// Database insertion
	_, dbSpan := tracer.Start(ctx, "db.insert")
	RecordDBSpan(dbSpan, "INSERT", "videos", "INSERT INTO videos ...", 1)
	dbSpan.End()

	// IPFS pin
	_, ipfsSpan := tracer.Start(ctx, "ipfs.pin")
	RecordIPFSSpan(ipfsSpan, "pin.add", "QmTest", 1048576)
	ipfsSpan.End()

	// Video encoding
	_, encodeSpan := tracer.Start(ctx, "video.encode")
	encodeSpan.SetAttributes(
		attribute.String("resolution", "720p"),
		attribute.Int("bitrate", 2500000),
	)
	encodeSpan.End()

	uploadSpan.End()

	spans := exporter.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans, got %d", len(spans))
	}

	// Verify span names
	expectedSpans := map[string]bool{
		"upload.video": false,
		"db.insert":    false,
		"ipfs.pin":     false,
		"video.encode": false,
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

	// Verify all spans are part of the same trace
	traceID := spans[0].SpanContext.TraceID()
	for _, span := range spans {
		if span.SpanContext.TraceID() != traceID {
			t.Errorf("span %q has different trace ID", span.Name)
		}
	}
}

func TestSamplingConfiguration(t *testing.T) {
	tests := []struct {
		name           string
		samplingRate   float64
		expectedSample bool
	}{
		{
			name:           "always sample",
			samplingRate:   1.0,
			expectedSample: true,
		},
		{
			name:           "never sample",
			samplingRate:   0.0,
			expectedSample: false,
		},
		{
			name:           "50% sample",
			samplingRate:   0.5,
			expectedSample: true, // This test is probabilistic
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()

			var sampler trace.Sampler
			if tt.samplingRate == 1.0 {
				sampler = trace.AlwaysSample()
			} else if tt.samplingRate == 0.0 {
				sampler = trace.NeverSample()
			} else {
				sampler = trace.TraceIDRatioBased(tt.samplingRate)
			}

			tp := trace.NewTracerProvider(
				trace.WithSyncer(exporter),
				trace.WithSampler(sampler),
			)
			defer tp.Shutdown(context.Background())

			tracer := tp.Tracer("test-tracer")

			ctx, span := tracer.Start(context.Background(), "test-operation")
			span.End()

			if tt.samplingRate == 1.0 {
				if !span.SpanContext().IsSampled() {
					t.Error("expected span to be sampled")
				}
			} else if tt.samplingRate == 0.0 {
				if span.SpanContext().IsSampled() {
					t.Error("expected span not to be sampled")
				}
			}
		})
	}
}

func TestSpanKinds(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	tests := []struct {
		name     string
		spanKind oteltrace.SpanKind
	}{
		{"server span", oteltrace.SpanKindServer},
		{"client span", oteltrace.SpanKindClient},
		{"internal span", oteltrace.SpanKindInternal},
		{"producer span", oteltrace.SpanKindProducer},
		{"consumer span", oteltrace.SpanKindConsumer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter.Reset()

			ctx, span := tracer.Start(
				context.Background(),
				tt.name,
				oteltrace.WithSpanKind(tt.spanKind),
			)
			span.End()

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			if spans[0].SpanKind != tt.spanKind {
				t.Errorf("expected span kind %v, got %v", tt.spanKind, spans[0].SpanKind)
			}
		})
	}
}

func BenchmarkSpanCreation(b *testing.B) {
	exporter := tracetest.NewNoopExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, span := tracer.Start(context.Background(), "test-operation")
		span.End()
		_ = ctx
	}
}

func BenchmarkSpanWithAttributes(b *testing.B) {
	exporter := tracetest.NewNoopExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, span := tracer.Start(context.Background(), "test-operation")
		span.SetAttributes(
			attribute.String("user.id", "user-123"),
			attribute.String("video.id", "vid-456"),
			attribute.Int("http.status_code", 200),
			attribute.String("http.method", "POST"),
		)
		span.End()
		_ = ctx
	}
}

func BenchmarkNestedSpans(b *testing.B) {
	exporter := tracetest.NewNoopExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-tracer")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, rootSpan := tracer.Start(context.Background(), "root")

		_, child1 := tracer.Start(ctx, "child1")
		child1.End()

		_, child2 := tracer.Start(ctx, "child2")
		child2.End()

		rootSpan.End()
	}
}
