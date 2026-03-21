package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// mockUserRepo is a simple in-memory implementation of usecase.UserRepository
type mockUserRepo struct {
	users map[string]*domain.User
}

// ensure mockUserRepo implements usecase.UserRepository
var _ usecase.UserRepository = (*mockUserRepo)(nil)

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: map[string]*domain.User{}}
}

func (m *mockUserRepo) Create(_ context.Context, user *domain.User, _ string) error {
	c := *user
	m.users[user.ID] = &c
	return nil
}

func (m *mockUserRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	if u, ok := m.users[id]; ok {
		// return a copy to avoid accidental external mutation
		c := *u
		// Deep copy the Avatar if present
		if u.Avatar != nil {
			avatarCopy := *u.Avatar
			c.Avatar = &avatarCopy
		}
		return &c, nil
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			c := *u
			// Deep copy the Avatar if present
			if u.Avatar != nil {
				avatarCopy := *u.Avatar
				c.Avatar = &avatarCopy
			}
			return &c, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			c := *u
			// Deep copy the Avatar if present
			if u.Avatar != nil {
				avatarCopy := *u.Avatar
				c.Avatar = &avatarCopy
			}
			return &c, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) Update(_ context.Context, user *domain.User) error {
	if _, ok := m.users[user.ID]; !ok {
		return domain.ErrUserNotFound
	}
	// store a copy
	c := *user
	m.users[user.ID] = &c
	return nil
}

func (m *mockUserRepo) Delete(_ context.Context, id string) error {
	delete(m.users, id)
	return nil
}

func (m *mockUserRepo) GetPasswordHash(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockUserRepo) UpdatePassword(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockUserRepo) List(_ context.Context, _, _ int) ([]*domain.User, error) {
	var out []*domain.User
	for _, u := range m.users {
		c := *u
		out = append(out, &c)
	}
	return out, nil
}

func (m *mockUserRepo) Count(_ context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

func (m *mockUserRepo) SetAvatarFields(_ context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	u, ok := m.users[userID]
	if !ok {
		return domain.ErrUserNotFound
	}
	// Create a new user with avatar data
	if u.Avatar == nil {
		// Use a deterministic UUID based on userID for test reproducibility
		avatarID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		u.Avatar = &domain.Avatar{
			ID: avatarID,
		}
	}
	u.Avatar.IPFSCID = ipfsCID
	u.Avatar.WebPIPFSCID = webpCID
	// Store back (u is already a pointer, so changes are in place)
	return nil
}

func (m *mockUserRepo) MarkEmailAsVerified(_ context.Context, userID string) error {
	u, ok := m.users[userID]
	if !ok {
		return domain.ErrUserNotFound
	}
	// Copy to avoid external mutation
	c := *u
	c.EmailVerified = true
	now := time.Now()
	c.EmailVerifiedAt = sql.NullTime{Time: now, Valid: true}
	m.users[userID] = &c
	return nil
}

func (m *mockUserRepo) Anonymize(_ context.Context, userID string) error {
	u, ok := m.users[userID]
	if !ok {
		return domain.ErrUserNotFound
	}
	c := *u
	c.IsActive = false
	c.Email = "deleted-" + userID + "@deleted.invalid"
	c.Username = "deleted-" + userID
	c.DisplayName = "Deleted User"
	c.Bio = ""
	c.BitcoinWallet = ""
	m.users[userID] = &c
	return nil
}

// Response decoding functions are now in test_helpers.go

func TestGetCurrentUserHandler_Success(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{
		ID:          "user123",
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().Add(-24 * time.Hour),
		UpdatedAt:   time.Now().Add(-1 * time.Hour),
	}
	repo.users[u.ID] = u

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	// inject user ID as middleware would
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))

	rr := httptest.NewRecorder()
	handler := GetCurrentUserHandler(repo)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	resp := decodeResponse(t, rr)
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got domain.User
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal user: %v", err)
	}
	if got.ID != u.ID || got.Username != u.Username {
		t.Fatalf("unexpected user in response: %+v", got)
	}
}

