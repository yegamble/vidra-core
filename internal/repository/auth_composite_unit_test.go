package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/usecase"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockAuthRepository struct {
	mock.Mock
}

func (m *mockAuthRepository) CreateRefreshToken(ctx context.Context, token *usecase.RefreshToken) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *mockAuthRepository) GetRefreshToken(ctx context.Context, token string) (*usecase.RefreshToken, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*usecase.RefreshToken), args.Error(1)
}

func (m *mockAuthRepository) RevokeRefreshToken(ctx context.Context, token string) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *mockAuthRepository) RevokeAllUserTokens(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *mockAuthRepository) CleanExpiredTokens(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockAuthRepository) CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error {
	args := m.Called(ctx, sessionID, userID, expiresAt)
	return args.Error(0)
}

func (m *mockAuthRepository) GetSession(ctx context.Context, sessionID string) (string, error) {
	args := m.Called(ctx, sessionID)
	return args.String(0), args.Error(1)
}

func (m *mockAuthRepository) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *mockAuthRepository) DeleteAllUserSessions(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

type mockRedisSessionRepository struct {
	mock.Mock
}

func (m *mockRedisSessionRepository) CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error {
	args := m.Called(ctx, sessionID, userID, expiresAt)
	return args.Error(0)
}

func (m *mockRedisSessionRepository) GetSession(ctx context.Context, sessionID string) (string, error) {
	args := m.Called(ctx, sessionID)
	return args.String(0), args.Error(1)
}

func (m *mockRedisSessionRepository) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *mockRedisSessionRepository) DeleteAllUserSessions(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func TestCompositeAuthRepository_RefreshTokenMethods(t *testing.T) {
	ctx := context.Background()
	mockDB := new(mockAuthRepository)
	mockRedis := new(mockRedisSessionRepository)
	composite := NewCompositeAuthRepository(mockDB, mockRedis)

	t.Run("CreateRefreshToken delegates to dbRepo", func(t *testing.T) {
		token := &usecase.RefreshToken{Token: "test-token"}
		mockDB.On("CreateRefreshToken", ctx, token).Return(nil)

		err := composite.CreateRefreshToken(ctx, token)
		assert.NoError(t, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("GetRefreshToken delegates to dbRepo", func(t *testing.T) {
		expectedToken := &usecase.RefreshToken{Token: "test-token"}
		mockDB.On("GetRefreshToken", ctx, "test-token").Return(expectedToken, nil)

		token, err := composite.GetRefreshToken(ctx, "test-token")
		assert.NoError(t, err)
		assert.Equal(t, expectedToken, token)
		mockDB.AssertExpectations(t)
	})

	t.Run("RevokeRefreshToken delegates to dbRepo", func(t *testing.T) {
		mockDB.On("RevokeRefreshToken", ctx, "test-token").Return(nil)

		err := composite.RevokeRefreshToken(ctx, "test-token")
		assert.NoError(t, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("RevokeAllUserTokens delegates to dbRepo", func(t *testing.T) {
		mockDB.On("RevokeAllUserTokens", ctx, "user-123").Return(nil)

		err := composite.RevokeAllUserTokens(ctx, "user-123")
		assert.NoError(t, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("CleanExpiredTokens delegates to dbRepo", func(t *testing.T) {
		mockDB.On("CleanExpiredTokens", ctx).Return(nil)

		err := composite.CleanExpiredTokens(ctx)
		assert.NoError(t, err)
		mockDB.AssertExpectations(t)
	})
}

func TestCompositeAuthRepository_SessionMethods(t *testing.T) {
	ctx := context.Background()
	mockDB := new(mockAuthRepository)
	mockRedis := new(mockRedisSessionRepository)
	composite := NewCompositeAuthRepository(mockDB, mockRedis)

	t.Run("CreateSession delegates to redisRepo", func(t *testing.T) {
		expiresAt := time.Now().Add(1 * time.Hour)
		mockRedis.On("CreateSession", ctx, "session-123", "user-456", expiresAt).Return(nil)

		err := composite.CreateSession(ctx, "session-123", "user-456", expiresAt)
		assert.NoError(t, err)
		mockRedis.AssertExpectations(t)
	})

	t.Run("GetSession delegates to redisRepo", func(t *testing.T) {
		mockRedis.On("GetSession", ctx, "session-123").Return("user-456", nil)

		userID, err := composite.GetSession(ctx, "session-123")
		assert.NoError(t, err)
		assert.Equal(t, "user-456", userID)
		mockRedis.AssertExpectations(t)
	})

	t.Run("DeleteSession delegates to redisRepo", func(t *testing.T) {
		mockRedis.On("DeleteSession", ctx, "session-123").Return(nil)

		err := composite.DeleteSession(ctx, "session-123")
		assert.NoError(t, err)
		mockRedis.AssertExpectations(t)
	})

	t.Run("DeleteAllUserSessions delegates to redisRepo", func(t *testing.T) {
		mockRedis.On("DeleteAllUserSessions", ctx, "user-456").Return(nil)

		err := composite.DeleteAllUserSessions(ctx, "user-456")
		assert.NoError(t, err)
		mockRedis.AssertExpectations(t)
	})
}

func TestCompositeAuthRepository_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	mockDB := new(mockAuthRepository)
	mockRedis := new(mockRedisSessionRepository)
	composite := NewCompositeAuthRepository(mockDB, mockRedis)
	testErr := errors.New("test error")

	t.Run("CreateRefreshToken error", func(t *testing.T) {
		token := &usecase.RefreshToken{Token: "test"}
		mockDB.On("CreateRefreshToken", ctx, token).Return(testErr)

		err := composite.CreateRefreshToken(ctx, token)
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("GetRefreshToken error", func(t *testing.T) {
		mockDB.On("GetRefreshToken", ctx, "test").Return(nil, testErr)

		_, err := composite.GetRefreshToken(ctx, "test")
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("RevokeRefreshToken error", func(t *testing.T) {
		mockDB.On("RevokeRefreshToken", ctx, "test").Return(testErr)

		err := composite.RevokeRefreshToken(ctx, "test")
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("RevokeAllUserTokens error", func(t *testing.T) {
		mockDB.On("RevokeAllUserTokens", ctx, "user").Return(testErr)

		err := composite.RevokeAllUserTokens(ctx, "user")
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("CleanExpiredTokens error", func(t *testing.T) {
		mockDB.On("CleanExpiredTokens", ctx).Return(testErr)

		err := composite.CleanExpiredTokens(ctx)
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockDB.AssertExpectations(t)
	})

	t.Run("CreateSession error", func(t *testing.T) {
		expiresAt := time.Now()
		mockRedis.On("CreateSession", ctx, "sess", "user", expiresAt).Return(testErr)

		err := composite.CreateSession(ctx, "sess", "user", expiresAt)
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockRedis.AssertExpectations(t)
	})

	t.Run("GetSession error", func(t *testing.T) {
		mockRedis.On("GetSession", ctx, "sess").Return("", testErr)

		_, err := composite.GetSession(ctx, "sess")
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockRedis.AssertExpectations(t)
	})

	t.Run("DeleteSession error", func(t *testing.T) {
		mockRedis.On("DeleteSession", ctx, "sess").Return(testErr)

		err := composite.DeleteSession(ctx, "sess")
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockRedis.AssertExpectations(t)
	})

	t.Run("DeleteAllUserSessions error", func(t *testing.T) {
		mockRedis.On("DeleteAllUserSessions", ctx, "user").Return(testErr)

		err := composite.DeleteAllUserSessions(ctx, "user")
		assert.Error(t, err)
		assert.Equal(t, testErr, err)
		mockRedis.AssertExpectations(t)
	})
}
