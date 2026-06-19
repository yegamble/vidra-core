package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
)

func TestInstanceEndpoint(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "Vidra Test" {
		t.Errorf("name = %q, want Vidra Test", body.Name)
	}
	if body.Software.Name != "vidra" {
		t.Errorf("software.name = %q, want vidra", body.Software.Name)
	}
	if !body.RegistrationEnabled {
		t.Error("registration_enabled = false, want true (testConfig default)")
	}
}

func TestInstanceAboutMetadata(t *testing.T) {
	cfg := testConfig()
	cfg.InstanceDescription = "A friendly instance"
	cfg.InstanceTermsURL = "https://example.test/terms"
	cfg.InstancePrivacyURL = "https://example.test/privacy"
	cfg.InstanceContactEmail = "admin@example.test"
	srv := New(cfg, nil, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Description != "A friendly instance" || body.TermsURL != "https://example.test/terms" ||
		body.PrivacyURL != "https://example.test/privacy" || body.ContactEmail != "admin@example.test" {
		t.Errorf("unexpected about metadata: %+v", body)
	}
}

// registrationDisabledServer wires auth over the fake repo with registration off.
func registrationDisabledServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.RegistrationEnabled = false
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	return New(cfg, nil, nil, WithAuthService(svc, 15*time.Minute))
}

func TestInstanceReflectsRegistrationDisabled(t *testing.T) {
	srv := registrationDisabledServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	var body instanceResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.RegistrationEnabled {
		t.Error("registration_enabled = true, want false")
	}
}

func TestRegisterForbiddenWhenDisabled(t *testing.T) {
	srv := registrationDisabledServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "forbidden" {
		t.Errorf("code = %q, want forbidden", er.Error.Code)
	}
}
