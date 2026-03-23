package usecase

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestTwoFAService_GetStatus(t *testing.T) {
	t.Run("2FA enabled", func(t *testing.T) {
		userRepo := new(MockTwoFAUserRepo)
		svc := &TwoFAService{userRepo: userRepo}
		ctx := context.Background()
		confirmedAt := time.Now().Add(-24 * time.Hour)

		userRepo.On("GetByID", ctx, "user-1").Return(&domain.User{
			ID:               "user-1",
			TwoFAEnabled:     true,
			TwoFAConfirmedAt: sql.NullTime{Time: confirmedAt, Valid: true},
		}, nil)

		status, err := svc.GetStatus(ctx, "user-1")
		require.NoError(t, err)
		assert.True(t, status.Enabled)
		require.NotNil(t, status.ConfirmedAt)
	})

	t.Run("2FA disabled", func(t *testing.T) {
		userRepo := new(MockTwoFAUserRepo)
		svc := &TwoFAService{userRepo: userRepo}
		ctx := context.Background()

		userRepo.On("GetByID", ctx, "user-2").Return(&domain.User{
			ID:           "user-2",
			TwoFAEnabled: false,
		}, nil)

		status, err := svc.GetStatus(ctx, "user-2")
		require.NoError(t, err)
		assert.False(t, status.Enabled)
		assert.Nil(t, status.ConfirmedAt)
	})

	t.Run("user not found", func(t *testing.T) {
		userRepo := new(MockTwoFAUserRepo)
		svc := &TwoFAService{userRepo: userRepo}
		ctx := context.Background()

		userRepo.On("GetByID", ctx, "missing").Return(nil, errors.New("not found"))

		_, err := svc.GetStatus(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user")
	})
}

func TestTwoFAService_VerifyBackupCode(t *testing.T) {
	t.Run("no codes returns error", func(t *testing.T) {
		backupRepo := new(MockTwoFABackupCodeRepo)
		svc := &TwoFAService{backupCodeRepo: backupRepo}
		ctx := context.Background()

		backupRepo.On("GetUnusedForUser", ctx, "user-1").Return([]*domain.TwoFABackupCode{}, nil)

		err := svc.verifyBackupCode(ctx, "user-1", "12345678")
		require.ErrorIs(t, err, domain.ErrTwoFAInvalidCode)
	})

	t.Run("no matching code", func(t *testing.T) {
		backupRepo := new(MockTwoFABackupCodeRepo)
		svc := &TwoFAService{backupCodeRepo: backupRepo}
		ctx := context.Background()

		hash, _ := bcrypt.GenerateFromPassword([]byte("CORRECT1"), bcrypt.MinCost)
		backupRepo.On("GetUnusedForUser", ctx, "user-1").Return([]*domain.TwoFABackupCode{
			{ID: "bc-1", CodeHash: string(hash)},
		}, nil)

		err := svc.verifyBackupCode(ctx, "user-1", "WRONGCODE")
		require.ErrorIs(t, err, domain.ErrTwoFAInvalidCode)
	})

	t.Run("matching code marks used", func(t *testing.T) {
		backupRepo := new(MockTwoFABackupCodeRepo)
		svc := &TwoFAService{backupCodeRepo: backupRepo}
		ctx := context.Background()

		code := "CORRECT1"
		hash, _ := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
		backupRepo.On("GetUnusedForUser", ctx, "user-1").Return([]*domain.TwoFABackupCode{
			{ID: "bc-1", CodeHash: string(hash)},
		}, nil)
		backupRepo.On("MarkAsUsed", ctx, "bc-1").Return(nil)

		err := svc.verifyBackupCode(ctx, "user-1", code)
		require.NoError(t, err)
		backupRepo.AssertCalled(t, "MarkAsUsed", ctx, "bc-1")
	})

	t.Run("repo error", func(t *testing.T) {
		backupRepo := new(MockTwoFABackupCodeRepo)
		svc := &TwoFAService{backupCodeRepo: backupRepo}
		ctx := context.Background()

		backupRepo.On("GetUnusedForUser", ctx, "user-1").Return(([]*domain.TwoFABackupCode)(nil), errors.New("db error"))

		err := svc.verifyBackupCode(ctx, "user-1", "anycode")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get backup codes")
	})
}

type MockTwoFABackupCodeRepo struct {
	mock.Mock
}

func (m *MockTwoFABackupCodeRepo) Create(ctx context.Context, code *domain.TwoFABackupCode) error {
	args := m.Called(ctx, code)
	return args.Error(0)
}

func (m *MockTwoFABackupCodeRepo) GetUnusedForUser(ctx context.Context, userID string) ([]*domain.TwoFABackupCode, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.TwoFABackupCode), args.Error(1)
}

func (m *MockTwoFABackupCodeRepo) MarkAsUsed(ctx context.Context, codeID string) error {
	args := m.Called(ctx, codeID)
	return args.Error(0)
}

func (m *MockTwoFABackupCodeRepo) DeleteAllForUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockTwoFABackupCodeRepo) CountUnusedForUser(ctx context.Context, userID string) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}
