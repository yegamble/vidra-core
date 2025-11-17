package obs

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// NewTracerProvider creates a basic tracer provider. Endpoint is validated but not used
// to avoid network requirements in unit tests.
func NewTracerProvider(serviceName, endpoint string) (*trace.TracerProvider, error) {
	if serviceName == "" || endpoint == "" {
		return nil, errors.New("invalid tracer configuration")
	}
	// Resource with service name
	res := resource.NewWithAttributes(
		"",
		attribute.String("service.name", serviceName),
	)
	tp := trace.NewTracerProvider(trace.WithResource(res))
	return tp, nil
}

// RecordHTTPSpan annotates a span with HTTP request information.
func RecordHTTPSpan(span oteltrace.Span, req *http.Request, statusCode int, userID string) {
	if span == nil || req == nil {
		return
	}
	span.SetAttributes(
		attribute.String("http.method", req.Method),
		attribute.String("http.target", req.URL.Path),
		attribute.Int("http.status_code", statusCode),
	)
	if userID != "" {
		span.SetAttributes(attribute.String("user.id", userID))
	}
}

// RecordDBSpan annotates a span with DB operation details.
func RecordDBSpan(span oteltrace.Span, operation, table, statement string, rowsAffected int) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", operation),
		attribute.String("db.sql.table", table),
		attribute.String("db.statement", statement),
		attribute.Int("db.rows_affected", rowsAffected),
	)
}

// RecordIPFSSpan annotates a span with IPFS operation details.
func RecordIPFSSpan(span oteltrace.Span, operation, cid string, size int64) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String("ipfs.operation", operation),
		attribute.String("ipfs.cid", cid),
		attribute.Int64("ipfs.size", size),
	)
}

// RecordError marks the span as error and records an exception event.
func RecordError(span oteltrace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// InjectTraceContext injects the W3C trace context into the outgoing HTTP request headers.
func InjectTraceContext(ctx context.Context, req *http.Request) {
	if req == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// ExtractTraceContext extracts the W3C trace context from the HTTP request headers.
func ExtractTraceContext(req *http.Request) context.Context {
	if req == nil {
		return context.Background()
	}
	return otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(req.Header))
}
