package repository

import (
	"context"
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

func setupVideoPasswordMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newVideoPasswordRepo(t *testing.T) (port_VideoPasswordRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupVideoPasswordMockDB(t)
	repo := NewVideoPasswordRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

// port_VideoPasswordRepository is a local interface alias to avoid importing port package inline
type port_VideoPasswordRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoPassword, error)
	Create(ctx context.Context, videoID string, passwordHash string) (*domain.VideoPassword, error)
	ReplaceAll(ctx context.Context, videoID string, passwordHashes []string) ([]domain.VideoPassword, error)
	Delete(ctx context.Context, passwordID int64) error
}

func videoPasswordColumns() []string {
	return []string{"id", "video_id", "password_hash", "created_at"}
}

func TestVideoPasswordRepository_ListByVideoID(t *testing.T) {
	ctx := context.Background()
	videoID := "video-abc-123"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, password_hash, created_at
		 FROM video_passwords
		 WHERE video_id = $1
		 ORDER BY created_at ASC`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows(videoPasswordColumns()).
				AddRow(int64(1), videoID, "$2a$12$hash1", now).
				AddRow(int64(2), videoID, "$2a$12$hash2", now))

		passwords, err := repo.ListByVideoID(ctx, videoID)
		require.NoError(t, err)
		assert.Len(t, passwords, 2)
		assert.Equal(t, videoID, passwords[0].VideoID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, password_hash, created_at
		 FROM video_passwords
		 WHERE video_id = $1
		 ORDER BY created_at ASC`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows(videoPasswordColumns()))

		passwords, err := repo.ListByVideoID(ctx, videoID)
		require.NoError(t, err)
		assert.Empty(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, password_hash, created_at
		 FROM video_passwords
		 WHERE video_id = $1
		 ORDER BY created_at ASC`)).
			WithArgs(videoID).
			WillReturnError(errors.New("db error"))

		passwords, err := repo.ListByVideoID(ctx, videoID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list video passwords")
		assert.Nil(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoPasswordRepository_Create(t *testing.T) {
	ctx := context.Background()
	videoID := "video-create-123"
	hash := "$2a$12$testhash"

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO video_passwords (video_id, password_hash)
		 VALUES ($1, $2)
		 RETURNING id, video_id, password_hash, created_at`)).
			WithArgs(videoID, hash).
			WillReturnRows(sqlmock.NewRows(videoPasswordColumns()).
				AddRow(int64(5), videoID, hash, now))

		pw, err := repo.Create(ctx, videoID, hash)
		require.NoError(t, err)
		require.NotNil(t, pw)
		assert.Equal(t, int64(5), pw.ID)
		assert.Equal(t, videoID, pw.VideoID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO video_passwords (video_id, password_hash)
		 VALUES ($1, $2)
		 RETURNING id, video_id, password_hash, created_at`)).
			WithArgs(videoID, hash).
			WillReturnError(errors.New("unique violation"))

		pw, err := repo.Create(ctx, videoID, hash)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create video password")
		assert.Nil(t, pw)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoPasswordRepository_ReplaceAll(t *testing.T) {
	ctx := context.Background()
	videoID := "video-replace-123"

	t.Run("success with passwords", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		hashes := []string{"$2a$12$hash1", "$2a$12$hash2"}
		now := time.Now()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE video_id = $1`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`(?s)INSERT INTO video_passwords.*SELECT.*FROM UNNEST.*`).
			WillReturnRows(sqlmock.NewRows(videoPasswordColumns()).
				AddRow(int64(1), videoID, hashes[0], now).
				AddRow(int64(2), videoID, hashes[1], now))
		mock.ExpectCommit()

		passwords, err := repo.ReplaceAll(ctx, videoID, hashes)
		require.NoError(t, err)
		assert.Len(t, passwords, 2)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with empty list", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE video_id = $1`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 3))
		mock.ExpectCommit()

		passwords, err := repo.ReplaceAll(ctx, videoID, []string{})
		require.NoError(t, err)
		assert.Empty(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin tx error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectBegin().WillReturnError(errors.New("tx error"))

		passwords, err := repo.ReplaceAll(ctx, videoID, []string{"hash"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "begin transaction")
		assert.Nil(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE video_id = $1`)).
			WithArgs(videoID).
			WillReturnError(errors.New("delete error"))
		mock.ExpectRollback()

		passwords, err := repo.ReplaceAll(ctx, videoID, []string{"hash"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete existing passwords")
		assert.Nil(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE video_id = $1`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`(?s)INSERT INTO video_passwords.*SELECT.*FROM UNNEST.*`).
			WillReturnError(errors.New("insert error"))
		mock.ExpectRollback()

		passwords, err := repo.ReplaceAll(ctx, videoID, []string{"hash"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert video passwords")
		assert.Nil(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE video_id = $1`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`(?s)INSERT INTO video_passwords.*SELECT.*FROM UNNEST.*`).
			WillReturnRows(sqlmock.NewRows(videoPasswordColumns()).
				AddRow(int64(1), videoID, "$2a$12$hash1", time.Now()))
		mock.ExpectCommit().WillReturnError(errors.New("commit error"))

		passwords, err := repo.ReplaceAll(ctx, videoID, []string{"$2a$12$hash1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "commit video passwords")
		assert.Nil(t, passwords)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoPasswordRepository_Delete(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE id = $1`)).
			WithArgs(int64(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, 42)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE id = $1`)).
			WithArgs(int64(99)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, 99)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		repo, mock, cleanup := newVideoPasswordRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_passwords WHERE id = $1`)).
			WithArgs(int64(1)).
			WillReturnError(errors.New("db error"))

		err := repo.Delete(ctx, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete video password")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
