package httpapi

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
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// mockUserRepo implementation
type mockUserRepo struct {
	users map[string]*domain.User
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: map[string]*domain.User{}}
}

func (m *mockUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	c := *user
	m.users[user.ID] = &c
	return nil
}

func (m *mockUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

func (m *mockUserRepo) Update(ctx context.Context, user *domain.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) Delete(ctx context.Context, id string) error {
	delete(m.users, id)
	return nil
}

func (m *mockUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	return "", nil
}

func (m *mockUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return nil
}

func (m *mockUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	return nil, nil
}

func (m *mockUserRepo) Count(ctx context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

func (m *mockUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return nil
}

func (m *mockUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return nil
}

func generateTestJWT(secret, userID, role string) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
		"jti": uuid.NewString(),
	}
	if role != "" {
		claims["role"] = role
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	sgn, _ := token.SignedString([]byte(secret))
	return sgn
}

func TestPrivilegeEscalation_CreateUser(t *testing.T) {
	// Setup
	jwtSecret := "test-secret-123"
	cfg := &config.Config{
		JWTSecret:         jwtSecret,
		RateLimitDuration: time.Minute,
		RateLimitRequests: 100,
	}

	repo := newMockUserRepo()
	deps := &shared.HandlerDependencies{
		UserRepo:         repo,
		JWTSecret:        jwtSecret,
		RedisPingTimeout: time.Second,
	}

	r := chi.NewRouter()
	rlManager := middleware.NewRateLimiterManager()
	RegisterRoutesWithDependencies(r, cfg, rlManager, deps)

	// Create a regular user token
	userID := uuid.NewString()
	token := generateTestJWT(jwtSecret, userID, "user")

	// Prepare request to create an admin user
	reqBody := map[string]interface{}{
		"username": "hacker",
		"email":    "hacker@example.com",
		"password": "password123",
		"role":     "admin",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()

	// Execute
	r.ServeHTTP(rr, req)

	// Assert - Expect 403 Forbidden (vulnerability fixed)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden (vulnerability fixed), but got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify user was NOT created
	_, err := repo.GetByUsername(context.Background(), "hacker")
	if err == nil {
		t.Fatal("Expected user NOT to be created, but it was found in repo")
	}
}
