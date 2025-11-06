package usecase

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"athena/internal/domain"
)

// MockTwoFAUserRepo is a mock implementation of UserRepository for 2FA testing
type MockTwoFAUserRepo struct {
	mock.Mock
}

func (m *MockTwoFAUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}

func (m *MockTwoFAUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockTwoFAUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockTwoFAUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockTwoFAUserRepo) Update(ctx context.Context, user *domain.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockTwoFAUserRepo) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockTwoFAUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}

func (m *MockTwoFAUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockTwoFAUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

func (m *MockTwoFAUserRepo) UpdateEmailVerification(ctx context.Context, userID string, verified bool) error {
	args := m.Called(ctx, userID, verified)
	return args.Error(0)
}

func (m *MockTwoFAUserRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockTwoFAUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	args := m.Called(ctx, userID, ipfsCID, webpCID)
	return args.Error(0)
}

func (m *MockTwoFAUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// MockTwoFABackupCodeRepository is a mock implementation of TwoFABackupCodeRepository
type MockTwoFABackupCodeRepository struct {
	mock.Mock
}

func (m *MockTwoFABackupCodeRepository) Create(ctx context.Context, code *domain.TwoFABackupCode) error {
	args := m.Called(ctx, code)
	return args.Error(0)
}

func (m *MockTwoFABackupCodeRepository) GetUnusedForUser(ctx context.Context, userID string) ([]*domain.TwoFABackupCode, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.TwoFABackupCode), args.Error(1)
}

func (m *MockTwoFABackupCodeRepository) MarkAsUsed(ctx context.Context, codeID string) error {
	args := m.Called(ctx, codeID)
	return args.Error(0)
}

func (m *MockTwoFABackupCodeRepository) DeleteAllForUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func TestTwoFAService_GenerateSecret(t *testing.T) {
	ctx := context.Background()

	t.Run("Successfully generates secret for user without 2FA", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)
		mockUserRepo.On("Update", ctx, mock.AnythingOfType("*domain.User")).Return(nil)
		mockBackupCodeRepo.On("Create", ctx, mock.AnythingOfType("*domain.TwoFABackupCode")).Return(nil).Times(10)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		result, err := service.GenerateSecret(ctx, "user-123")

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.NotEmpty(t, result.Secret)
		assert.NotEmpty(t, result.QRCodeURI)
		assert.Len(t, result.BackupCodes, 10)

		mockUserRepo.AssertExpectations(t)
		mockBackupCodeRepo.AssertExpectations(t)
	})

	t.Run("Fails if 2FA already enabled", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		result, err := service.GenerateSecret(ctx, "user-123")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFAAlreadyEnabled, err)
		assert.Nil(t, result)

		mockUserRepo.AssertExpectations(t)
	})
}

func TestTwoFAService_VerifySetup(t *testing.T) {
	ctx := context.Background()

	t.Run("Successfully verifies correct TOTP code", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		// Generate a valid TOTP secret
		secret := "JBSWY3DPEHPK3PXP"
		code, err := totp.GenerateCode(secret, time.Now())
		assert.NoError(t, err)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
			TwoFASecret:  secret,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)
		mockUserRepo.On("Update", ctx, mock.AnythingOfType("*domain.User")).Return(nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err = service.VerifySetup(ctx, "user-123", code)

		assert.NoError(t, err)
		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Fails with invalid TOTP code", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
			TwoFASecret:  "JBSWY3DPEHPK3PXP",
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err := service.VerifySetup(ctx, "user-123", "000000")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFAInvalidCode, err)
		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Fails if 2FA already enabled", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err := service.VerifySetup(ctx, "user-123", "123456")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFAAlreadyEnabled, err)
		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Fails if setup is incomplete (no secret)", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
			TwoFASecret:  "",
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err := service.VerifySetup(ctx, "user-123", "123456")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFASetupIncomplete, err)
		mockUserRepo.AssertExpectations(t)
	})
}

