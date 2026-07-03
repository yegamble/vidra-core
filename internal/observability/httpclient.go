package observability

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// OTelHTTPTransport wraps base with the OpenTelemetry net/http RoundTripper,
// which (a) starts a client span per outbound request and (b) injects the active
// trace context into the outbound headers using the global propagator installed
// by SetupTracing — i.e. it stamps W3C `traceparent` onto federation / import /
// whisper / atproto calls so a trace continues across the network hop. It is
// meant to be handed to urlsafety.SetTransportWrapper so every SSRF-guarded
// client is instrumented at one seam (see cmd/api). base stays underneath, so
// the dial-time SSRF guard is unaffected.
//
// When OpenTelemetry is disabled we simply never install this wrapper (cmd/api
// gates the SetTransportWrapper call on OTEL_ENABLED), so outbound calls pay
// exactly zero cost and inject no headers.
func OTelHTTPTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}
