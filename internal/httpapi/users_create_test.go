package httpapi

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestCreateUserHandler_Success(t *testing.T) {
	repo := newMockUserRepo()
	handler := CreateUserHandler(repo)

	body := map[string]any{
		"username":     "alice",
		"email":        "alice@example.com",
		"password":     "password-12345",
		"display_name": "Alice",
		"avatar":       "",
		"bio":          "hello",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}

	resp := decodeResponse(t, rr)
	var got map[string]any
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal user: %v", err)
	}

	if got["username"].(string) != "alice" || got["email"].(string) != "alice@example.com" {
		t.Fatalf("unexpected user fields: %+v", got)
	}
}

func TestCreateUserHandler_MissingFields(t *testing.T) {
	repo := newMockUserRepo()
	handler := CreateUserHandler(repo)

	body := map[string]any{ // missing password
		"username": "bob",
		"email":    "bob@example.com",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestCreateUserHandler_Conflicts(t *testing.T) {
    repo := newMockUserRepo()
    handler := CreateUserHandler(repo)

    // Seed an initial user through the handler
    seedBody := map[string]any{
        "username":     "seed",
        "email":        "seed@example.com",
        "password":     "password-12345",
        "display_name": "Seed User",
    }
    sb, _ := json.Marshal(seedBody)
    sreq := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(sb))
    sreq.Header.Set("Content-Type", "application/json")
    srr := httptest.NewRecorder()
    handler.ServeHTTP(srr, sreq)
    if srr.Code != http.StatusCreated {
        t.Fatalf("seeding failed, expected 201 got %d", srr.Code)
    }

    // duplicate email (new username, same email)
    body := map[string]any{
        "username": "another",
        "email":    "seed@example.com",
        "password": "password-12345",
    }
    b, _ := json.Marshal(body)
    req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()
    handler.ServeHTTP(rr, req)
    if rr.Code != http.StatusConflict {
        t.Fatalf("expected 409 for duplicate email, got %d", rr.Code)
    }

    // duplicate username (same username, different email)
    body2 := map[string]any{
        "username": "seed",
        "email":    "new@example.com",
        "password": "password-12345",
    }
    b2, _ := json.Marshal(body2)
    req2 := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(b2))
    req2.Header.Set("Content-Type", "application/json")
    rr2 := httptest.NewRecorder()
    handler.ServeHTTP(rr2, req2)
    if rr2.Code != http.StatusConflict {
        t.Fatalf("expected 409 for duplicate username, got %d", rr2.Code)
    }
}