func TestTwoFAService_VerifyCode(t *testing.T) {
	ctx := context.Background()

	t.Run("Successfully verifies valid TOTP code", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		secret := "JBSWY3DPEHPK3PXP"
		code, err := totp.GenerateCode(secret, time.Now())
		assert.NoError(t, err)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
			TwoFASecret:  secret,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err = service.VerifyCode(ctx, "user-123", code)

		assert.NoError(t, err)
		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Fails if 2FA not enabled", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err := service.VerifyCode(ctx, "user-123", "123456")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFANotEnabled, err)
		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Fails with invalid code", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
			TwoFASecret:  "JBSWY3DPEHPK3PXP",
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)
		mockBackupCodeRepo.On("GetUnusedForUser", ctx, "user-123").Return([]*domain.TwoFABackupCode{}, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		// Use an 8-character invalid code to trigger backup code check
		err := service.VerifyCode(ctx, "user-123", "INVALID1")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFAInvalidCode, err)
		mockUserRepo.AssertExpectations(t)
		mockBackupCodeRepo.AssertExpectations(t)
	})
}

func TestTwoFAService_Disable(t *testing.T) {
	ctx := context.Background()

	t.Run("Successfully disables 2FA with valid password and code", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		secret := "JBSWY3DPEHPK3PXP"
		code, err := totp.GenerateCode(secret, time.Now())
		assert.NoError(t, err)

		// This is the bcrypt hash of "password123" (verified)
		passwordHash := "$2a$10$az.56lD0eF3Zl4n3xQ3UW.dLiokl2S5hUvVHeklFrcA.qqrA3odVq"

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
			TwoFASecret:  secret,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil).Times(2)
		mockUserRepo.On("GetPasswordHash", ctx, "user-123").Return(passwordHash, nil)
		mockUserRepo.On("Update", ctx, mock.AnythingOfType("*domain.User")).Return(nil)
		mockBackupCodeRepo.On("DeleteAllForUser", ctx, "user-123").Return(nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err = service.Disable(ctx, "user-123", "password123", code)

		assert.NoError(t, err)
		mockUserRepo.AssertExpectations(t)
		mockBackupCodeRepo.AssertExpectations(t)
	})

	t.Run("Fails with invalid password", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		// This is the bcrypt hash of "password123" (verified)
		passwordHash := "$2a$10$az.56lD0eF3Zl4n3xQ3UW.dLiokl2S5hUvVHeklFrcA.qqrA3odVq"

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
			TwoFASecret:  "JBSWY3DPEHPK3PXP",
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)
		mockUserRepo.On("GetPasswordHash", ctx, "user-123").Return(passwordHash, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err := service.Disable(ctx, "user-123", "wrongpassword", "123456")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrInvalidCredentials, err)
		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Fails if 2FA not enabled", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		err := service.Disable(ctx, "user-123", "password123", "123456")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFANotEnabled, err)
		mockUserRepo.AssertExpectations(t)
	})
}

func TestTwoFAService_RegenerateBackupCodes(t *testing.T) {
	ctx := context.Background()

	t.Run("Successfully regenerates backup codes", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		secret := "JBSWY3DPEHPK3PXP"
		code, err := totp.GenerateCode(secret, time.Now())
		assert.NoError(t, err)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: true,
			TwoFASecret:  secret,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil).Times(2)
		mockBackupCodeRepo.On("DeleteAllForUser", ctx, "user-123").Return(nil)
		mockBackupCodeRepo.On("Create", ctx, mock.AnythingOfType("*domain.TwoFABackupCode")).Return(nil).Times(10)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		backupCodes, err := service.RegenerateBackupCodes(ctx, "user-123", code)

		assert.NoError(t, err)
		assert.Len(t, backupCodes, 10)
		mockUserRepo.AssertExpectations(t)
		mockBackupCodeRepo.AssertExpectations(t)
	})

	t.Run("Fails if 2FA not enabled", func(t *testing.T) {
		mockUserRepo := new(MockTwoFAUserRepo)
		mockBackupCodeRepo := new(MockTwoFABackupCodeRepository)

		user := &domain.User{
			ID:           "user-123",
			Email:        "test@example.com",
			TwoFAEnabled: false,
		}

		mockUserRepo.On("GetByID", ctx, "user-123").Return(user, nil)

		service := NewTwoFAService(mockUserRepo, mockBackupCodeRepo, "TestApp")
		backupCodes, err := service.RegenerateBackupCodes(ctx, "user-123", "123456")

		assert.Error(t, err)
		assert.Equal(t, domain.ErrTwoFANotEnabled, err)
		assert.Nil(t, backupCodes)
		mockUserRepo.AssertExpectations(t)
	})
}
