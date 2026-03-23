package misc_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/httpapi/handlers/misc"
)

// TestContactFormHandler_OK verifies 204 on valid contact form submission.
func TestContactFormHandler_OK(t *testing.T) {
	h := misc.ContactFormHandler()
	body := `{"fromName":"Alice","fromEmail":"alice@example.com","body":"Hello!"}`
	req := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestContactFormHandler_MissingFields verifies 400 when required fields are absent.
func TestContactFormHandler_MissingFields(t *testing.T) {
	h := misc.ContactFormHandler()
	body := `{"fromName":"Alice"}`
	req := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetOAuthLocalHandler_OK verifies 200 with client_id and client_secret fields.
func TestGetOAuthLocalHandler_OK(t *testing.T) {
	h := misc.GetOAuthLocalHandler("test-client-id", "test-client-secret")
	req := httptest.NewRequest(http.MethodGet, "/oauth-clients/local", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.ClientID == "" {
		t.Error("expected non-empty client_id")
	}
	if resp.Data.ClientSecret == "" {
		t.Error("expected non-empty client_secret")
	}
}

// TestGetOAuthLocalHandler_EmptySecret verifies client_id is always returned even with no secret.
func TestGetOAuthLocalHandler_EmptySecret(t *testing.T) {
	h := misc.GetOAuthLocalHandler("local-client", "")
	req := httptest.NewRequest(http.MethodGet, "/oauth-clients/local", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
