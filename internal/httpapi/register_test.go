package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
)

func TestRegister_Success(t *testing.T) {
    s := NewServer(newMockUserRepo(), nil, "test-secret", nil, 0, "", "", 0)

	body := map[string]any{
		"username":     "reguser",
		"email":        "reg@example.com",
		"password":     "very-strong-password",
		"display_name": "Reg User",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.Register(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Data, &payload); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	// payload is AuthResponse; user is nested object
	userObj := payload["user"].(map[string]any)
	if userObj["username"].(string) != "reguser" || userObj["email"].(string) != "reg@example.com" {
		t.Fatalf("unexpected user fields: %+v", userObj)
	}
	if payload["access_token"].(string) == "" || payload["refresh_token"].(string) == "" {
		t.Fatalf("tokens missing in response")
	}
}

func TestRegister_MissingFields(t *testing.T) {
    s := NewServer(newMockUserRepo(), nil, "test-secret", nil, 0, "", "", 0)
	body := map[string]any{"username": "u", "email": "e@example.com"} // no password
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Register(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestRegister_Duplicate(t *testing.T) {
	repo := newMockUserRepo()
	// seed existing by email and username
	// existing email
	repo.users["by-email"] = &domain.User{ID: "by-email", Email: "dup@example.com", Username: "first"}
	// existing username (different email)
	repo.users["by-username"] = &domain.User{ID: "by-username", Email: "x@example.com", Username: "dupname"}

    s := NewServer(repo, nil, "test-secret", nil, 0, "", "", 0)

	// duplicate email
	b1, _ := json.Marshal(map[string]any{"username": "newname", "email": "dup@example.com", "password": "xpassword"})
	req1 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b1))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	s.Register(rr1, req1)
	if rr1.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate email, got %d", rr1.Code)
	}

	// duplicate username
	b2, _ := json.Marshal(map[string]any{"username": "dupname", "email": "new@example.com", "password": "xpassword"})
	req2 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	s.Register(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate username, got %d", rr2.Code)
	}
}
