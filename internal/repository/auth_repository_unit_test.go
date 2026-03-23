package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/usecase"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAuthMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newAuthRepo(t *testing.T) (*authRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupAuthMockDB(t)
	repo := NewAuthRepository(db).(*authRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func TestAuthRepository_Unit_CreateRefreshToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		token := &usecase.RefreshToken{
			ID:        uuid.New().String(),
			UserID:    uuid.New().String(),
			Token:     "refresh-token-value",
			ExpiresAt: time.Now().Add(24 * time.Hour),
			CreatedAt: time.Now(),
		}

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO refresh_tokens (id, user_id, token, expires_at, created_at)`)).
			WithArgs(token.ID, token.UserID, token.Token, token.ExpiresAt, token.CreatedAt).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.CreateRefreshToken(ctx, token)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		token := &usecase.RefreshToken{
			ID:        uuid.New().String(),
			UserID:    uuid.New().String(),
			Token:     "token",
			ExpiresAt: time.Now().Add(24 * time.Hour),
			CreatedAt: time.Now(),
		}

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO refresh_tokens`)).
			WillReturnError(sql.ErrConnDone)

		err := repo.CreateRefreshToken(ctx, token)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create refresh token")
	})
}

func TestAuthRepository_Unit_GetRefreshToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		tokenValue := "refresh-token-value"
		tokenID := uuid.New().String()
		userID := uuid.New().String()
		now := time.Now()
		expiresAt := now.Add(24 * time.Hour)

		rows := sqlmock.NewRows([]string{"id", "user_id", "token", "expires_at", "created_at", "revoked_at"}).
			AddRow(tokenID, userID, tokenValue, expiresAt, now, nil)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, token, expires_at, created_at, revoked_at FROM refresh_tokens`)).
			WithArgs(tokenValue).
			WillReturnRows(rows)

		token, err := repo.GetRefreshToken(ctx, tokenValue)
		require.NoError(t, err)
		require.NotNil(t, token)
		assert.Equal(t, tokenID, token.ID)
		assert.Equal(t, userID, token.UserID)
		assert.Equal(t, tokenValue, token.Token)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		tokenValue := "nonexistent-token"

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, token`)).
			WithArgs(tokenValue).
			WillReturnError(sql.ErrNoRows)

		token, err := repo.GetRefreshToken(ctx, tokenValue)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refresh token not found or expired")
		assert.Nil(t, token)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		tokenValue := "some-token"

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id`)).
			WithArgs(tokenValue).
			WillReturnError(sql.ErrConnDone)

		token, err := repo.GetRefreshToken(ctx, tokenValue)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get refresh token")
		assert.Nil(t, token)
	})
}

func TestAuthRepository_Unit_RevokeRefreshToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		tokenValue := "token-to-revoke"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token = $1`)).
			WithArgs(tokenValue).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.RevokeRefreshToken(ctx, tokenValue)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found (0 rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		tokenValue := "nonexistent-token"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked_at = NOW()`)).
			WithArgs(tokenValue).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.RevokeRefreshToken(ctx, tokenValue)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refresh token not found")
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		tokenValue := "some-token"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens`)).
			WithArgs(tokenValue).
			WillReturnError(sql.ErrConnDone)

		err := repo.RevokeRefreshToken(ctx, tokenValue)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to revoke refresh token")
	})
}

func TestAuthRepository_Unit_RevokeAllUserTokens(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 3))

		err := repo.RevokeAllUserTokens(ctx, userID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no tokens to revoke (still success)", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked_at = NOW()`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.RevokeAllUserTokens(ctx, userID)
		require.NoError(t, err)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens`)).
			WithArgs(userID).
			WillReturnError(sql.ErrConnDone)

		err := repo.RevokeAllUserTokens(ctx, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to revoke all user tokens")
	})
}

func TestAuthRepository_Unit_CleanExpiredTokens(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM refresh_tokens WHERE expires_at < NOW() OR revoked_at < NOW() - INTERVAL '30 days'`)).
			WillReturnResult(sqlmock.NewResult(0, 5))

		err := repo.CleanExpiredTokens(ctx)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM refresh_tokens`)).
			WillReturnError(sql.ErrConnDone)

		err := repo.CleanExpiredTokens(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to clean expired tokens")
	})
}

func TestAuthRepository_Unit_CreateSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()
		userID := uuid.New().String()
		expiresAt := time.Now().Add(1 * time.Hour)

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO sessions (id, user_id, expires_at, created_at)`)).
			WithArgs(sessionID, userID, expiresAt).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.CreateSession(ctx, sessionID, userID, expiresAt)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()
		userID := uuid.New().String()
		expiresAt := time.Now().Add(1 * time.Hour)

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO sessions`)).
			WillReturnError(sql.ErrConnDone)

		err := repo.CreateSession(ctx, sessionID, userID, expiresAt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create session")
	})
}

func TestAuthRepository_Unit_GetSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()
		userID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"user_id"}).AddRow(userID)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_id FROM sessions WHERE id = $1 AND expires_at > NOW()`)).
			WithArgs(sessionID).
			WillReturnRows(rows)

		retrievedUserID, err := repo.GetSession(ctx, sessionID)
		require.NoError(t, err)
		assert.Equal(t, userID, retrievedUserID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found or expired", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_id FROM sessions`)).
			WithArgs(sessionID).
			WillReturnError(sql.ErrNoRows)

		retrievedUserID, err := repo.GetSession(ctx, sessionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found or expired")
		assert.Empty(t, retrievedUserID)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT user_id`)).
			WithArgs(sessionID).
			WillReturnError(sql.ErrConnDone)

		retrievedUserID, err := repo.GetSession(ctx, sessionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get session")
		assert.Empty(t, retrievedUserID)
	})
}

func TestAuthRepository_Unit_DeleteSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM sessions WHERE id = $1`)).
			WithArgs(sessionID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteSession(ctx, sessionID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		sessionID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM sessions`)).
			WithArgs(sessionID).
			WillReturnError(sql.ErrConnDone)

		err := repo.DeleteSession(ctx, sessionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete session")
	})
}

func TestAuthRepository_Unit_DeleteAllUserSessions(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM sessions WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := repo.DeleteAllUserSessions(ctx, userID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newAuthRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM sessions`)).
			WithArgs(userID).
			WillReturnError(sql.ErrConnDone)

		err := repo.DeleteAllUserSessions(ctx, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete all user sessions")
	})
}
