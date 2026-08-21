package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecureHeadersPresent(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	want := map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "DENY",
		"Referrer-Policy":                   "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy":        "same-origin",
		"X-Permitted-Cross-Domain-Policies": "none",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	// HSTS is production-only, so it must be absent in the test environment.
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should be absent outside production, got %q", got)
	}
}

func TestSecureHeadersHSTSInProduction(t *testing.T) {
	cfg := testConfig()
	cfg.Environment = "production"
	srv := New(cfg, nil, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS should be set in production")
	}
	// The base headers still apply in production.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// HSTS follows the ORIGIN's scheme, not VIDRA_ENV. The two cases below are the
// pair that used to be impossible to express: a development/staging instance
// genuinely behind TLS got no HSTS, and a deliberate plain-http deployment
// (VIDRA_ALLOW_PLAIN_HTTP, VIDRA_TLS_MODE=plain-http) got one it cannot honour.
//
// The plain-http half is the one with teeth. A browser ignores HSTS received
// over http (RFC 6797 §8.1), so emitting it is not immediately fatal — but the
// same hostname put behind a certificate later, or reached through a proxy that
// re-terminates, is a visitor pinned to a scheme the instance does not serve.
func TestSecureHeadersHSTSFollowsTheOriginScheme(t *testing.T) {
	for _, tc := range []struct {
		name           string
		env            string
		origin         string
		allowPlainHTTP bool
		want           bool
	}{
		{name: "https origin outside production", env: "development", origin: "https://videos.example", want: true},
		{name: "https origin in production", env: "production", origin: "https://videos.example", want: true},
		{name: "consented plain-http origin in production", env: "production", origin: "http://videos.internal", allowPlainHTTP: true, want: false},
		{name: "plain-http localhost in development", env: "development", origin: "http://localhost:3000", want: false},
		// Fail-secure: production with nothing to go on still pins TLS.
		{name: "production with no origin at all", env: "production", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Environment = tc.env
			cfg.PublicBaseURL = tc.origin
			cfg.AllowPlainHTTP = tc.allowPlainHTTP
			srv := New(cfg, nil, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

			got := rec.Header().Get("Strict-Transport-Security")
			if tc.want && got == "" {
				t.Errorf("HSTS is absent, want it set for %s", tc.origin)
			}
			if !tc.want && got != "" {
				t.Errorf("HSTS = %q, want it absent on a plain-http origin", got)
			}
			// The base headers are unconditional either way.
			if h := rec.Header().Get("X-Content-Type-Options"); h != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", h)
			}
		})
	}
}
