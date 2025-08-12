package repository

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"
	"athena/internal/usecase"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthRepository_CreateRefreshToken(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	token := &usecase.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     "refresh_token_123",
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err := authRepo.CreateRefreshToken(ctx, token)
	require.NoError(t, err)

	retrievedToken, err := authRepo.GetRefreshToken(ctx, "refresh_token_123")
	require.NoError(t, err)
	assert.Equal(t, token.ID, retrievedToken.ID)
	assert.Equal(t, token.UserID, retrievedToken.UserID)
	assert.Equal(t, token.Token, retrievedToken.Token)
}

func TestAuthRepository_GetRefreshToken(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	// Test getting existing valid token
	token := createTestRefreshToken(t, authRepo, ctx, user.ID, "valid_token")

	retrievedToken, err := authRepo.GetRefreshToken(ctx, "valid_token")
	require.NoError(t, err)
	assert.Equal(t, token.ID, retrievedToken.ID)

	// Test getting non-existent token
	_, err = authRepo.GetRefreshToken(ctx, "nonexistent_token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test getting expired token
	expiredToken := &usecase.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     "expired_token",
		ExpiresAt: time.Now().Add(-time.Hour), // Expired 1 hour ago
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	err = authRepo.CreateRefreshToken(ctx, expiredToken)
	require.NoError(t, err)

	_, err = authRepo.GetRefreshToken(ctx, "expired_token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAuthRepository_RevokeRefreshToken(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)
	createTestRefreshToken(t, authRepo, ctx, user.ID, "token_to_revoke")

	err := authRepo.RevokeRefreshToken(ctx, "token_to_revoke")
	require.NoError(t, err)

	_, err = authRepo.GetRefreshToken(ctx, "token_to_revoke")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test revoking non-existent token
	err = authRepo.RevokeRefreshToken(ctx, "nonexistent_token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAuthRepository_RevokeAllUserTokens(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	// Create multiple tokens for the user
	createTestRefreshToken(t, authRepo, ctx, user.ID, "user_token_1")
	createTestRefreshToken(t, authRepo, ctx, user.ID, "user_token_2")
	createTestRefreshToken(t, authRepo, ctx, user.ID, "user_token_3")

	// Create token for another user
	anotherUser := createTestUserForAuth(t, userRepo, ctx)
	createTestRefreshToken(t, authRepo, ctx, anotherUser.ID, "other_user_token")

	err := authRepo.RevokeAllUserTokens(ctx, user.ID)
	require.NoError(t, err)

	// User's tokens should be revoked
	_, err = authRepo.GetRefreshToken(ctx, "user_token_1")
	assert.Error(t, err)
	_, err = authRepo.GetRefreshToken(ctx, "user_token_2")
	assert.Error(t, err)
	_, err = authRepo.GetRefreshToken(ctx, "user_token_3")
	assert.Error(t, err)

	// Other user's token should still be valid
	_, err = authRepo.GetRefreshToken(ctx, "other_user_token")
	require.NoError(t, err)
}

func TestAuthRepository_CleanExpiredTokens(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	// Create expired token
	expiredToken := &usecase.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     "expired_token",
		ExpiresAt: time.Now().Add(-time.Hour),
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	err := authRepo.CreateRefreshToken(ctx, expiredToken)
	require.NoError(t, err)

	// Create old revoked token
	revokedToken := &usecase.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     "old_revoked_token",
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now().Add(-40 * 24 * time.Hour), // 40 days ago
	}
	err = authRepo.CreateRefreshToken(ctx, revokedToken)
	require.NoError(t, err)
	err = authRepo.RevokeRefreshToken(ctx, "old_revoked_token")
	require.NoError(t, err)

	// Create valid token
	createTestRefreshToken(t, authRepo, ctx, user.ID, "valid_token")

	err = authRepo.CleanExpiredTokens(ctx)
	require.NoError(t, err)

	// Valid token should still exist
	_, err = authRepo.GetRefreshToken(ctx, "valid_token")
	require.NoError(t, err)
}

func TestAuthRepository_CreateSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	sessionID := "session_123"
	expiresAt := time.Now().Add(24 * time.Hour)

	err := authRepo.CreateSession(ctx, sessionID, user.ID, expiresAt)
	require.NoError(t, err)

	retrievedUserID, err := authRepo.GetSession(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, retrievedUserID)
}

func TestAuthRepository_GetSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	// Create valid session
	sessionID := "valid_session"
	expiresAt := time.Now().Add(24 * time.Hour)
	err := authRepo.CreateSession(ctx, sessionID, user.ID, expiresAt)
	require.NoError(t, err)

	userID, err := authRepo.GetSession(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, userID)

	// Test getting non-existent session
	_, err = authRepo.GetSession(ctx, "nonexistent_session")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test getting expired session
	expiredSessionID := "expired_session"
	expiredExpiresAt := time.Now().Add(-time.Hour)
	err = authRepo.CreateSession(ctx, expiredSessionID, user.ID, expiredExpiresAt)
	require.NoError(t, err)

	_, err = authRepo.GetSession(ctx, expiredSessionID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAuthRepository_DeleteSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	sessionID := "session_to_delete"
	expiresAt := time.Now().Add(24 * time.Hour)
	err := authRepo.CreateSession(ctx, sessionID, user.ID, expiresAt)
	require.NoError(t, err)

	err = authRepo.DeleteSession(ctx, sessionID)
	require.NoError(t, err)

	_, err = authRepo.GetSession(ctx, sessionID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAuthRepository_DeleteAllUserSessions(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)
	anotherUser := createTestUserForAuth(t, userRepo, ctx)

	expiresAt := time.Now().Add(24 * time.Hour)

	// Create sessions for both users
	err := authRepo.CreateSession(ctx, "user_session_1", user.ID, expiresAt)
	require.NoError(t, err)
	err = authRepo.CreateSession(ctx, "user_session_2", user.ID, expiresAt)
	require.NoError(t, err)
	err = authRepo.CreateSession(ctx, "other_user_session", anotherUser.ID, expiresAt)
	require.NoError(t, err)

	err = authRepo.DeleteAllUserSessions(ctx, user.ID)
	require.NoError(t, err)

	// User's sessions should be deleted
	_, err = authRepo.GetSession(ctx, "user_session_1")
	assert.Error(t, err)
	_, err = authRepo.GetSession(ctx, "user_session_2")
	assert.Error(t, err)

	// Other user's session should still exist
	userID, err := authRepo.GetSession(ctx, "other_user_session")
	require.NoError(t, err)
	assert.Equal(t, anotherUser.ID, userID)
}

func TestAuthRepository_SessionUpsert(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	userRepo := NewUserRepository(testDB.DB)
	authRepo := NewAuthRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUserForAuth(t, userRepo, ctx)

	sessionID := "upsert_session"
	expiresAt1 := time.Now().Add(1 * time.Hour)
	expiresAt2 := time.Now().Add(2 * time.Hour)

	// Create session
	err := authRepo.CreateSession(ctx, sessionID, user.ID, expiresAt1)
	require.NoError(t, err)

	// Update session (upsert)
	err = authRepo.CreateSession(ctx, sessionID, user.ID, expiresAt2)
	require.NoError(t, err)

	// Should still get the session
	userID, err := authRepo.GetSession(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, userID)
}

func createTestUserForAuth(t *testing.T, repo usecase.UserRepository, ctx context.Context) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:        uuid.New().String(),
		Username:  "testuser_" + uuid.New().String()[:8],
		Email:     "test_" + uuid.New().String()[:8] + "@example.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := repo.Create(ctx, user, "hashed_password")
	require.NoError(t, err)

	return user
}

func createTestRefreshToken(t *testing.T, repo usecase.AuthRepository, ctx context.Context, userID, token string) *usecase.RefreshToken {
	t.Helper()

	refreshToken := &usecase.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		Token:     token,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err := repo.CreateRefreshToken(ctx, refreshToken)
	require.NoError(t, err)

	return refreshToken
}
