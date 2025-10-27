package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// Mock email service for testing
type mockEmailService struct {
	sentEmails []sentEmail
	shouldFail bool
}

type sentEmail struct {
	to       string
	username string
	token    string
	code     string
}

func (m *mockEmailService) SendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	if m.shouldFail {
		return domain.NewDomainError("EMAIL_SEND_FAILED", "Failed to send email")
	}
	m.sentEmails = append(m.sentEmails, sentEmail{
		to:       toEmail,
		username: username,
		token:    token,
		code:     code,
	})
	return nil
}

func (m *mockEmailService) SendResendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	return m.SendVerificationEmail(ctx, toEmail, username, token, code)
}

func (m *mockEmailService) SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error {
	return nil
}

// Test email verification flow
func TestEmailVerification_CompleteFlow(t *testing.T) {
	// Setup test database and repositories
	ctx := context.Background()

	// Create mock repositories
	userRepo := newMockUserRepo()
	verifyRepo := &mockVerificationRepo{
		tokens: make(map[string]*domain.EmailVerificationToken),
	}

	// Create mock email service
	emailSvc := &mockEmailService{}

	// Create verification service
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, emailSvc)

	// Create handlers
	handlers := NewEmailVerificationHandlers(verificationService)

	// Test 1: Create a user
	userID := uuid.NewString()
	user := &domain.User{
		ID:            userID,
		Username:      "testuser",
		Email:         "test@example.com",
		EmailVerified: false,
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	// Test 2: Send verification email
	err = verificationService.SendVerificationEmail(ctx, userID)
	assert.NoError(t, err)

	// Verify email was sent
	assert.Len(t, emailSvc.sentEmails, 1)
	sentEmail := emailSvc.sentEmails[0]
	assert.Equal(t, "test@example.com", sentEmail.to)
	assert.Equal(t, "testuser", sentEmail.username)
	assert.NotEmpty(t, sentEmail.token)
	assert.NotEmpty(t, sentEmail.code)

	// Test 3: Verify email with token
	reqBody := domain.VerifyEmailRequest{
		Token: sentEmail.token,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handlers.VerifyEmail(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Check user is now verified
	verifiedUser, err := userRepo.GetByID(ctx, userID)
	require.NoError(t, err)
	assert.True(t, verifiedUser.EmailVerified)
}

// Test verification with code
func TestEmailVerification_WithCode(t *testing.T) {
	ctx := context.Background()

	// Setup
	userRepo := newMockUserRepo()
	verifyRepo := &mockVerificationRepo{
		tokens: make(map[string]*domain.EmailVerificationToken),
	}
	emailSvc := &mockEmailService{}
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
	handlers := NewEmailVerificationHandlers(verificationService)

	// Create user
	userID := uuid.NewString()
	user := &domain.User{
		ID:            userID,
		Username:      "codeuser",
		Email:         "code@example.com",
		EmailVerified: false,
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	userRepo.Create(ctx, user, "hashedpassword")

	// Send verification email
	verificationService.SendVerificationEmail(ctx, userID)
	sentEmail := emailSvc.sentEmails[0]

	// Verify with code (requires authentication)
	reqBody := domain.VerifyEmailRequest{
		Code: sentEmail.code,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Add user context (simulating authenticated request)
	ctx = context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handlers.VerifyEmail(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Check user is verified
	verifiedUser, _ := userRepo.GetByID(context.Background(), userID)
	assert.True(t, verifiedUser.EmailVerified)
}

// Test resend verification email
func TestEmailVerification_Resend(t *testing.T) {
	ctx := context.Background()

	// Setup
	userRepo := newMockUserRepo()
	verifyRepo := &mockVerificationRepo{
		tokens: make(map[string]*domain.EmailVerificationToken),
	}
	emailSvc := &mockEmailService{}
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
	handlers := NewEmailVerificationHandlers(verificationService)

	// Create unverified user
	userID := uuid.NewString()
	user := &domain.User{
		ID:            userID,
		Username:      "resenduser",
		Email:         "resend@example.com",
		EmailVerified: false,
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	userRepo.Create(ctx, user, "hashedpassword")

	// Request resend
	reqBody := domain.ResendVerificationRequest{
		Email: "resend@example.com",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/resend-verification", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handlers.ResendVerification(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, emailSvc.sentEmails, 1)
}

// Test already verified email
func TestEmailVerification_AlreadyVerified(t *testing.T) {
	ctx := context.Background()

	// Setup
	userRepo := newMockUserRepo()
	verifyRepo := &mockVerificationRepo{
		tokens: make(map[string]*domain.EmailVerificationToken),
	}
	emailSvc := &mockEmailService{}
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, emailSvc)

	// Create verified user
	userID := uuid.NewString()
	user := &domain.User{
		ID:            userID,
		Username:      "verifieduser",
		Email:         "verified@example.com",
		EmailVerified: true,
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	userRepo.Create(ctx, user, "hashedpassword")

	// Try to send verification email
	err := verificationService.SendVerificationEmail(ctx, userID)
	assert.Equal(t, domain.ErrEmailAlreadyVerified, err)
}

// Test expired token
func TestEmailVerification_ExpiredToken(t *testing.T) {
	ctx := context.Background()

	// Setup
	userRepo := newMockUserRepo()
	verifyRepo := &mockVerificationRepo{
		tokens: make(map[string]*domain.EmailVerificationToken),
	}
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, nil)

	// Create user
	userID := uuid.NewString()
	user := &domain.User{
		ID:            userID,
		Username:      "expireduser",
		Email:         "expired@example.com",
		EmailVerified: false,
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	userRepo.Create(ctx, user, "hashedpassword")

	// Create expired token
	expiredToken := &domain.EmailVerificationToken{
		ID:        uuid.NewString(),
		UserID:    userID,
		Token:     "expired-token",
		Code:      "123456",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // Expired
		CreatedAt: time.Now().Add(-25 * time.Hour),
	}
	verifyRepo.CreateVerificationToken(ctx, expiredToken)

	// Try to verify with expired token
	err := verificationService.VerifyEmailWithToken(ctx, "expired-token")
	assert.Equal(t, domain.ErrVerificationTokenExpired, err)
}

// Test invalid token
func TestEmailVerification_InvalidToken(t *testing.T) {
	// Setup
	userRepo := newMockUserRepo()
	verifyRepo := &mockVerificationRepo{
		tokens: make(map[string]*domain.EmailVerificationToken),
	}
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, nil)
	handlers := NewEmailVerificationHandlers(verificationService)

	// Try to verify with non-existent token
	reqBody := domain.VerifyEmailRequest{
		Token: "invalid-token",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handlers.VerifyEmail(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// Mock verification repository
type mockVerificationRepo struct {
	tokens map[string]*domain.EmailVerificationToken
}

func (m *mockVerificationRepo) CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken) error {
	m.tokens[token.Token] = token
	return nil
}

func (m *mockVerificationRepo) GetVerificationToken(ctx context.Context, token string) (*domain.EmailVerificationToken, error) {
	if t, ok := m.tokens[token]; ok && t.UsedAt == nil {
		return t, nil
	}
	return nil, domain.ErrInvalidVerificationToken
}

func (m *mockVerificationRepo) GetVerificationTokenByCode(ctx context.Context, code string, userID string) (*domain.EmailVerificationToken, error) {
	for _, t := range m.tokens {
		if t.Code == code && t.UserID == userID && t.UsedAt == nil {
			return t, nil
		}
	}
	return nil, domain.ErrInvalidVerificationCode
}

func (m *mockVerificationRepo) MarkTokenAsUsed(ctx context.Context, tokenID string) error {
	now := time.Now()
	for _, t := range m.tokens {
		if t.ID == tokenID {
			t.UsedAt = &now
			return nil
		}
	}
	return domain.ErrInvalidVerificationToken
}

func (m *mockVerificationRepo) DeleteExpiredTokens(ctx context.Context) error {
	now := time.Now()
	for k, t := range m.tokens {
		if t.ExpiresAt.Before(now) && t.UsedAt == nil {
			delete(m.tokens, k)
		}
	}
	return nil
}

func (m *mockVerificationRepo) GetLatestTokenForUser(ctx context.Context, userID string) (*domain.EmailVerificationToken, error) {
	var latest *domain.EmailVerificationToken
	for _, t := range m.tokens {
		if t.UserID == userID && t.UsedAt == nil && time.Now().Before(t.ExpiresAt) {
			if latest == nil || t.CreatedAt.After(latest.CreatedAt) {
				latest = t
			}
		}
	}
	if latest == nil {
		return nil, nil
	}
	return latest, nil
}

func (m *mockVerificationRepo) RevokeAllUserTokens(ctx context.Context, userID string) error {
	now := time.Now()
	for _, t := range m.tokens {
		if t.UserID == userID && t.UsedAt == nil {
			t.UsedAt = &now
		}
	}
	return nil
}
