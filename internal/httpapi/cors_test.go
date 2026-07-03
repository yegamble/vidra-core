package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These lock in the CORS contract: the browser cross-origin policy is exactly the
// configured allow-list (testConfig allows http://localhost:3000). A regression
// that widened or dropped CORS would flip a green check here.

func TestCORSEchoesAllowedOrigin(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the configured origin echoed", got)
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("Origin", "http://evil.example")
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no CORS grant for a disallowed origin", got)
	}
}

func TestCORSPreflightForAllowedOrigin(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/channels", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("preflight Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
	if methods := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, http.MethodPost) {
		t.Errorf("preflight Access-Control-Allow-Methods = %q, want it to include POST", methods)
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
}

// Cookie-mode auth needs credentialed CORS: allow-listed origins get
// Access-Control-Allow-Credentials so the browser attaches the vidra_refresh
// cookie, but the grant must never be paired with a wildcard origin.

func TestCORSAllowsCredentialsForAllowedOrigin(t *testing.T) {
	srv := New(testConfig(), nil, nil)

	// Simple request.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\" for an allow-listed origin", got)
	}

	// Preflight.
	pre := httptest.NewRecorder()
	preReq := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/refresh", nil)
	preReq.Header.Set("Origin", "http://localhost:3000")
	preReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	srv.Handler().ServeHTTP(pre, preReq)
	if got := pre.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("preflight Access-Control-Allow-Credentials = %q, want \"true\"", got)
	}
}

func TestCORSNoCredentialsForUnknownOrigin(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("Origin", "http://evil.example")
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q for a disallowed origin, want none", got)
	}
}

func TestCORSWildcardNeverGrantsCredentials(t *testing.T) {
	cfg := testConfig()
	cfg.CORSAllowedOrigins = []string{"*"}
	srv := New(cfg, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("Origin", "http://anywhere.example")
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q with a wildcard allow-list, want none", got)
	}
}
