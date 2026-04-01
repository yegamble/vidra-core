package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPasswordResetMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newPasswordResetRepo(t *testing.T) (*passwordResetRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupPasswordResetMockDB(t)
	repo := NewPasswordResetRepository(db).(*passwordResetRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func samplePasswordResetToken() domain.PasswordResetToken {
	now := time.Now()
	return domain.PasswordResetToken{
		ID:        "token-001",
		UserID:    "user-001",
		TokenHash: "hash123",
		ExpiresAt: now.Add(1 * time.Hour),
		CreatedAt: now,
		UsedAt:    nil,
	}
}

var passwordResetColumns = []string{
	"id", "user_id", "token_hash", "expires_at", "created_at", "used_at",
}

func makePasswordResetTokenRow(tok domain.PasswordResetToken) *sqlmock.Rows {
	return sqlmock.NewRows(passwordResetColumns).AddRow(
		tok.ID, tok.UserID, tok.TokenHash, tok.ExpiresAt, tok.CreatedAt, tok.UsedAt,
	)
}

// ---------- CreateToken ----------

func TestPasswordResetRepository_Unit_CreateToken(t *testing.T) {
	ctx := context.Background()

	insertQuery := `INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		tok := samplePasswordResetToken()

		mock.ExpectExec(regexp.QuoteMeta(insertQuery)).
			WithArgs(tok.ID, tok.UserID, tok.TokenHash, tok.ExpiresAt, tok.CreatedAt).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateToken(ctx, &tok)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		tok := samplePasswordResetToken()

		mock.ExpectExec(regexp.QuoteMeta(insertQuery)).
			WithArgs(tok.ID, tok.UserID, tok.TokenHash, tok.ExpiresAt, tok.CreatedAt).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateToken(ctx, &tok)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetByTokenHash ----------

func TestPasswordResetRepository_Unit_GetByTokenHash(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, user_id, token_hash, expires_at, created_at, used_at
		FROM password_reset_tokens
		WHERE token_hash = $1 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		tok := samplePasswordResetToken()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(tok.TokenHash).
			WillReturnRows(makePasswordResetTokenRow(tok))

		got, err := repo.GetByTokenHash(ctx, tok.TokenHash)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tok.ID, got.ID)
		assert.Equal(t, tok.TokenHash, got.TokenHash)
		assert.Equal(t, tok.UserID, got.UserID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns ErrInvalidToken", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByTokenHash(ctx, "nonexistent")
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrInvalidToken)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("broken").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetByTokenHash(ctx, "broken")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		// the function returns pointer so it may be non-nil depending on what it returns, but here it shouldn't matter as we check err
		_ = got
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- MarkUsed ----------

func TestPasswordResetRepository_Unit_MarkUsed(t *testing.T) {
	ctx := context.Background()

	updateQuery := `UPDATE password_reset_tokens
		SET used_at = $1
		WHERE id = $2 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "token-001").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.MarkUsed(ctx, "token-001")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "token-001").
			WillReturnError(errors.New("update failed"))

		err := repo.MarkUsed(ctx, "token-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "token-001").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.MarkUsed(ctx, "token-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns ErrInvalidToken", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "missing-token").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkUsed(ctx, "missing-token")
		require.ErrorIs(t, err, domain.ErrInvalidToken)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- DeleteExpiredTokens ----------

func TestPasswordResetRepository_Unit_DeleteExpiredTokens(t *testing.T) {
	ctx := context.Background()

	deleteQuery := `DELETE FROM password_reset_tokens WHERE expires_at < $1 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 5))

		err := repo.DeleteExpiredTokens(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows deleted is still success", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteExpiredTokens(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newPasswordResetRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteExpiredTokens(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
