package usecase

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGenerateToken_IsHex64(t *testing.T) {
	tok := generateToken()
	assert.Len(t, tok, 64, "token should be 64 hex chars")
	_, err := hex.DecodeString(tok)
	assert.NoError(t, err, "token should be valid hex")
}

func TestGenerateCode_IsSixDigits(t *testing.T) {
	code := generateCode()
	re := regexp.MustCompile(`^\d{6}$`)
	assert.True(t, re.MatchString(code), "code should be six digits")
}

// --- Mocks for email verification service tests ---

type MockEVUserRepo struct {
	mock.Mock
}

func (m *MockEVUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}

func (m *MockEVUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockEVUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockEVUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockEVUserRepo) Update(ctx context.Context, user *domain.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockEVUserRepo) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockEVUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}

func (m *MockEVUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockEVUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

func (m *MockEVUserRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockEVUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	args := m.Called(ctx, userID, ipfsCID, webpCID)
	return args.Error(0)
}

func (m *MockEVUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockEVUserRepo) Anonymize(_ context.Context, _ string) error { return nil }

type MockEmailVerificationRepo struct {
	mock.Mock
}

func (m *MockEmailVerificationRepo) CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockEmailVerificationRepo) GetVerificationToken(ctx context.Context, token string) (*domain.EmailVerificationToken, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EmailVerificationToken), args.Error(1)
}

func (m *MockEmailVerificationRepo) GetVerificationTokenByCode(ctx context.Context, code string, userID string) (*domain.EmailVerificationToken, error) {
	args := m.Called(ctx, code, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EmailVerificationToken), args.Error(1)
}

func (m *MockEmailVerificationRepo) MarkTokenAsUsed(ctx context.Context, tokenID string) error {
	args := m.Called(ctx, tokenID)
	return args.Error(0)
}

func (m *MockEmailVerificationRepo) DeleteExpiredTokens(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockEmailVerificationRepo) GetLatestTokenForUser(ctx context.Context, userID string) (*domain.EmailVerificationToken, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EmailVerificationToken), args.Error(1)
}

func (m *MockEmailVerificationRepo) RevokeAllUserTokens(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

type MockEmailServiceImpl struct {
	mock.Mock
}

func (m *MockEmailServiceImpl) SendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	args := m.Called(ctx, toEmail, username, token, code)
	return args.Error(0)
}

func (m *MockEmailServiceImpl) SendResendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	args := m.Called(ctx, toEmail, username, token, code)
	return args.Error(0)
}

func (m *MockEmailServiceImpl) SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error {
	args := m.Called(ctx, toEmail, username, token)
	return args.Error(0)
}

// --- Tests ---

func TestNewEmailVerificationService(t *testing.T) {
	userRepo := new(MockEVUserRepo)
	verifyRepo := new(MockEmailVerificationRepo)
	emailSvc := new(MockEmailServiceImpl)

	svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)

	require.NotNil(t, svc)
	assert.Equal(t, userRepo, svc.userRepo)
	assert.Equal(t, verifyRepo, svc.verifyRepo)
	assert.Equal(t, emailSvc, svc.emailService)
}

func TestSendVerificationEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("successful send", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-1",
			Email:         "test@example.com",
			Username:      "testuser",
			EmailVerified: false,
		}

		userRepo.On("GetByID", ctx, "user-1").Return(user, nil)
		verifyRepo.On("RevokeAllUserTokens", ctx, "user-1").Return(nil)
		verifyRepo.On("CreateVerificationToken", ctx, mock.AnythingOfType("*domain.EmailVerificationToken")).Return(nil)
		emailSvc.On("SendVerificationEmail", ctx, "test@example.com", "testuser", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.SendVerificationEmail(ctx, "user-1")

		assert.NoError(t, err)
		userRepo.AssertExpectations(t)
		verifyRepo.AssertExpectations(t)
		emailSvc.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		userRepo.On("GetByID", ctx, "bad-id").Return(nil, errors.New("not found"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.SendVerificationEmail(ctx, "bad-id")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user")
	})

	t.Run("email already verified", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-2",
			Email:         "verified@example.com",
			EmailVerified: true,
		}

		userRepo.On("GetByID", ctx, "user-2").Return(user, nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.SendVerificationEmail(ctx, "user-2")

		assert.ErrorIs(t, err, domain.ErrEmailAlreadyVerified)
	})

	t.Run("create token fails", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-3",
			Email:         "test3@example.com",
			Username:      "testuser3",
			EmailVerified: false,
		}

		userRepo.On("GetByID", ctx, "user-3").Return(user, nil)
		verifyRepo.On("RevokeAllUserTokens", ctx, "user-3").Return(nil)
		verifyRepo.On("CreateVerificationToken", ctx, mock.Anything).Return(errors.New("db error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.SendVerificationEmail(ctx, "user-3")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create verification token")
	})

	t.Run("send email fails", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-4",
			Email:         "test4@example.com",
			Username:      "testuser4",
			EmailVerified: false,
		}

		userRepo.On("GetByID", ctx, "user-4").Return(user, nil)
		verifyRepo.On("RevokeAllUserTokens", ctx, "user-4").Return(nil)
		verifyRepo.On("CreateVerificationToken", ctx, mock.Anything).Return(nil)
		emailSvc.On("SendVerificationEmail", ctx, "test4@example.com", "testuser4", mock.Anything, mock.Anything).Return(errors.New("smtp error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.SendVerificationEmail(ctx, "user-4")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send verification email")
	})
}

func TestVerifyEmailWithToken(t *testing.T) {
	ctx := context.Background()

	t.Run("successful verification", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-1",
			UserID:    "user-1",
			Token:     "abc123",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}

		verifyRepo.On("GetVerificationToken", ctx, "abc123").Return(token, nil)
		userRepo.On("MarkEmailAsVerified", ctx, "user-1").Return(nil)
		verifyRepo.On("MarkTokenAsUsed", ctx, "tok-1").Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithToken(ctx, "abc123")

		assert.NoError(t, err)
		verifyRepo.AssertExpectations(t)
		userRepo.AssertExpectations(t)
	})

	t.Run("token not found", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		verifyRepo.On("GetVerificationToken", ctx, "nonexistent").Return(nil, errors.New("not found"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithToken(ctx, "nonexistent")

		assert.Error(t, err)
	})

	t.Run("token expired", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-2",
			UserID:    "user-2",
			Token:     "expired-tok",
			ExpiresAt: time.Now().Add(-1 * time.Hour),
		}

		verifyRepo.On("GetVerificationToken", ctx, "expired-tok").Return(token, nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithToken(ctx, "expired-tok")

		assert.ErrorIs(t, err, domain.ErrVerificationTokenExpired)
	})

	t.Run("mark email verified fails", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-3",
			UserID:    "user-3",
			Token:     "tok-val",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}

		verifyRepo.On("GetVerificationToken", ctx, "tok-val").Return(token, nil)
		userRepo.On("MarkEmailAsVerified", ctx, "user-3").Return(errors.New("db error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithToken(ctx, "tok-val")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark email as verified")
	})

	t.Run("mark token used fails", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-4",
			UserID:    "user-4",
			Token:     "tok-val-4",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}

		verifyRepo.On("GetVerificationToken", ctx, "tok-val-4").Return(token, nil)
		userRepo.On("MarkEmailAsVerified", ctx, "user-4").Return(nil)
		verifyRepo.On("MarkTokenAsUsed", ctx, "tok-4").Return(errors.New("db error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithToken(ctx, "tok-val-4")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark token as used")
	})
}

