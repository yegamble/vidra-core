package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOTelHandler_InjectsTraceContext(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewOTelHandler(inner)
	logger := slog.New(handler)

	// Create a real span using the SDK
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	ctx, span := tp.Tracer("test").Start(context.Background(), "test-span")
	defer span.End()

	logger.InfoContext(ctx, "test message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := entry["trace_id"]; !ok {
		t.Error("expected trace_id in log entry within traced context")
	}
	if _, ok := entry["span_id"]; !ok {
		t.Error("expected span_id in log entry within traced context")
	}

	// Both should be non-empty hex strings
	traceID, _ := entry["trace_id"].(string)
	spanID, _ := entry["span_id"].(string)
	if traceID == "" || traceID == "00000000000000000000000000000000" {
		t.Errorf("expected non-zero trace_id, got %q", traceID)
	}
	if spanID == "" || spanID == "0000000000000000" {
		t.Errorf("expected non-zero span_id, got %q", spanID)
	}
}

func TestOTelHandler_OmitsFieldsWithoutTrace(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewOTelHandler(inner)
	logger := slog.New(handler)

	// No span in context
	logger.InfoContext(context.Background(), "no trace message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should not have trace_id or span_id
	if _, ok := entry["trace_id"]; ok {
		t.Errorf("expected no trace_id in untraced context, got: %v", entry["trace_id"])
	}
	if _, ok := entry["span_id"]; ok {
		t.Errorf("expected no span_id in untraced context, got: %v", entry["span_id"])
	}
}

func TestOTelHandler_DelegatesWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewOTelHandler(inner)

	// WithAttrs should work
	withAttrs := handler.WithAttrs([]slog.Attr{slog.String("service", "test")})
	logger := slog.New(withAttrs)
	logger.Info("with attrs")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["service"] != "test" {
		t.Errorf("expected service=test in WithAttrs output, got %v", entry["service"])
	}
}
