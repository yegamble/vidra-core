package observability

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TracingConfig configures OpenTelemetry tracing. It mirrors the OTEL_* config
// keys so internal/config need not import the OTel SDK.
type TracingConfig struct {
	Enabled        bool
	Endpoint       string // OTLP endpoint host:port (no scheme)
	Protocol       string // "grpc" (default) | "http/protobuf"
	ServiceName    string
	ServiceVersion string
}

// noopShutdown is returned when tracing is disabled.
func noopShutdown(context.Context) error { return nil }

// SetupTracing installs the OpenTelemetry tracer provider and the W3C
// TraceContext propagator when cfg.Enabled, exporting spans over OTLP. It is a
// no-op with zero cost when disabled: the global tracer provider stays the
// default no-op, no exporter is created, and no propagator is installed. It
// returns a shutdown function (a no-op when disabled) that flushes and stops the
// provider; always call it on process shutdown.
//
// The OTLP exporter connects lazily, so a bad/unreachable endpoint does not fail
// startup — spans are dropped until the collector is reachable.
func SetupTracing(ctx context.Context, cfg TracingConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return noopShutdown, nil
	}

	var (
		exporter *otlptrace.Exporter
		err      error
	)
	switch strings.ToLower(strings.TrimSpace(cfg.Protocol)) {
	case "", "grpc":
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
	case "http/protobuf":
		exporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithInsecure(),
		)
	default:
		return nil, fmt.Errorf("observability: unknown OTLP protocol %q (want grpc|http/protobuf)", cfg.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("observability: create OTLP trace exporter: %w", err)
	}

	res := resource.NewSchemaless(
		attribute.String("service.name", cfg.ServiceName),
		attribute.String("service.version", cfg.ServiceVersion),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	// Accept + emit W3C traceparent so vidra-user↔vidra-core traces correlate.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
