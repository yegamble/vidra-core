package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"

	chi "github.com/go-chi/chi/v5"
)

// mockAdminUserRepo satisfies usecase.UserRepository for admin handler tests.
type mockAdminUserRepo struct {
	users []*domain.User
	err   error
}

func (m *mockAdminUserRepo) Create(_ context.Context, _ *domain.User, _ string) error {
	return m.err
}

func (m *mockAdminUserRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, u := range m.users {
		if u.ID == id {
			c := *u
			return &c, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockAdminUserRepo) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return nil, domain.ErrUserNotFound
}

func (m *mockAdminUserRepo) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, domain.ErrUserNotFound
}

func (m *mockAdminUserRepo) Update(_ context.Context, user *domain.User) error {
	if m.err != nil {
		return m.err
	}
	for i, u := range m.users {
		if u.ID == user.ID {
			c := *user
			m.users[i] = &c
			return nil
		}
	}
	return domain.ErrUserNotFound
}

func (m *mockAdminUserRepo) Delete(_ context.Context, _ string) error { return m.err }

func (m *mockAdminUserRepo) GetPasswordHash(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockAdminUserRepo) UpdatePassword(_ context.Context, _, _ string) error { return m.err }

func (m *mockAdminUserRepo) List(_ context.Context, limit, offset int) ([]*domain.User, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Simulate pagination
	if offset >= len(m.users) {
		return []*domain.User{}, nil
	}
	end := offset + limit
	if end > len(m.users) {
		end = len(m.users)
	}
	result := make([]*domain.User, end-offset)
	for i, u := range m.users[offset:end] {
		c := *u
		result[i] = &c
	}
	return result, nil
}

func (m *mockAdminUserRepo) Count(_ context.Context) (int64, error) {
	return int64(len(m.users)), m.err
}

func (m *mockAdminUserRepo) SetAvatarFields(_ context.Context, _ string, _ sql.NullString, _ sql.NullString) error {
	return nil
}

func (m *mockAdminUserRepo) MarkEmailAsVerified(_ context.Context, _ string) error { return nil }
func (m *mockAdminUserRepo) Anonymize(_ context.Context, _ string) error           { return nil }

var _ usecase.UserRepository = (*mockAdminUserRepo)(nil)

