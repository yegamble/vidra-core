package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

type mockUserRepo struct {
	users     map[string]*domain.User
	passwords map[string]string
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{
		users:     map[string]*domain.User{},
		passwords: map[string]string{},
	}
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
	if h, ok := m.passwords[userID]; ok {
		return h, nil
	}
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

	userID := uuid.NewString()
	token := generateTestJWT(jwtSecret, userID, "user")

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

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden (vulnerability fixed), but got %d. Body: %s", rr.Code, rr.Body.String())
	}

	_, err := repo.GetByUsername(context.Background(), "hacker")
	if err == nil {
		t.Fatal("Expected user NOT to be created, but it was found in repo")
	}
}

func TestLogin_BannedUserBlocked(t *testing.T) {
	const testSecret = "test-jwt-secret"
	const password = "password123"

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	repo := newMockUserRepo()
	userID := uuid.NewString()
	repo.users[userID] = &domain.User{
		ID:        userID,
		Username:  "banneduser",
		Email:     "banned@example.com",
		Role:      domain.RoleUser,
		IsActive:  false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	repo.passwords[userID] = string(hash)

	cfg := &config.Config{JWTSecret: testSecret}
	s := NewServer(repo, nil, testSecret, nil, time.Second, "", "", time.Second, cfg)

	body := `{"email":"banned@example.com","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.Login(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("banned user: expected 403, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestLogin_ActiveUserAllowed(t *testing.T) {
	const testSecret = "test-jwt-secret"
	const password = "password123"

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}

	repo := newMockUserRepo()
	userID := uuid.NewString()
	repo.users[userID] = &domain.User{
		ID:        userID,
		Username:  "activeuser",
		Email:     "active@example.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	repo.passwords[userID] = string(hash)

	cfg := &config.Config{JWTSecret: testSecret}
	s := NewServer(repo, nil, testSecret, nil, time.Second, "", "", time.Second, cfg)

	body := `{"email":"active@example.com","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("active user: expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}
}
