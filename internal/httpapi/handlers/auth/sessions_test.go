package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/middleware"
	"athena/internal/port"
)

func withRoleContext(req *http.Request, role string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), middleware.UserRoleKey, role))
}

type mockTokenSessionRepo struct {
	tokens    []*port.TokenSession
	revokeErr error
	revokedID string
}

var _ port.TokenSessionRepository = (*mockTokenSessionRepo)(nil)

func (m *mockTokenSessionRepo) ListUserTokenSessions(_ context.Context, userID string) ([]*port.TokenSession, error) {
	var result []*port.TokenSession
	for _, t := range m.tokens {
		if t.UserID == userID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockTokenSessionRepo) RevokeTokenSession(_ context.Context, tokenSessionID string) error {
	m.revokedID = tokenSessionID
	return m.revokeErr
}

func TestListTokenSessions_OK(t *testing.T) {
	repo := &mockTokenSessionRepo{
		tokens: []*port.TokenSession{
			{ID: "tok-1", UserID: "user-abc", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}
	h := NewTokenSessionHandlers(repo)

	r := chi.NewRouter()
	r.Get("/users/{id}/token-sessions", h.ListTokenSessions)

	req := httptest.NewRequest(http.MethodGet, "/users/user-abc/token-sessions", nil)
	req = withUserContext(req, "user-abc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListTokenSessions_Unauthorized(t *testing.T) {
	repo := &mockTokenSessionRepo{}
	h := NewTokenSessionHandlers(repo)

	r := chi.NewRouter()
	r.Get("/users/{id}/token-sessions", h.ListTokenSessions)

	req := httptest.NewRequest(http.MethodGet, "/users/user-abc/token-sessions", nil)
	// No user context — unauthenticated
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListTokenSessions_Forbidden(t *testing.T) {
	repo := &mockTokenSessionRepo{}
	h := NewTokenSessionHandlers(repo)

	r := chi.NewRouter()
	r.Get("/users/{id}/token-sessions", h.ListTokenSessions)

	// caller "other-user" tries to access "user-abc"'s sessions
	req := httptest.NewRequest(http.MethodGet, "/users/user-abc/token-sessions", nil)
	req = withUserContext(req, "other-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListTokenSessions_AdminCanAccessOtherUser(t *testing.T) {
	repo := &mockTokenSessionRepo{
		tokens: []*port.TokenSession{
			{ID: "tok-1", UserID: "user-abc", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour)},
		},
	}
	h := NewTokenSessionHandlers(repo)

	r := chi.NewRouter()
	r.Get("/users/{id}/token-sessions", h.ListTokenSessions)

	req := httptest.NewRequest(http.MethodGet, "/users/user-abc/token-sessions", nil)
	req = withUserContext(req, "admin-user")
	req = withRoleContext(req, "admin")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevokeTokenSession_Forbidden(t *testing.T) {
	repo := &mockTokenSessionRepo{}
	h := NewTokenSessionHandlers(repo)

	r := chi.NewRouter()
	r.Post("/users/{id}/token-sessions/{tokenSessionId}/revoke", h.RevokeTokenSession)

	req := httptest.NewRequest(http.MethodPost, "/users/user-abc/token-sessions/tok-1/revoke", nil)
	req = withUserContext(req, "other-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevokeTokenSession_OK(t *testing.T) {
	repo := &mockTokenSessionRepo{}
	h := NewTokenSessionHandlers(repo)

	r := chi.NewRouter()
	r.Post("/users/{id}/token-sessions/{tokenSessionId}/revoke", h.RevokeTokenSession)

	req := httptest.NewRequest(http.MethodPost, "/users/user-abc/token-sessions/tok-1/revoke", nil)
	req = withUserContext(req, "user-abc")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.revokedID != "tok-1" {
		t.Fatalf("expected revokedID=tok-1, got %q", repo.revokedID)
	}
}