func TestGetCurrentUserHandler_Unauthorized(t *testing.T) {
	repo := newMockUserRepo()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	rr := httptest.NewRecorder()
	GetCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestGetCurrentUserHandler_NotFound(t *testing.T) {
	repo := newMockUserRepo()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "missing"))
	rr := httptest.NewRecorder()
	GetCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestUpdateCurrentUserHandler_Success(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{
		ID:          "user123",
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Before",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().Add(-48 * time.Hour),
		UpdatedAt:   time.Now().Add(-24 * time.Hour),
	}
	repo.users[u.ID] = u

	body := map[string]string{
		"display_name": "After",
		"bio":          "New bio",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))

	rr := httptest.NewRecorder()
	UpdateCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	resp := decodeResponse(t, rr)
	var got domain.User
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal user: %v", err)
	}

	if got.DisplayName != "After" || got.Bio != "New bio" {
		t.Fatalf("user not updated correctly: %+v", got)
	}

	// verify in repo
	if repo.users[u.ID].DisplayName != "After" {
		t.Fatalf("repo not updated")
	}
}

func TestGetUserHandler_Success(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "123e4567-e89b-12d3-a456-426614174000", Username: "public", Email: "p@e.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	r := chi.NewRouter()
	r.Get("/{id}", GetUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/123e4567-e89b-12d3-a456-426614174000", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	var got domain.User
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal user: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("unexpected user returned: %+v", got)
	}
}

func TestGetUserHandler_NotFound(t *testing.T) {
	repo := newMockUserRepo()
	r := chi.NewRouter()
	r.Get("/{id}", GetUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/123e4567-e89b-12d3-a456-426614174001", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// no extra helpers needed; we set middleware.UserIDKey directly in request context

// errorMockUserRepo is a mock that returns errors for testing error paths
type errorMockUserRepo struct {
	getByIDErr error
	updateErr  error
	user       *domain.User
}

func (m *errorMockUserRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	if m.user != nil {
		return m.user, nil
	}
	return &domain.User{ID: id, Username: "testuser"}, nil
}

func (m *errorMockUserRepo) Update(_ context.Context, user *domain.User) error {
	return m.updateErr
}

// Implement remaining interface methods (not used in tests)
func (m *errorMockUserRepo) Create(_ context.Context, _ *domain.User, _ string) error { return nil }
func (m *errorMockUserRepo) GetByEmail(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}
func (m *errorMockUserRepo) GetByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, nil
}
func (m *errorMockUserRepo) Delete(_ context.Context, _ string) error { return nil }
func (m *errorMockUserRepo) GetPasswordHash(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *errorMockUserRepo) UpdatePassword(_ context.Context, _, _ string) error { return nil }
func (m *errorMockUserRepo) List(_ context.Context, _, _ int) ([]*domain.User, error) {
	return nil, nil
}
func (m *errorMockUserRepo) Count(_ context.Context) (int64, error) { return 0, nil }
func (m *errorMockUserRepo) SetAvatarFields(_ context.Context, _ string, _ sql.NullString, _ sql.NullString) error {
	return nil
}
func (m *errorMockUserRepo) MarkEmailAsVerified(_ context.Context, _ string) error { return nil }
func (m *errorMockUserRepo) Anonymize(_ context.Context, _ string) error           { return nil }

// Additional UpdateCurrentUserHandler error path tests

func TestUpdateCurrentUserHandler_Unauthorized(t *testing.T) {
	mockRepo := newMockUserRepo()
	handler := UpdateCurrentUserHandler(mockRepo)

	reqBody := map[string]string{"display_name": "New Name"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	// No user ID in context
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestUpdateCurrentUserHandler_InvalidJSON(t *testing.T) {
	mockRepo := newMockUserRepo()
	handler := UpdateCurrentUserHandler(mockRepo)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader([]byte("invalid-json")))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateCurrentUserHandler_UserNotFoundOnGet(t *testing.T) {
	mockRepo := &errorMockUserRepo{getByIDErr: domain.ErrUserNotFound}
	handler := UpdateCurrentUserHandler(mockRepo)

	reqBody := map[string]string{"display_name": "New Name"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateCurrentUserHandler_ServiceErrorOnGet(t *testing.T) {
	mockRepo := &errorMockUserRepo{getByIDErr: sql.ErrConnDone}
	handler := UpdateCurrentUserHandler(mockRepo)

	reqBody := map[string]string{"display_name": "New Name"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestUpdateCurrentUserHandler_UserNotFoundOnUpdate(t *testing.T) {
	mockRepo := &errorMockUserRepo{updateErr: domain.ErrUserNotFound}
	handler := UpdateCurrentUserHandler(mockRepo)

	reqBody := map[string]string{"display_name": "New Name"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateCurrentUserHandler_ServiceErrorOnUpdate(t *testing.T) {
	mockRepo := &errorMockUserRepo{updateErr: sql.ErrConnDone}
	handler := UpdateCurrentUserHandler(mockRepo)

	reqBody := map[string]string{"display_name": "New Name"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