// decodeAdminResponse decodes the shared response envelope in admin tests.
func decodeAdminResponse(t *testing.T, rr *httptest.ResponseRecorder) struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
} {
	t.Helper()
	var resp struct {
		Data    json.RawMessage   `json:"data"`
		Error   *shared.ErrorInfo `json:"error"`
		Success bool              `json:"success"`
		Meta    *shared.Meta      `json:"meta"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func newTestUsers() []*domain.User {
	now := time.Now()
	return []*domain.User{
		{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Username: "alice", Email: "alice@example.com", Role: domain.RoleUser, IsActive: true, CreatedAt: now, UpdatedAt: now},
		{ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", Username: "bob", Email: "bob@example.com", Role: domain.RoleMod, IsActive: true, CreatedAt: now, UpdatedAt: now},
		{ID: "cccccccc-cccc-cccc-cccc-cccccccccccc", Username: "carol", Email: "carol@example.com", Role: domain.RoleAdmin, IsActive: true, CreatedAt: now, UpdatedAt: now},
	}
}

// withAdminContext injects a user ID as middleware would for admin routes.
func withAdminContext(req *http.Request, userID string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
}

// ---------------------------------------------------------------------------
// ListUsers tests
// ---------------------------------------------------------------------------

func TestAdminListUsers_Success(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.ListUsers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeAdminResponse(t, rr)
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	if resp.Meta == nil {
		t.Fatal("expected meta in response")
	}
	if resp.Meta.Total != 3 {
		t.Errorf("expected total=3, got %d", resp.Meta.Total)
	}
}

func TestAdminListUsers_Pagination(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/users?limit=2&offset=0", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.ListUsers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeAdminResponse(t, rr)
	if resp.Meta.Limit != 2 {
		t.Errorf("expected limit=2, got %d", resp.Meta.Limit)
	}
}

func TestAdminListUsers_Search(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/users?search=alice", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.ListUsers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeAdminResponse(t, rr)

	// Total should reflect filtered count (1 user matching "alice")
	if resp.Meta.Total != 1 {
		t.Errorf("expected total=1 for search=alice, got %d", resp.Meta.Total)
	}
}

func TestAdminListUsers_EmptyResult(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/users?search=zzznomatch", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.ListUsers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeAdminResponse(t, rr)
	if resp.Meta.Total != 0 {
		t.Errorf("expected total=0 for no-match search, got %d", resp.Meta.Total)
	}
}

// ---------------------------------------------------------------------------
// UpdateUser tests
// ---------------------------------------------------------------------------

func TestAdminUpdateUser_ChangeRole(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	body := `{"role":"moderator"}`
	req := httptest.NewRequest(http.MethodPut, "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify updated in repo
	updated, _ := repo.GetByID(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if updated.Role != domain.RoleMod {
		t.Errorf("expected role=moderator, got %s", updated.Role)
	}
}

func TestAdminUpdateUser_Ban(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	body := `{"is_active":false}`
	req := httptest.NewRequest(http.MethodPut, "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, _ := repo.GetByID(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if updated.IsActive {
		t.Error("expected user to be banned (IsActive=false)")
	}
}

func TestAdminUpdateUser_SelfDemotion(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	body := `{"role":"user"}`
	// Admin carol tries to change her own role
	req := httptest.NewRequest(http.MethodPut, "/cccccccc-cccc-cccc-cccc-cccccccccccc", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for self-demotion, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAdminUpdateUser_InvalidRole(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	body := `{"role":"superuser"}`
	req := httptest.NewRequest(http.MethodPut, "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid role, got %d", rr.Code)
	}
}

func TestAdminUpdateUser_NotFound(t *testing.T) {
	repo := &mockAdminUserRepo{users: newTestUsers()}
	h := NewAdminUserHandlers(repo)

	body := `{"role":"user"}`
	req := httptest.NewRequest(http.MethodPut, "/dddddddd-dddd-dddd-dddd-dddddddddddd", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestAdminUpdateUser_LastAdminProtection(t *testing.T) {
	// Only one admin (carol). Attempt to demote her from a different admin caller.
	now := time.Now()
	users := []*domain.User{
		{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Username: "alice", Email: "alice@example.com", Role: domain.RoleUser, IsActive: true, CreatedAt: now, UpdatedAt: now},
		{ID: "cccccccc-cccc-cccc-cccc-cccccccccccc", Username: "carol", Email: "carol@example.com", Role: domain.RoleAdmin, IsActive: true, CreatedAt: now, UpdatedAt: now},
	}
	repo := &mockAdminUserRepo{users: users}
	h := NewAdminUserHandlers(repo)

	body := `{"role":"user"}`
	// Different admin caller (not carol) tries to demote carol, the last admin.
	req := httptest.NewRequest(http.MethodPut, "/cccccccc-cccc-cccc-cccc-cccccccccccc", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "dddddddd-dddd-dddd-dddd-dddddddddddd")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for last-admin protection, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := decodeAdminResponse(t, rr)
	if resp.Error == nil || resp.Error.Code != "LAST_ADMIN_PROTECTION" {
		t.Errorf("expected error code LAST_ADMIN_PROTECTION, got %+v", resp.Error)
	}
}

func TestAdminUpdateUser_ListErrorDuringDemotion(t *testing.T) {
	// If List fails during last-admin check, return 500 instead of proceeding.
	now := time.Now()
	adminUser := &domain.User{ID: "cccccccc-cccc-cccc-cccc-cccccccccccc", Username: "carol", Email: "carol@example.com", Role: domain.RoleAdmin, IsActive: true, CreatedAt: now, UpdatedAt: now}
	repo := &mockAdminUserRepoListErr{user: adminUser}
	h := NewAdminUserHandlers(repo)

	body := `{"role":"user"}`
	req := httptest.NewRequest(http.MethodPut, "/cccccccc-cccc-cccc-cccc-cccccccccccc", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "dddddddd-dddd-dddd-dddd-dddddddddddd")

	r := chi.NewRouter()
	r.Put("/{id}", h.UpdateUser)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when List fails during demotion check, got %d: %s", rr.Code, rr.Body.String())
	}
}

// mockAdminUserRepoListErr returns a specific user from GetByID but errors on List.
type mockAdminUserRepoListErr struct {
	mockAdminUserRepo
	user *domain.User
}

func (m *mockAdminUserRepoListErr) GetByID(_ context.Context, id string) (*domain.User, error) {
	if m.user != nil && m.user.ID == id {
		c := *m.user
		return &c, nil
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockAdminUserRepoListErr) List(_ context.Context, _, _ int) ([]*domain.User, error) {
	return nil, errors.New("database error")
}

// jsonBody returns a strings.Reader over json string.
func jsonBody(s string) *strings.Reader {
	return strings.NewReader(s)
}
