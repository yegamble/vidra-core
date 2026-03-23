package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/usecase"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupUploadMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newUploadRepo(t *testing.T) (usecase.UploadRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupUploadMockDB(t)
	repo := NewUploadRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleUploadSession() domain.UploadSession {
	now := time.Now()
	return domain.UploadSession{
		ID:             "session-001",
		VideoID:        "video-001",
		UserID:         "user-001",
		FileName:       "test_video.mp4",
		FileSize:       1048576,
		ChunkSize:      10485,
		TotalChunks:    100,
		UploadedChunks: []int{0, 1, 2},
		Status:         domain.UploadStatusActive,
		TempFilePath:   "/tmp/test_session",
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
	}
}

// ---------- CreateSession ----------

func TestUploadRepository_Unit_CreateSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()

		mock.ExpectExec(regexp.QuoteMeta(
			`INSERT INTO upload_sessions (
			id, video_id, user_id, filename, file_size, chunk_size,
			total_chunks, uploaded_chunks, status, temp_file_path,
			created_at, updated_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`)).
			WithArgs(
				session.ID, session.VideoID, session.UserID, session.FileName,
				session.FileSize, session.ChunkSize, session.TotalChunks,
				pq.Array(session.UploadedChunks), session.Status, session.TempFilePath,
				session.CreatedAt, session.UpdatedAt, session.ExpiresAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateSession(ctx, &session)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()

		mock.ExpectExec(regexp.QuoteMeta(
			`INSERT INTO upload_sessions (
			id, video_id, user_id, filename, file_size, chunk_size,
			total_chunks, uploaded_chunks, status, temp_file_path,
			created_at, updated_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`)).
			WithArgs(
				session.ID, session.VideoID, session.UserID, session.FileName,
				session.FileSize, session.ChunkSize, session.TotalChunks,
				pq.Array(session.UploadedChunks), session.Status, session.TempFilePath,
				session.CreatedAt, session.UpdatedAt, session.ExpiresAt,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateSession(ctx, &session)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create upload session")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetSession ----------

func TestUploadRepository_Unit_GetSession(t *testing.T) {
	ctx := context.Background()

	selectQuery := `SELECT id, video_id, user_id, filename, file_size, chunk_size,
		       total_chunks, uploaded_chunks, status, temp_file_path,
		       created_at, updated_at, expires_at
		FROM upload_sessions WHERE id = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()
		chunks := pq.Int32Array{0, 1, 2}

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs(session.ID).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "video_id", "user_id", "filename", "file_size", "chunk_size",
				"total_chunks", "uploaded_chunks", "status", "temp_file_path",
				"created_at", "updated_at", "expires_at",
			}).AddRow(
				session.ID, session.VideoID, session.UserID, session.FileName,
				session.FileSize, session.ChunkSize, session.TotalChunks,
				chunks, session.Status, session.TempFilePath,
				session.CreatedAt, session.UpdatedAt, session.ExpiresAt,
			))

		got, err := repo.GetSession(ctx, session.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, session.ID, got.ID)
		assert.Equal(t, session.VideoID, got.VideoID)
		assert.Equal(t, session.FileName, got.FileName)
		assert.Equal(t, []int{0, 1, 2}, got.UploadedChunks)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("missing-session").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetSession(ctx, "missing-session")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SESSION_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(selectQuery)).
			WithArgs("session-x").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetSession(ctx, "session-x")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get upload session")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- UpdateSession ----------

func TestUploadRepository_Unit_UpdateSession(t *testing.T) {
	ctx := context.Background()

	updateQuery := `UPDATE upload_sessions SET
			uploaded_chunks = $2, status = $3, temp_file_path = $4,
			updated_at = $5
		WHERE id = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(
				session.ID, pq.Array(session.UploadedChunks), session.Status,
				session.TempFilePath, session.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateSession(ctx, &session)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(
				session.ID, pq.Array(session.UploadedChunks), session.Status,
				session.TempFilePath, session.UpdatedAt,
			).
			WillReturnError(errors.New("update failed"))

		err := repo.UpdateSession(ctx, &session)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update upload session")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(
				session.ID, pq.Array(session.UploadedChunks), session.Status,
				session.TempFilePath, session.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.UpdateSession(ctx, &session)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		session := sampleUploadSession()

		mock.ExpectExec(regexp.QuoteMeta(updateQuery)).
			WithArgs(
				session.ID, pq.Array(session.UploadedChunks), session.Status,
				session.TempFilePath, session.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateSession(ctx, &session)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SESSION_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- DeleteSession ----------

func TestUploadRepository_Unit_DeleteSession(t *testing.T) {
	ctx := context.Background()
	deleteQuery := `DELETE FROM upload_sessions WHERE id = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs("session-001").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteSession(ctx, "session-001")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs("session-001").
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteSession(ctx, "session-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete upload session")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs("session-001").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.DeleteSession(ctx, "session-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(deleteQuery)).
			WithArgs("session-001").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteSession(ctx, "session-001")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SESSION_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetUploadedChunks ----------

func TestUploadRepository_Unit_GetUploadedChunks(t *testing.T) {
	ctx := context.Background()
	chunkQuery := `SELECT uploaded_chunks FROM upload_sessions WHERE id = $1`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		chunks := pq.Int32Array{0, 1, 2}

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("session-001").
			WillReturnRows(sqlmock.NewRows([]string{"uploaded_chunks"}).AddRow(chunks))

		got, err := repo.GetUploadedChunks(ctx, "session-001")
		require.NoError(t, err)
		assert.Equal(t, []int{0, 1, 2}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty chunks", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		chunks := pq.Int32Array{}

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("session-001").
			WillReturnRows(sqlmock.NewRows([]string{"uploaded_chunks"}).AddRow(chunks))

		got, err := repo.GetUploadedChunks(ctx, "session-001")
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetUploadedChunks(ctx, "missing")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SESSION_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("session-001").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetUploadedChunks(ctx, "session-001")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get uploaded chunks")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- IsChunkUploaded ----------

func TestUploadRepository_Unit_IsChunkUploaded(t *testing.T) {
	ctx := context.Background()
	chunkQuery := `SELECT $2 = ANY(uploaded_chunks) FROM upload_sessions WHERE id = $1`

	t.Run("true", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("session-001", 0).
			WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(true))

		ok, err := repo.IsChunkUploaded(ctx, "session-001", 0)
		require.NoError(t, err)
		assert.True(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("false", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("session-001", 5).
			WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(false))

		ok, err := repo.IsChunkUploaded(ctx, "session-001", 5)
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("missing", 0).
			WillReturnError(sql.ErrNoRows)

		ok, err := repo.IsChunkUploaded(ctx, "missing", 0)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "SESSION_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(chunkQuery)).
			WithArgs("session-001", 0).
			WillReturnError(errors.New("db error"))

		ok, err := repo.IsChunkUploaded(ctx, "session-001", 0)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "failed to check chunk status")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- ExpireOldSessions ----------

func TestUploadRepository_Unit_ExpireOldSessions(t *testing.T) {
	ctx := context.Background()

	expireQuery := `UPDATE upload_sessions
		SET status = 'expired', updated_at = NOW()
		WHERE expires_at < NOW() AND status = 'active'`

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(expireQuery)).
			WillReturnResult(sqlmock.NewResult(0, 3))

		err := repo.ExpireOldSessions(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows affected is still success", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(expireQuery)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.ExpireOldSessions(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(expireQuery)).
			WillReturnError(errors.New("update failed"))

		err := repo.ExpireOldSessions(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to expire old sessions")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetExpiredSessions ----------

func TestUploadRepository_Unit_GetExpiredSessions(t *testing.T) {
	ctx := context.Background()

	expiredQuery := `SELECT id, video_id, user_id, filename, file_size, chunk_size,
		       total_chunks, uploaded_chunks, status, temp_file_path,
		       created_at, updated_at, expires_at
		FROM upload_sessions
		WHERE status = 'expired' OR (expires_at < NOW() AND status != 'completed')`

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		s := sampleUploadSession()
		s.Status = domain.UploadStatusExpired
		chunks := pq.Int32Array{0, 1, 2}

		mock.ExpectQuery(regexp.QuoteMeta(expiredQuery)).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "video_id", "user_id", "filename", "file_size", "chunk_size",
				"total_chunks", "uploaded_chunks", "status", "temp_file_path",
				"created_at", "updated_at", "expires_at",
			}).AddRow(
				s.ID, s.VideoID, s.UserID, s.FileName,
				s.FileSize, s.ChunkSize, s.TotalChunks,
				chunks, s.Status, s.TempFilePath,
				s.CreatedAt, s.UpdatedAt, s.ExpiresAt,
			))

		got, err := repo.GetExpiredSessions(ctx)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, s.ID, got[0].ID)
		assert.Equal(t, domain.UploadStatusExpired, got[0].Status)
		assert.Equal(t, []int{0, 1, 2}, got[0].UploadedChunks)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with no results", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(expiredQuery)).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "video_id", "user_id", "filename", "file_size", "chunk_size",
				"total_chunks", "uploaded_chunks", "status", "temp_file_path",
				"created_at", "updated_at", "expires_at",
			}))

		got, err := repo.GetExpiredSessions(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(expiredQuery)).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetExpiredSessions(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get expired sessions")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan failure", func(t *testing.T) {
		repo, mock, cleanup := newUploadRepo(t)
		defer cleanup()

		// Return a row with wrong column count to trigger scan error
		mock.ExpectQuery(regexp.QuoteMeta(expiredQuery)).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "video_id",
			}).AddRow("s1", "v1"))

		got, err := repo.GetExpiredSessions(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan expired session")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
