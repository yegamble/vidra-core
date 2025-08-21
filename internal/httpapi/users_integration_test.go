package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
)

// decode helper for API response wrapper
type integResp struct {
	Data    json.RawMessage `json:"data"`
	Error   *ErrorInfo      `json:"error"`
	Success bool            `json:"success"`
	Meta    *Meta           `json:"meta"`
}

func decodeInteg(rr *httptest.ResponseRecorder, t *testing.T) integResp {
	t.Helper()
	var resp integResp
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestRegister_Integration_CreatesUserInDB(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens", "sessions")

	repo := repository.NewUserRepository(td.DB)
	s := NewServer(repo, repository.NewAuthRepository(td.DB), "test-secret", nil, 0, "", "", 0, nil)

	// unique values
	uname := "reg_" + time.Now().Format("20060102150405")
	email := uname + "@example.com"

	body := map[string]any{
		"username":     uname,
		"email":        email,
		"password":     "integration-password-12345",
		"display_name": "Reg Integ",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.Register(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// Verify response and DB
	resp := decodeInteg(rr, t)
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	// DB has created user
	got, err := repo.GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("expected user in DB: %v", err)
	}
	if got.Username != uname || got.Email != email {
		t.Fatalf("unexpected stored user: %+v", got)
	}
}

func TestCreateUserHandler_Integration_CreatesUserInDB(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)
	h := CreateUserHandler(repo)

	uname := "api_" + time.Now().Format("20060102150405")
	email := uname + "@example.com"

	body := map[string]any{
		"username":     uname,
		"email":        email,
		"password":     "integration-password-abc",
		"display_name": "API Integ",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// Confirm persisted
	got, err := repo.GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("expected user in DB: %v", err)
	}
	if got.Username != uname {
		t.Fatalf("unexpected stored user: %+v", got)
	}
}

func TestGetUserHandler_Integration_GetsFromDB(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)

	// Seed a user directly via repo
	u := &domain.User{
		ID:          uuid.NewString(),
		Username:    "get_integ",
		Email:       "get_integ@example.com",
		DisplayName: "Getter",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(context.Background(), u, "hash"); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/{id}", GetUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/"+u.ID, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	resp := decodeInteg(rr, t)
	var got domain.User
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("unmarshal user: %v", err)
	}
	if got.ID != u.ID || got.Username != u.Username {
		t.Fatalf("unexpected user: %+v", got)
	}
}

func TestGetCurrentUserHandler_Integration_ReturnsDBUser(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)

	u := &domain.User{
		ID:          uuid.NewString(),
		Username:    "me_integ",
		Email:       "me_integ@example.com",
		DisplayName: "Me",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(context.Background(), u, "hash"); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
	rr := httptest.NewRecorder()

	GetCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	resp := decodeInteg(rr, t)
	var got domain.User
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("unmarshal user: %v", err)
	}
	if got.ID != u.ID || got.Username != u.Username || got.Email != u.Email {
		t.Fatalf("unexpected user: %+v", got)
	}
}

func TestUpdateCurrentUserHandler_Integration_UpdatesDBUser(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)

	u := &domain.User{
		ID:          uuid.NewString(),
		Username:    "upd_integ",
		Email:       "upd_integ@example.com",
		DisplayName: "Before",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(context.Background(), u, "hash"); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	body := map[string]any{
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
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// Response asserts
	resp := decodeInteg(rr, t)
	var got domain.User
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("unmarshal user: %v", err)
	}
	if got.DisplayName != "After" || got.Bio != "New bio" {
		t.Fatalf("unexpected updated fields: %+v", got)
	}

	// Verify persisted in DB
	fromDB, err := repo.GetByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("expected user in DB: %v", err)
	}
	if fromDB.DisplayName != "After" || fromDB.Bio != "New bio" {
		t.Fatalf("DB not updated: %+v", fromDB)
	}
}

func TestGetCurrentUserHandler_Integration_Unauthorized(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	rr := httptest.NewRecorder()
	GetCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestGetCurrentUserHandler_Integration_NotFound(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	// Use a valid but non-existent UUID to ensure repository returns ErrUserNotFound
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
	rr := httptest.NewRecorder()
	GetCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateCurrentUserHandler_Integration_InvalidJSON(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)
	// Seed a user so lookup works; invalid JSON should be caught before update
	u := &domain.User{ID: uuid.NewString(), Username: "badjson", Email: "badjson@example.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repo.Create(context.Background(), u, "hash"); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	// Send invalid JSON (unterminated object)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewBufferString("{\"display_name\": \"x"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
	rr := httptest.NewRecorder()
	UpdateCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateCurrentUserHandler_Integration_Unauthorized(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	repo := repository.NewUserRepository(td.DB)

	body := map[string]any{"display_name": "After"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	UpdateCurrentUserHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d, body=%s", rr.Code, rr.Body.String())
	}
}
