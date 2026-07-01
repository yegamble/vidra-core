package observability

import (
	"context"
	"testing"
)

func TestSetupTracingDisabledIsNoop(t *testing.T) {
	shutdown, err := SetupTracing(context.Background(), TracingConfig{Enabled: false})
	if err != nil {
		t.Fatalf("SetupTracing(disabled) error = %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown must not be nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown error = %v", err)
	}
}

func TestSetupTracingUnknownProtocol(t *testing.T) {
	if _, err := SetupTracing(context.Background(), TracingConfig{
		Enabled: true, Protocol: "smoke-signal", Endpoint: "localhost:4317",
	}); err == nil {
		t.Fatal("SetupTracing expected error for unknown protocol, got nil")
	}
}

func TestSetupTracingEnabledConstructsProvider(t *testing.T) {
	// The OTLP exporter connects lazily, so this does not require a collector.
	shutdown, err := SetupTracing(context.Background(), TracingConfig{
		Enabled: true, Protocol: "grpc", Endpoint: "localhost:4317",
		ServiceName: "vidra-core-test", ServiceVersion: "0.0.0",
	})
	if err != nil {
		t.Fatalf("SetupTracing(enabled) error = %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	if shutdown == nil {
		t.Fatal("shutdown must not be nil")
	}
}
