package httpapi

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/go-chi/chi/v5"

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
        return &c, nil
    }
    return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
    for _, u := range m.users {
        if u.Email == email {
            c := *u
            return &c, nil
        }
    }
    return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) GetByUsername(_ context.Context, username string) (*domain.User, error) {
    for _, u := range m.users {
        if u.Username == username {
            c := *u
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

// Response decoding helpers
type testResponse struct {
    Data    json.RawMessage `json:"data"`
    Error   *ErrorInfo      `json:"error"`
    Success bool            `json:"success"`
    Meta    *Meta           `json:"meta"`
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) testResponse {
    t.Helper()
    var resp testResponse
    if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
        t.Fatalf("failed to decode response: %v", err)
    }
    return resp
}

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
        "avatar":       "https://example.com/a.png",
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

    if got.DisplayName != "After" || got.Bio != "New bio" || got.Avatar != "https://example.com/a.png" {
        t.Fatalf("user not updated correctly: %+v", got)
    }

    // verify in repo
    if repo.users[u.ID].DisplayName != "After" {
        t.Fatalf("repo not updated")
    }
}

func TestGetUserHandler_Success(t *testing.T) {
    repo := newMockUserRepo()
    u := &domain.User{ID: "abc123", Username: "public", Email: "p@e.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
    repo.users[u.ID] = u

    r := chi.NewRouter()
    r.Get("/{id}", GetUserHandler(repo))

    req := httptest.NewRequest(http.MethodGet, "/abc123", nil)
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

    req := httptest.NewRequest(http.MethodGet, "/nope", nil)
    rr := httptest.NewRecorder()
    r.ServeHTTP(rr, req)

    if rr.Code != http.StatusNotFound {
        t.Fatalf("expected 404, got %d", rr.Code)
    }
}

// no extra helpers needed; we set middleware.UserIDKey directly in request context
