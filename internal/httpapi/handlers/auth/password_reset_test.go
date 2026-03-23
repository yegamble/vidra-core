package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"vidra-core/internal/domain"
)

// MockPasswordResetRepository mocks the PasswordResetRepository interface.
type MockPasswordResetRepository struct {
	mock.Mock
}

func (m *MockPasswordResetRepository) CreateToken(ctx context.Context, token *domain.PasswordResetToken) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockPasswordResetRepository) GetByTokenHash(ctx context.Context, hash string) (*domain.PasswordResetToken, error) {
	args := m.Called(ctx, hash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.PasswordResetToken), args.Error(1)
}

func (m *MockPasswordResetRepository) MarkUsed(ctx context.Context, tokenID string) error {
	args := m.Called(ctx, tokenID)
	return args.Error(0)
}

func (m *MockPasswordResetRepository) DeleteExpiredTokens(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// MockUserRepoForReset mocks only the user repo methods needed for password reset.
type MockUserRepoForReset struct {
	mock.Mock
}

func (m *MockUserRepoForReset) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepoForReset) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepoForReset) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

// MockEmailSvcForReset mocks only the email method needed for password reset.
type MockEmailSvcForReset struct {
	mock.Mock
}

func (m *MockEmailSvcForReset) SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error {
	args := m.Called(ctx, toEmail, username, token)
	return args.Error(0)
}

func newTestPasswordResetHandler() (*PasswordResetHandlers, *MockPasswordResetRepository, *MockUserRepoForReset, *MockEmailSvcForReset) {
	resetRepo := &MockPasswordResetRepository{}
	userRepo := &MockUserRepoForReset{}
	emailSvc := &MockEmailSvcForReset{}
	h := NewPasswordResetHandlers(resetRepo, userRepo, emailSvc)
	return h, resetRepo, userRepo, emailSvc
}

func TestPasswordResetRequest_ValidEmail(t *testing.T) {
	h, resetRepo, userRepo, emailSvc := newTestPasswordResetHandler()

	user := &domain.User{
		ID:       "user-123",
		Email:    "test@example.com",
		Username: "testuser",
	}

	userRepo.On("GetByEmail", mock.Anything, "test@example.com").Return(user, nil)
	resetRepo.On("CreateToken", mock.Anything, mock.AnythingOfType("*domain.PasswordResetToken")).Return(nil)
	emailSvc.On("SendPasswordResetEmail", mock.Anything, "test@example.com", "testuser", mock.AnythingOfType("string")).Return(nil)

	body := `{"email": "test@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/ask-reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.AskResetPassword(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	userRepo.AssertExpectations(t)
	resetRepo.AssertExpectations(t)
	emailSvc.AssertExpectations(t)
}

func TestPasswordResetRequest_UnknownEmail(t *testing.T) {
	h, _, userRepo, _ := newTestPasswordResetHandler()

	userRepo.On("GetByEmail", mock.Anything, "unknown@example.com").Return(nil, domain.ErrUserNotFound)

	body := `{"email": "unknown@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/ask-reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.AskResetPassword(w, req)

	// Should return 204 regardless (don't leak user existence)
	assert.Equal(t, http.StatusNoContent, w.Code)
	userRepo.AssertExpectations(t)
}

func TestPasswordResetRequest_MissingEmail(t *testing.T) {
	h, _, _, _ := newTestPasswordResetHandler()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/ask-reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.AskResetPassword(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPasswordResetConfirm_ValidToken(t *testing.T) {
	h, resetRepo, userRepo, _ := newTestPasswordResetHandler()

	resetToken := &domain.PasswordResetToken{
		ID:        "token-123",
		UserID:    "user-456",
		TokenHash: "somehash",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	resetRepo.On("GetByTokenHash", mock.Anything, mock.AnythingOfType("string")).Return(resetToken, nil)
	resetRepo.On("MarkUsed", mock.Anything, "token-123").Return(nil)
	userRepo.On("UpdatePassword", mock.Anything, "user-456", mock.AnythingOfType("string")).Return(nil)

	body := `{"currentPassword": "ignored", "password": "NewPass123!", "token": "plaintexttoken"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-456/reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "user-456")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.ResetPassword(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	resetRepo.AssertExpectations(t)
	userRepo.AssertExpectations(t)
}

func TestPasswordResetConfirm_ExpiredToken(t *testing.T) {
	h, resetRepo, _, _ := newTestPasswordResetHandler()

	expiredToken := &domain.PasswordResetToken{
		ID:        "token-expired",
		UserID:    "user-456",
		TokenHash: "somehash",
		ExpiresAt: time.Now().Add(-time.Hour), // Expired
	}

	resetRepo.On("GetByTokenHash", mock.Anything, mock.AnythingOfType("string")).Return(expiredToken, nil)

	body := `{"password": "NewPass123!", "token": "plaintexttoken"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-456/reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "user-456")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.ResetPassword(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	resetRepo.AssertExpectations(t)
}

func TestPasswordResetConfirm_InvalidToken(t *testing.T) {
	h, resetRepo, _, _ := newTestPasswordResetHandler()

	resetRepo.On("GetByTokenHash", mock.Anything, mock.AnythingOfType("string")).Return(nil, domain.ErrInvalidToken)

	body := `{"password": "NewPass123!", "token": "badtoken"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-456/reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "user-456")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.ResetPassword(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	resetRepo.AssertExpectations(t)
}

func TestPasswordResetConfirm_MissingFields(t *testing.T) {
	h, _, _, _ := newTestPasswordResetHandler()

	body := `{"token": "sometoken"}` // Missing password
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-456/reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "user-456")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.ResetPassword(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPasswordResetConfirm_TokenUserMismatch(t *testing.T) {
	h, resetRepo, _, _ := newTestPasswordResetHandler()

	// Token belongs to a different user
	resetToken := &domain.PasswordResetToken{
		ID:        "token-123",
		UserID:    "different-user",
		TokenHash: "somehash",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	resetRepo.On("GetByTokenHash", mock.Anything, mock.AnythingOfType("string")).Return(resetToken, nil)

	body := `{"password": "NewPass123!", "token": "plaintexttoken"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/user-456/reset-password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "user-456")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.ResetPassword(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	resetRepo.AssertExpectations(t)
}
