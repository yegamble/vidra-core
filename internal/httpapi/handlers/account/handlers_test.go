package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/usecase"
)

// testResponse mirrors shared.Response for test decoding
type testResponse struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) testResponse {
	t.Helper()
	var resp testResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

// mockUserRepo is a minimal in-memory UserRepository for unit tests
type mockUserRepo struct {
	users map[string]*domain.User
}

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

func (m *mockUserRepo) SetAvatarFields(_ context.Context, _ string, _ sql.NullString, _ sql.NullString) error {
	return nil
}

func (m *mockUserRepo) MarkEmailAsVerified(_ context.Context, _ string) error {
	return nil
}

func (m *mockUserRepo) Anonymize(_ context.Context, _ string) error {
	return nil
}

// seedUser adds a user to the mock repo and returns it
func seedUser(repo *mockUserRepo, username, id string) *domain.User {
	u := &domain.User{
		ID:          id,
		Username:    username,
		Email:       username + "@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().Add(-24 * time.Hour),
		UpdatedAt:   time.Now(),
	}
	repo.users[u.ID] = u
	return u
}

func newRouter(h *AccountHandlers) chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.ListAccounts)
	r.Get("/{name}", h.GetAccount)
	r.Get("/{name}/videos", h.GetAccountVideos)
	r.Get("/{name}/video-channels", h.GetAccountVideoChannels)
	r.Get("/{name}/ratings", h.GetAccountRatings)
	r.Get("/{name}/followers", h.GetAccountFollowers)
	return r
}

// TestListAccounts_ReturnsAllAccounts verifies GET /accounts returns a paginated list.
func TestListAccounts_ReturnsAllAccounts(t *testing.T) {
	repo := newMockUserRepo()
	seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174001")
	seedUser(repo, "bob", "123e4567-e89b-12d3-a456-426614174002")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
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
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	total, ok := got["total"]
	if !ok {
		t.Fatal("expected 'total' field in response")
	}
	if total.(float64) < 2 {
		t.Errorf("expected total >= 2, got %v", total)
	}
}

// TestListAccounts_EmptyRepo verifies GET /accounts returns total=0 for an empty user store.
func TestListAccounts_EmptyRepo(t *testing.T) {
	repo := newMockUserRepo()
	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	resp := decodeResponse(t, rr)
	var got map[string]interface{}
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if got["total"].(float64) != 0 {
		t.Errorf("expected total=0, got %v", got["total"])
	}
}

// TestGetAccount_BySimpleUsername verifies that GET /accounts/{name} resolves a plain username.
func TestGetAccount_BySimpleUsername(t *testing.T) {
	repo := newMockUserRepo()
	u := seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174000")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/alice", nil)
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
		t.Fatalf("failed to unmarshal data: %v", err)
	}

	if got["username"] != u.Username {
		t.Errorf("expected username=%q, got %v", u.Username, got["username"])
	}
	// Sensitive fields must be absent
	for _, sensitive := range []string{"email", "bitcoin_wallet", "is_active"} {
		if _, ok := got[sensitive]; ok {
			t.Errorf("sensitive field %q must not appear in account response", sensitive)
		}
	}
}

// TestGetAccount_AtHandle verifies that @alice@example.com resolves to "alice".
func TestGetAccount_AtHandle(t *testing.T) {
	repo := newMockUserRepo()
	seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174000")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/@alice@example.com", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for @alice@example.com, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetAccount_NotFound verifies that a missing username returns 404.
func TestGetAccount_NotFound(t *testing.T) {
	repo := newMockUserRepo()
	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/nosuchuser", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// TestGetAccountVideos_ReturnsEmptyList verifies the videos endpoint exists and returns a list.
func TestGetAccountVideos_ReturnsEmptyList(t *testing.T) {
	repo := newMockUserRepo()
	seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174000")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/alice/videos", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetAccountVideoChannels_ReturnsEmptyList verifies the channels endpoint exists.
func TestGetAccountVideoChannels_ReturnsEmptyList(t *testing.T) {
	repo := newMockUserRepo()
	seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174000")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/alice/video-channels", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetAccountRatings_ReturnsEmptyList verifies the ratings endpoint exists.
func TestGetAccountRatings_ReturnsEmptyList(t *testing.T) {
	repo := newMockUserRepo()
	seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174000")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/alice/ratings", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestGetAccountFollowers_ReturnsEmptyList verifies the followers endpoint exists.
func TestGetAccountFollowers_ReturnsEmptyList(t *testing.T) {
	repo := newMockUserRepo()
	seedUser(repo, "alice", "123e4567-e89b-12d3-a456-426614174000")

	h := NewAccountHandlers(repo, nil, nil, nil)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/alice/followers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
