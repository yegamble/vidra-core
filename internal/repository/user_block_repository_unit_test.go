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

func setupUserBlockMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newUserBlockRepo(t *testing.T) (*UserBlockRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupUserBlockMockDB(t)
	repo := NewUserBlockRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func userBlockColumns() []string {
	return []string{"id", "user_id", "block_type", "target_account_id", "target_server_host", "created_at"}
}

func TestUserBlockRepository_Unit_BlockAccount(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	targetAccountID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO user_blocks")).
			WillReturnRows(sqlmock.NewRows(userBlockColumns()).
				AddRow(uuid.New(), userID, domain.BlockTypeAccount, targetAccountID, nil, now))

		block, err := repo.BlockAccount(ctx, userID, targetAccountID)
		require.NoError(t, err)
		require.NotNil(t, block)
		assert.Equal(t, userID, block.UserID)
		assert.Equal(t, domain.BlockTypeAccount, block.BlockType)
		assert.Equal(t, &targetAccountID, block.TargetAccountID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("on conflict do nothing", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO user_blocks")).
			WillReturnRows(sqlmock.NewRows(userBlockColumns()))

		block, err := repo.BlockAccount(ctx, userID, targetAccountID)
		require.NoError(t, err)
		require.NotNil(t, block)
		// block is returned with original values if no row returned from DB
		assert.Equal(t, userID, block.UserID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO user_blocks")).
			WillReturnError(errors.New("db error"))

		block, err := repo.BlockAccount(ctx, userID, targetAccountID)
		require.Error(t, err)
		assert.Nil(t, block)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserBlockRepository_Unit_UnblockAccount(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	targetAccountName := "bob"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_blocks")).
			WithArgs(userID, targetAccountName).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UnblockAccount(ctx, userID, targetAccountName)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_blocks")).
			WithArgs(userID, targetAccountName).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UnblockAccount(ctx, userID, targetAccountName)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_blocks")).
			WithArgs(userID, targetAccountName).
			WillReturnError(errors.New("db error"))

		err := repo.UnblockAccount(ctx, userID, targetAccountName)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserBlockRepository_Unit_ListAccountBlocks(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		targetID1 := uuid.New()
		targetID2 := uuid.New()
		mock.ExpectQuery(regexp.QuoteMeta("SELECT id, user_id, block_type")).
			WithArgs(userID, 10, 0).
			WillReturnRows(sqlmock.NewRows(userBlockColumns()).
				AddRow(uuid.New(), userID, domain.BlockTypeAccount, targetID1, nil, time.Now()).
				AddRow(uuid.New(), userID, domain.BlockTypeAccount, targetID2, nil, time.Now()))

		blocks, total, err := repo.ListAccountBlocks(ctx, userID, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		assert.Len(t, blocks, 2)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count error", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WillReturnError(errors.New("count error"))

		blocks, total, err := repo.ListAccountBlocks(ctx, userID, 10, 0)
		require.Error(t, err)
		assert.Equal(t, int64(0), total)
		assert.Nil(t, blocks)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select error", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

		mock.ExpectQuery(regexp.QuoteMeta("SELECT id, user_id, block_type")).
			WillReturnError(errors.New("select error"))

		blocks, total, err := repo.ListAccountBlocks(ctx, userID, 10, 0)
		require.Error(t, err)
		assert.Equal(t, int64(0), total)
		assert.Nil(t, blocks)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserBlockRepository_Unit_BlockServer(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	host := "example.com"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO user_blocks")).
			WillReturnRows(sqlmock.NewRows(userBlockColumns()).
				AddRow(uuid.New(), userID, domain.BlockTypeServer, nil, host, now))

		block, err := repo.BlockServer(ctx, userID, host)
		require.NoError(t, err)
		require.NotNil(t, block)
		assert.Equal(t, userID, block.UserID)
		assert.Equal(t, domain.BlockTypeServer, block.BlockType)
		assert.Equal(t, &host, block.TargetServerHost)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO user_blocks")).
			WillReturnError(errors.New("db error"))

		block, err := repo.BlockServer(ctx, userID, host)
		require.Error(t, err)
		assert.Nil(t, block)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserBlockRepository_Unit_UnblockServer(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	host := "example.com"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_blocks")).
			WithArgs(userID, host).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UnblockServer(ctx, userID, host)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_blocks")).
			WithArgs(userID, host).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UnblockServer(ctx, userID, host)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUserBlockRepository_Unit_ListServerBlocks(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUserBlockRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		host := "spam.com"
		mock.ExpectQuery(regexp.QuoteMeta("SELECT id, user_id, block_type")).
			WithArgs(userID, 10, 0).
			WillReturnRows(sqlmock.NewRows(userBlockColumns()).
				AddRow(uuid.New(), userID, domain.BlockTypeServer, nil, host, time.Now()))

		blocks, total, err := repo.ListServerBlocks(ctx, userID, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Len(t, blocks, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
