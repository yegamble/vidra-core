package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupEmailVerificationMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newEmailVerificationRepo(t *testing.T) (*EmailVerificationRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupEmailVerificationMockDB(t)
	repo := NewEmailVerificationRepository(db).(*EmailVerificationRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleVerificationToken() domain.EmailVerificationToken {
	now := time.Now()
	return domain.EmailVerificationToken{
		ID:        "token-001",
		UserID:    "user-001",
		Token:     "abc123token",
		Code:      "123456",
		ExpiresAt: now.Add(1 * time.Hour),
		CreatedAt: now,
		UsedAt:    nil,
	}
}

var emailVerificationColumns = []string{
	"id", "user_id", "token", "code", "expires_at", "created_at", "used_at",
}

func makeVerificationTokenRow(tok domain.EmailVerificationToken) *sqlmock.Rows {
	return sqlmock.NewRows(emailVerificationColumns).AddRow(
		tok.ID, tok.UserID, tok.Token, tok.Code, tok.ExpiresAt, tok.CreatedAt, tok.UsedAt,
	)
}

// ---------- CreateVerificationToken ----------

func TestEmailVerificationRepository_Unit_CreateVerificationToken(t *testing.T) {
	ctx := context.Background()

	insertQuery := `INSERT INTO email_verification_tokens (id, user_id, token, code, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		tok := sampleVerificationToken()

		mock.ExpectExec(regexp.QuoteMeta(insertQuery)).
			WithArgs(tok.ID, tok.UserID, tok.Token, tok.Code, tok.ExpiresAt, tok.CreatedAt).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateVerificationToken(ctx, &tok)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		tok := sampleVerificationToken()

		mock.ExpectExec(regexp.QuoteMeta(insertQuery)).
			WithArgs(tok.ID, tok.UserID, tok.Token, tok.Code, tok.ExpiresAt, tok.CreatedAt).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateVerificationToken(ctx, &tok)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetVerificationToken ----------

func TestEmailVerificationRepository_Unit_GetVerificationToken(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE token = $1 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		tok := sampleVerificationToken()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(tok.Token).
			WillReturnRows(makeVerificationTokenRow(tok))

		got, err := repo.GetVerificationToken(ctx, tok.Token)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tok.ID, got.ID)
		assert.Equal(t, tok.Token, got.Token)
		assert.Equal(t, tok.UserID, got.UserID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns ErrInvalidVerificationToken", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("nonexistent").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetVerificationToken(ctx, "nonexistent")
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrInvalidVerificationToken)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("broken").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetVerificationToken(ctx, "broken")
		// The source code returns (&verificationToken, err) on non-ErrNoRows errors,
		// so got may be non-nil but err will be set.
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		// The function returns the zero-value struct pointer on error
		_ = got
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetVerificationTokenByCode ----------

func TestEmailVerificationRepository_Unit_GetVerificationTokenByCode(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE code = $1 AND user_id = $2 AND used_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		tok := sampleVerificationToken()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(tok.Code, tok.UserID).
			WillReturnRows(makeVerificationTokenRow(tok))

		got, err := repo.GetVerificationTokenByCode(ctx, tok.Code, tok.UserID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tok.Code, got.Code)
		assert.Equal(t, tok.UserID, got.UserID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns ErrInvalidVerificationCode", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("000000", "user-x").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetVerificationTokenByCode(ctx, "000000", "user-x")
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrInvalidVerificationCode)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("123456", "user-001").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetVerificationTokenByCode(ctx, "123456", "user-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		_ = got
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- MarkTokenAsUsed ----------

func TestEmailVerificationRepository_Unit_MarkTokenAsUsed(t *testing.T) {
	ctx := context.Background()

	updateQuery := `UPDATE email_verification_tokens
		SET used_at = $1
		WHERE id = $2 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		// time.Now() is called inside the method, so use AnyArg
		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "token-001").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.MarkTokenAsUsed(ctx, "token-001")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "token-001").
			WillReturnError(errors.New("update failed"))

		err := repo.MarkTokenAsUsed(ctx, "token-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "token-001").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.MarkTokenAsUsed(ctx, "token-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns ErrInvalidVerificationToken", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(sqlmock.AnyArg(), "missing-token").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkTokenAsUsed(ctx, "missing-token")
		require.ErrorIs(t, err, domain.ErrInvalidVerificationToken)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- DeleteExpiredTokens ----------

func TestEmailVerificationRepository_Unit_DeleteExpiredTokens(t *testing.T) {
	ctx := context.Background()

	deleteQuery := `DELETE FROM email_verification_tokens
		WHERE expires_at < $1 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(sqlmock.AnyArg()). // time.Now() called internally
			WillReturnResult(sqlmock.NewResult(0, 5))

		err := repo.DeleteExpiredTokens(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows deleted is still success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteExpiredTokens(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
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

// ---------- GetLatestTokenForUser ----------

func TestEmailVerificationRepository_Unit_GetLatestTokenForUser(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE user_id = $1 AND used_at IS NULL AND expires_at > $2
		ORDER BY created_at DESC
		LIMIT 1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		tok := sampleVerificationToken()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(tok.UserID, sqlmock.AnyArg()). // time.Now() called internally
			WillReturnRows(makeVerificationTokenRow(tok))

		got, err := repo.GetLatestTokenForUser(ctx, tok.UserID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tok.ID, got.ID)
		assert.Equal(t, tok.UserID, got.UserID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows returns nil nil", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("user-x", sqlmock.AnyArg()).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetLatestTokenForUser(ctx, "user-x")
		require.NoError(t, err)
		require.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("user-001", sqlmock.AnyArg()).
			WillReturnError(errors.New("db error"))

		got, err := repo.GetLatestTokenForUser(ctx, "user-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
		_ = got
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- RevokeAllUserTokens ----------

func TestEmailVerificationRepository_Unit_RevokeAllUserTokens(t *testing.T) {
	ctx := context.Background()

	revokeQuery := `UPDATE email_verification_tokens
		SET used_at = $1
		WHERE user_id = $2 AND used_at IS NULL`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(revokeQuery)).
			WithArgs(sqlmock.AnyArg(), "user-001"). // time.Now() called internally
			WillReturnResult(sqlmock.NewResult(0, 3))

		err := repo.RevokeAllUserTokens(ctx, "user-001")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no tokens to revoke is still success", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(revokeQuery)).
			WithArgs(sqlmock.AnyArg(), "user-x").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.RevokeAllUserTokens(ctx, "user-x")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEmailVerificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(revokeQuery)).
			WithArgs(sqlmock.AnyArg(), "user-001").
			WillReturnError(errors.New("update failed"))

		err := repo.RevokeAllUserTokens(ctx, "user-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
