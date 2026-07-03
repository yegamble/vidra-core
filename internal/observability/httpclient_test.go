package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// TestOTelHTTPTransportInjectsTraceparent proves the outbound instrumentation
// stamps a W3C traceparent onto a request made through an SSRF-guarded client —
// the propagation that lets a vidra-core→remote call continue an active trace
// (federation/import/whisper/atproto).
func TestOTelHTTPTransportInjectsTraceparent(t *testing.T) {
	// Install a real (always-sampling) provider + the W3C propagator, exactly as
	// SetupTracing does when OTEL_ENABLED. Restore globals afterwards.
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
		_ = tp.Shutdown(context.Background())
	}()

	// Wire the outbound-transport wrapper exactly as cmd/api does when OTel is on.
	urlsafety.SetTransportWrapper(OTelHTTPTransport)
	defer urlsafety.SetTransportWrapper(nil)

	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// httptest listens on loopback, which the SSRF guard blocks by default; the
	// dev AllowPrivate escape hatch lets the test reach it (the same knob e2e uses).
	client := urlsafety.Guard{AllowPrivate: true}.NewClient(5 * time.Second)

	ctx, span := tp.Tracer("test").Start(context.Background(), "outbound")
	defer span.End()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if gotTraceparent == "" {
		t.Fatal("outbound request carried no W3C traceparent header")
	}
	// "00-<32 hex trace>-<16 hex span>-<2 hex flags>" == 55 chars.
	if !strings.HasPrefix(gotTraceparent, "00-") || len(gotTraceparent) != 55 {
		t.Errorf("malformed traceparent %q", gotTraceparent)
	}
}

// TestNoWrapperNoInjection confirms the zero-cost default: with no wrapper
// installed (OTel off), an outbound call carries no traceparent.
func TestNoWrapperNoInjection(t *testing.T) {
	urlsafety.SetTransportWrapper(nil)

	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := urlsafety.Guard{AllowPrivate: true}.NewClient(5 * time.Second)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if gotTraceparent != "" {
		t.Errorf("expected no traceparent with OTel off, got %q", gotTraceparent)
	}
}
