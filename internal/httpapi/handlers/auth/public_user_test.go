package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
)

func TestGetPublicUserHandler_Success(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{
		ID:            "123e4567-e89b-12d3-a456-426614174000",
		Username:      "alice",
		Email:         "alice@example.com",
		DisplayName:   "Alice",
		Bio:           "Hello world",
		BitcoinWallet: "bc1qsecret",
		Role:          domain.RoleUser,
		IsActive:      true,
		TwoFAEnabled:  true,
		CreatedAt:     time.Now().Add(-24 * time.Hour),
		UpdatedAt:     time.Now(),
	}
	repo.users[u.ID] = u

	r := chi.NewRouter()
	r.Get("/{id}", GetPublicUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/123e4567-e89b-12d3-a456-426614174000", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeResponse(t, rr)
	if !resp.Success {
		t.Fatalf("expected success=true")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal response data: %v", err)
	}

	// Assert public fields present
	if got["id"] != u.ID {
		t.Errorf("expected id=%q, got %q", u.ID, got["id"])
	}
	if got["username"] != u.Username {
		t.Errorf("expected username=%q, got %q", u.Username, got["username"])
	}

	// Assert sensitive fields absent
	for _, sensitive := range []string{"email", "bitcoin_wallet", "is_active", "twofa_enabled", "email_verified"} {
		if _, ok := got[sensitive]; ok {
			t.Errorf("sensitive field %q must not appear in public profile response", sensitive)
		}
	}
}

func TestGetPublicUserHandler_NotFound(t *testing.T) {
	repo := newMockUserRepo()
	r := chi.NewRouter()
	r.Get("/{id}", GetPublicUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/123e4567-e89b-12d3-a456-426614174001", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetPublicUserHandler_InvalidID(t *testing.T) {
	repo := newMockUserRepo()
	r := chi.NewRouter()
	r.Get("/{id}", GetPublicUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
