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
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTwoFABackupCodeMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newTwoFABackupCodeRepo(t *testing.T) (*TwoFABackupCodeRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupTwoFABackupCodeMockDB(t)
	repo := NewTwoFABackupCodeRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func makeTwoFABackupCodeRows(id, userID, codeHash string, usedAt sql.NullTime, createdAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "code_hash", "used_at", "created_at"}).
		AddRow(id, userID, codeHash, usedAt, createdAt)
}

func TestTwoFABackupCodeRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success with provided ID", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		codeID := uuid.NewString()
		userID := uuid.NewString()
		now := time.Now()
		code := &domain.TwoFABackupCode{
			ID:        codeID,
			UserID:    userID,
			CodeHash:  "hash123",
			CreatedAt: now,
		}

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO twofa_backup_codes (id, user_id, code_hash, used_at, created_at)`)).
			WithArgs(codeID, userID, "hash123", nil, now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Create(ctx, code)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with generated ID", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		userID := uuid.NewString()
		code := &domain.TwoFABackupCode{
			UserID:   userID,
			CodeHash: "hash456",
		}

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO twofa_backup_codes (id, user_id, code_hash, used_at, created_at)`)).
			WithArgs(sqlmock.AnyArg(), userID, "hash456", nil, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Create(ctx, code)
		require.NoError(t, err)
		assert.NotEmpty(t, code.ID)
		assert.False(t, code.CreatedAt.IsZero())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with used_at set", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		codeID := uuid.NewString()
		userID := uuid.NewString()
		now := time.Now()
		usedAt := sql.NullTime{Time: now.Add(-1 * time.Hour), Valid: true}
		code := &domain.TwoFABackupCode{
			ID:        codeID,
			UserID:    userID,
			CodeHash:  "hash789",
			UsedAt:    usedAt,
			CreatedAt: now,
		}

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO twofa_backup_codes (id, user_id, code_hash, used_at, created_at)`)).
			WithArgs(codeID, userID, "hash789", usedAt.Time, now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Create(ctx, code)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert failure", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		code := &domain.TwoFABackupCode{
			ID:       uuid.NewString(),
			UserID:   uuid.NewString(),
			CodeHash: "hashfail",
		}

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO twofa_backup_codes (id, user_id, code_hash, used_at, created_at)`)).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, code)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create backup code")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestTwoFABackupCodeRepository_Unit_GetUnusedForUser(t *testing.T) {
	ctx := context.Background()
	userID := uuid.NewString()
	now := time.Now()

	t.Run("success with multiple codes", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		rows := makeTwoFABackupCodeRows(
			uuid.NewString(), userID, "hash1", sql.NullTime{}, now,
		).AddRow(
			uuid.NewString(), userID, "hash2", sql.NullTime{}, now.Add(1*time.Second),
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, code_hash, used_at, created_at FROM twofa_backup_codes WHERE user_id = $1 AND used_at IS NULL ORDER BY created_at ASC`)).
			WithArgs(userID).
			WillReturnRows(rows)

		codes, err := repo.GetUnusedForUser(ctx, userID)
		require.NoError(t, err)
		require.Len(t, codes, 2)
		assert.Equal(t, "hash1", codes[0].CodeHash)
		assert.Equal(t, "hash2", codes[1].CodeHash)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with no codes (empty result)", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, code_hash, used_at, created_at FROM twofa_backup_codes WHERE user_id = $1 AND used_at IS NULL ORDER BY created_at ASC`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "code_hash", "used_at", "created_at"}))

		codes, err := repo.GetUnusedForUser(ctx, userID)
		require.NoError(t, err)
		require.Empty(t, codes)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, code_hash, used_at, created_at FROM twofa_backup_codes WHERE user_id = $1 AND used_at IS NULL ORDER BY created_at ASC`)).
			WithArgs(userID).
			WillReturnError(errors.New("select failed"))

		codes, err := repo.GetUnusedForUser(ctx, userID)
		require.Nil(t, codes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get unused backup codes")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestTwoFABackupCodeRepository_Unit_MarkAsUsed(t *testing.T) {
	ctx := context.Background()
	codeID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE twofa_backup_codes SET used_at = $1 WHERE id = $2 AND used_at IS NULL`)).
			WithArgs(sqlmock.AnyArg(), codeID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.MarkAsUsed(ctx, codeID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("already used (zero rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE twofa_backup_codes SET used_at = $1 WHERE id = $2 AND used_at IS NULL`)).
			WithArgs(sqlmock.AnyArg(), codeID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkAsUsed(ctx, codeID)
		require.ErrorIs(t, err, domain.ErrTwoFABackupCodeUsed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE twofa_backup_codes SET used_at = $1 WHERE id = $2 AND used_at IS NULL`)).
			WithArgs(sqlmock.AnyArg(), codeID).
			WillReturnError(errors.New("update failed"))

		err := repo.MarkAsUsed(ctx, codeID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark backup code as used")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE twofa_backup_codes SET used_at = $1 WHERE id = $2 AND used_at IS NULL`)).
			WithArgs(sqlmock.AnyArg(), codeID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := repo.MarkAsUsed(ctx, codeID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestTwoFABackupCodeRepository_Unit_DeleteAllForUser(t *testing.T) {
	ctx := context.Background()
	userID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM twofa_backup_codes WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 5))

		err := repo.DeleteAllForUser(ctx, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with zero rows (no codes to delete)", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM twofa_backup_codes WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteAllForUser(ctx, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newTwoFABackupCodeRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM twofa_backup_codes WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteAllForUser(ctx, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete backup codes")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
