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

func setupBlacklistMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestBlacklistRepository_AddToBlacklist(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		videoID := uuid.New()
		entryID := uuid.New()
		now := time.Now()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_blacklist`)).
			WithArgs(entryID, videoID, "spam", false, now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.AddToBlacklist(ctx, &domain.VideoBlacklist{
			ID: entryID, VideoID: videoID, Reason: "spam", Unfederated: false, CreatedAt: now,
		})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("generates ID if nil", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_blacklist`)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.AddToBlacklist(ctx, &domain.VideoBlacklist{
			VideoID: uuid.New(), Reason: "test",
		})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_blacklist`)).
			WillReturnError(errors.New("insert failed"))

		err := repo.AddToBlacklist(ctx, &domain.VideoBlacklist{VideoID: uuid.New()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "add to blacklist")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBlacklistRepository_RemoveFromBlacklist(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_blacklist WHERE video_id`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.RemoveFromBlacklist(ctx, videoID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_blacklist WHERE video_id`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.RemoveFromBlacklist(ctx, videoID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBlacklistRepository_GetByVideoID(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	t.Run("found", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		entryID := uuid.New()
		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, reason, unfederated, created_at FROM video_blacklist WHERE video_id`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows([]string{"id", "video_id", "reason", "unfederated", "created_at"}).
				AddRow(entryID, videoID, "spam", false, now))

		entry, err := repo.GetByVideoID(ctx, videoID)
		require.NoError(t, err)
		assert.Equal(t, entryID, entry.ID)
		assert.Equal(t, "spam", entry.Reason)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, reason, unfederated, created_at FROM video_blacklist WHERE video_id`)).
			WithArgs(videoID).
			WillReturnError(sql.ErrNoRows)

		_, err := repo.GetByVideoID(ctx, videoID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBlacklistRepository_List(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		videoID := uuid.New()
		entryID := uuid.New()
		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, reason, unfederated, created_at FROM video_blacklist ORDER BY`)).
			WithArgs(10, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id", "video_id", "reason", "unfederated", "created_at"}).
				AddRow(entryID, videoID, "spam", false, now))
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM video_blacklist`)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		entries, total, err := repo.List(ctx, 10, 0)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
		assert.Equal(t, 1, total)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select error", func(t *testing.T) {
		db, mock := setupBlacklistMockDB(t)
		defer db.Close()
		repo := NewBlacklistRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id`)).
			WillReturnError(errors.New("query error"))

		_, _, err := repo.List(ctx, 10, 0)
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