func TestVerifyEmailWithCode(t *testing.T) {
	ctx := context.Background()

	t.Run("successful verification with code", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-c1",
			UserID:    "user-c1",
			Code:      "123456",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}

		verifyRepo.On("GetVerificationTokenByCode", ctx, "123456", "user-c1").Return(token, nil)
		userRepo.On("MarkEmailAsVerified", ctx, "user-c1").Return(nil)
		verifyRepo.On("MarkTokenAsUsed", ctx, "tok-c1").Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithCode(ctx, "123456", "user-c1")

		assert.NoError(t, err)
	})

	t.Run("code not found", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		verifyRepo.On("GetVerificationTokenByCode", ctx, "000000", "user-x").Return(nil, errors.New("not found"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithCode(ctx, "000000", "user-x")

		assert.Error(t, err)
	})

	t.Run("code expired", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-c2",
			UserID:    "user-c2",
			Code:      "999999",
			ExpiresAt: time.Now().Add(-1 * time.Hour),
		}

		verifyRepo.On("GetVerificationTokenByCode", ctx, "999999", "user-c2").Return(token, nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithCode(ctx, "999999", "user-c2")

		assert.ErrorIs(t, err, domain.ErrVerificationTokenExpired)
	})

	t.Run("mark email verified fails for code", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-c3",
			UserID:    "user-c3",
			Code:      "555555",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}

		verifyRepo.On("GetVerificationTokenByCode", ctx, "555555", "user-c3").Return(token, nil)
		userRepo.On("MarkEmailAsVerified", ctx, "user-c3").Return(errors.New("db error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithCode(ctx, "555555", "user-c3")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark email as verified")
	})

	t.Run("mark token used fails for code", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		token := &domain.EmailVerificationToken{
			ID:        "tok-c4",
			UserID:    "user-c4",
			Code:      "777777",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}

		verifyRepo.On("GetVerificationTokenByCode", ctx, "777777", "user-c4").Return(token, nil)
		userRepo.On("MarkEmailAsVerified", ctx, "user-c4").Return(nil)
		verifyRepo.On("MarkTokenAsUsed", ctx, "tok-c4").Return(errors.New("db error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.VerifyEmailWithCode(ctx, "777777", "user-c4")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark token as used")
	})
}

func TestResendVerificationEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("resend with recent existing token", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-r1",
			Email:         "resend@example.com",
			Username:      "resenduser",
			EmailVerified: false,
		}

		recentToken := &domain.EmailVerificationToken{
			ID:        "tok-r1",
			UserID:    "user-r1",
			Token:     "recent-token",
			Code:      "111111",
			CreatedAt: time.Now().Add(-2 * time.Minute), // Created 2 minutes ago (within 5 min)
			ExpiresAt: time.Now().Add(22 * time.Hour),
		}

		userRepo.On("GetByEmail", ctx, "resend@example.com").Return(user, nil)
		verifyRepo.On("GetLatestTokenForUser", ctx, "user-r1").Return(recentToken, nil)
		emailSvc.On("SendResendVerificationEmail", ctx, "resend@example.com", "resenduser", "recent-token", "111111").Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "resend@example.com")

		assert.NoError(t, err)
		emailSvc.AssertExpectations(t)
	})

	t.Run("resend creates new token when old token is stale", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-r2",
			Email:         "old@example.com",
			Username:      "olduser",
			EmailVerified: false,
		}

		staleToken := &domain.EmailVerificationToken{
			ID:        "tok-r2",
			UserID:    "user-r2",
			Token:     "stale-token",
			Code:      "222222",
			CreatedAt: time.Now().Add(-10 * time.Minute), // Created 10 minutes ago
			ExpiresAt: time.Now().Add(14 * time.Hour),
		}

		userRepo.On("GetByEmail", ctx, "old@example.com").Return(user, nil)
		verifyRepo.On("GetLatestTokenForUser", ctx, "user-r2").Return(staleToken, nil)
		// SendVerificationEmail path will be called
		userRepo.On("GetByID", ctx, "user-r2").Return(user, nil)
		verifyRepo.On("RevokeAllUserTokens", ctx, "user-r2").Return(nil)
		verifyRepo.On("CreateVerificationToken", ctx, mock.Anything).Return(nil)
		emailSvc.On("SendVerificationEmail", ctx, "old@example.com", "olduser", mock.Anything, mock.Anything).Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "old@example.com")

		assert.NoError(t, err)
	})

	t.Run("resend creates new token when no existing token", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-r3",
			Email:         "notoken@example.com",
			Username:      "notokenuser",
			EmailVerified: false,
		}

		userRepo.On("GetByEmail", ctx, "notoken@example.com").Return(user, nil)
		verifyRepo.On("GetLatestTokenForUser", ctx, "user-r3").Return(nil, sql.ErrNoRows)
		// SendVerificationEmail path
		userRepo.On("GetByID", ctx, "user-r3").Return(user, nil)
		verifyRepo.On("RevokeAllUserTokens", ctx, "user-r3").Return(nil)
		verifyRepo.On("CreateVerificationToken", ctx, mock.Anything).Return(nil)
		emailSvc.On("SendVerificationEmail", ctx, "notoken@example.com", "notokenuser", mock.Anything, mock.Anything).Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "notoken@example.com")

		assert.NoError(t, err)
	})

	t.Run("user not found", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		userRepo.On("GetByEmail", ctx, "nope@example.com").Return(nil, errors.New("not found"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "nope@example.com")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user")
	})

	t.Run("email already verified", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-r4",
			Email:         "verified@example.com",
			EmailVerified: true,
		}

		userRepo.On("GetByEmail", ctx, "verified@example.com").Return(user, nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "verified@example.com")

		assert.ErrorIs(t, err, domain.ErrEmailAlreadyVerified)
	})

	t.Run("check existing token error non-sql", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-r5",
			Email:         "tokenerr@example.com",
			Username:      "tokenerr",
			EmailVerified: false,
		}

		userRepo.On("GetByEmail", ctx, "tokenerr@example.com").Return(user, nil)
		verifyRepo.On("GetLatestTokenForUser", ctx, "user-r5").Return(nil, errors.New("db connection lost"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "tokenerr@example.com")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check existing token")
	})

	t.Run("resend email fails for existing token", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		user := &domain.User{
			ID:            "user-r6",
			Email:         "mailfail@example.com",
			Username:      "mailfailuser",
			EmailVerified: false,
		}

		recentToken := &domain.EmailVerificationToken{
			ID:        "tok-r6",
			UserID:    "user-r6",
			Token:     "recent-tok-r6",
			Code:      "333333",
			CreatedAt: time.Now().Add(-1 * time.Minute),
			ExpiresAt: time.Now().Add(23 * time.Hour),
		}

		userRepo.On("GetByEmail", ctx, "mailfail@example.com").Return(user, nil)
		verifyRepo.On("GetLatestTokenForUser", ctx, "user-r6").Return(recentToken, nil)
		emailSvc.On("SendResendVerificationEmail", ctx, "mailfail@example.com", "mailfailuser", "recent-tok-r6", "333333").Return(errors.New("smtp error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.ResendVerificationEmail(ctx, "mailfail@example.com")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resend verification email")
	})
}

func TestCleanupExpiredTokens(t *testing.T) {
	ctx := context.Background()

	t.Run("successful cleanup", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		verifyRepo.On("DeleteExpiredTokens", ctx).Return(nil)

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.CleanupExpiredTokens(ctx)

		assert.NoError(t, err)
		verifyRepo.AssertExpectations(t)
	})

	t.Run("cleanup error", func(t *testing.T) {
		userRepo := new(MockEVUserRepo)
		verifyRepo := new(MockEmailVerificationRepo)
		emailSvc := new(MockEmailServiceImpl)

		verifyRepo.On("DeleteExpiredTokens", ctx).Return(errors.New("db error"))

		svc := NewEmailVerificationService(userRepo, verifyRepo, emailSvc)
		err := svc.CleanupExpiredTokens(ctx)

		assert.Error(t, err)
	})
}
