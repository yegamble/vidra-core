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
