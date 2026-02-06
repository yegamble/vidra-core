package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCommentMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	return sqlxDB, mock
}

func TestCommentRepository_Unit_Create(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		videoID := uuid.New()
		userID := uuid.New()
		parentID := uuid.New()

		comment := &domain.Comment{
			VideoID:  videoID,
			UserID:   userID,
			ParentID: &parentID,
			Body:     "Test Comment",
		}

		query := `INSERT INTO comments (video_id, user_id, parent_id, body, status, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at, updated_at`

		rows := sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(uuid.New(), time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(query)).
			WithArgs(
				comment.VideoID,
				comment.UserID,
				comment.ParentID,
				comment.Body,
				domain.CommentStatusActive,
				sqlmock.AnyArg(), // created_at
				sqlmock.AnyArg(), // updated_at
			).
			WillReturnRows(rows)

		err := repo.Create(ctx, comment)
		require.NoError(t, err)
		assert.NotEmpty(t, comment.ID)
		assert.Equal(t, domain.CommentStatusActive, comment.Status)
		assert.Equal(t, 0, comment.FlagCount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DB Error", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		comment := &domain.Comment{
			VideoID: uuid.New(),
			UserID:  uuid.New(),
			Body:    "Test Comment",
		}

		query := `INSERT INTO comments`
		mock.ExpectQuery(regexp.QuoteMeta(query)).
			WillReturnError(sql.ErrConnDone)

		err := repo.Create(ctx, comment)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create comment")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_GetByID(t *testing.T) {
	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	t.Run("Found", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "user_id", "parent_id", "body", "status", "flag_count",
			"edited_at", "created_at", "updated_at",
		}).AddRow(
			commentID, videoID, userID, nil, "Test Body", domain.CommentStatusActive, 0,
			nil, now, now,
		)

		query := `SELECT id, video_id, user_id, parent_id, body, status, flag_count, edited_at, created_at, updated_at FROM comments WHERE id = $1`
		mock.ExpectQuery(regexp.QuoteMeta(query)).
			WithArgs(commentID).
			WillReturnRows(rows)

		result, err := repo.GetByID(ctx, commentID)
		require.NoError(t, err)
		assert.Equal(t, commentID, result.ID)
		assert.Equal(t, "Test Body", result.Body)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Not Found", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		query := `SELECT id, video_id, user_id, parent_id, body, status, flag_count, edited_at, created_at, updated_at FROM comments WHERE id = $1`
		mock.ExpectQuery(regexp.QuoteMeta(query)).
			WithArgs(commentID).
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetByID(ctx, commentID)
		require.Error(t, err)
		assert.Equal(t, domain.ErrNotFound, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_ListByVideo(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	t.Run("List Root Comments", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		opts := domain.CommentListOptions{
			VideoID: videoID,
			Limit:   10,
			Offset:  0,
			OrderBy: "newest",
		}

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "user_id", "parent_id", "body", "status",
			"flag_count", "edited_at", "created_at", "updated_at",
			"username", "avatar",
		}).AddRow(
			uuid.New(), videoID, userID, nil, "Comment 1", domain.CommentStatusActive,
			0, nil, time.Now(), time.Now(),
			"user1", "avatar1",
		)

		query := `SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status, c.flag_count, c.edited_at, c.created_at, c.updated_at, u.username, ua.webp_ipfs_cid as avatar FROM comments c JOIN users u ON c.user_id = u.id LEFT JOIN user_avatars ua ON u.id = ua.user_id WHERE c.video_id = $1 AND c.status = 'active' AND c.parent_id IS NULL ORDER BY c.created_at DESC LIMIT $2 OFFSET $3`

		mock.ExpectQuery(regexp.QuoteMeta(query)).
			WithArgs(videoID, opts.Limit, opts.Offset).
			WillReturnRows(rows)

		results, err := repo.ListByVideo(ctx, opts)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "user1", results[0].Username)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("List Replies", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		parentID := uuid.New()
		opts := domain.CommentListOptions{
			VideoID:  videoID,
			ParentID: &parentID,
			Limit:    5,
			Offset:   0,
		}

		query := `SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status, c.flag_count, c.edited_at, c.created_at, c.updated_at, u.username, ua.webp_ipfs_cid as avatar FROM comments c JOIN users u ON c.user_id = u.id LEFT JOIN user_avatars ua ON u.id = ua.user_id WHERE c.video_id = $1 AND c.status = 'active' AND c.parent_id = $2 ORDER BY c.created_at DESC LIMIT $3 OFFSET $4`

		mock.ExpectQuery(regexp.QuoteMeta(query)).
			WithArgs(videoID, parentID, opts.Limit, opts.Offset).
			WillReturnRows(sqlmock.NewRows(nil))

		results, err := repo.ListByVideo(ctx, opts)
		require.NoError(t, err)
		assert.Len(t, results, 0)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_Delete(t *testing.T) {
	commentID := uuid.New()

	t.Run("Success", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		query := `UPDATE comments SET status = 'deleted', updated_at = $1 WHERE id = $2 AND status = 'active'`

		mock.ExpectExec(regexp.QuoteMeta(query)).
			WithArgs(sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, commentID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Not Found", func(t *testing.T) {
		db, mock := setupCommentMockDB(t)
		defer db.Close()
		repo := NewCommentRepository(db)
		ctx := context.Background()

		query := `UPDATE comments SET status = 'deleted', updated_at = $1 WHERE id = $2 AND status = 'active'`

		mock.ExpectExec(regexp.QuoteMeta(query)).
			WithArgs(sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, commentID)
		require.Error(t, err)
		assert.Equal(t, domain.ErrNotFound, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
