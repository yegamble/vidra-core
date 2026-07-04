package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSystemStatus(t *testing.T) {
	srv := authServer(t)
	// The first registered account becomes admin.
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/admin/system", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("system status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body systemStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Software.Name != "vidra" || body.Software.GoVersion == "" {
		t.Errorf("software = %+v, want vidra with a go_version", body.Software)
	}
	if body.Environment != "test" {
		t.Errorf("environment = %q, want test", body.Environment)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok (nil deps report not_configured, not down)", body.Status)
	}
	// The component map is always present (postgres/redis "not_configured" here,
	// since authServer wires no db/redis).
	if c, ok := body.Components["postgres"]; !ok || c.Status != "not_configured" {
		t.Errorf("postgres component = %+v (present=%v), want not_configured", c, ok)
	}
	if _, ok := body.Components["redis"]; !ok {
		t.Error("redis component missing")
	}

	// The effective (non-secret) rate-limit config is surfaced read-only so an
	// operator can confirm what is in force (product-decisions §3 — config-only,
	// no runtime mutation endpoint). testConfig mirrors the production defaults.
	rl := body.RateLimits
	if !rl.Enabled || rl.Requests != 120 || rl.AuthRequests != 10 || rl.WindowSeconds != 60 {
		t.Errorf("rate_limits = %+v, want {Enabled:true Requests:120 AuthRequests:10 WindowSeconds:60}", rl)
	}

	// A regular user cannot read it; anon is unauthorized.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/admin/system", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin system status = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/system", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon system status = %d, want 401", rec.Code)
	}
}
