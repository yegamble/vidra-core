package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"database/sql"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	chi "github.com/go-chi/chi/v5"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockUserRepository to replace real DB
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}
func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *MockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *MockUserRepository) Update(ctx context.Context, user *domain.User) error {
	return m.Called(ctx, user).Error(0)
}
func (m *MockUserRepository) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *MockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}
func (m *MockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return m.Called(ctx, userID, passwordHash).Error(0)
}
func (m *MockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	return args.Get(0).([]*domain.User), args.Error(1)
}
func (m *MockUserRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return m.Called(ctx, userID, ipfsCID, webpCID).Error(0)
}
func (m *MockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return m.Called(ctx, userID).Error(0)
}

func TestPrivilegeEscalation_CreateUser(t *testing.T) {
	// Create mock dependencies
	userRepo := new(MockUserRepository)

	// Expectations for the VULNERABLE state (handler is reached)
	// We expect GetByEmail and GetByUsername to be called to check for duplicates
	userRepo.On("GetByEmail", mock.Anything, "hacked@example.com").Return(nil, domain.ErrUserNotFound).Maybe()
	userRepo.On("GetByUsername", mock.Anything, "hacked_admin").Return(nil, domain.ErrUserNotFound).Maybe()

	// Expect Create to be called. We capture the user passed to it to verify role.
	var capturedUser *domain.User
	userRepo.On("Create", mock.Anything, mock.MatchedBy(func(u *domain.User) bool {
		capturedUser = u
		return true
	}), mock.Anything).Return(nil).Maybe()

	jwtSecret := "test-jwt-secret"

	deps := &shared.HandlerDependencies{
		UserRepo:  userRepo,
		JWTSecret: jwtSecret,
	}

	// Setup Router
	r := chi.NewRouter()
	cfg := &config.Config{
		JWTSecret: jwtSecret,
		LogLevel:  "error",
		RateLimitDuration: 1 * time.Minute,
		RateLimitRequests: 1000,
	}
	rlManager := middleware.NewRateLimiterManager()

	RegisterRoutesWithDependencies(r, cfg, rlManager, deps)

	// Create a regular user token
	attackerID := uuid.NewString()
	token := generateTestJWT(t, jwtSecret, attackerID, string(domain.RoleUser))

	// Payload to create a new ADMIN user
	payload := map[string]interface{}{
		"username":     "hacked_admin",
		"email":        "hacked@example.com",
		"password":     "password123",
		"display_name": "Hacked Admin",
		"role":         "admin",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/api/v1/users/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Assertions
	// We expect the request to be forbidden (403) because the user is not an admin
	assert.Equal(t, http.StatusForbidden, w.Code, "Expected 403 Forbidden for non-admin user creating user")

	// Ensure Create was NOT called
	assert.Nil(t, capturedUser, "Create should not have been called")
}

func generateTestJWT(t *testing.T, secret, userID, role string) string {
	claims := jwt.MapClaims{
		"sub": userID,
		"role": role,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}
