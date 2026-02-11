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
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCommentMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newCommentRepo(t *testing.T) (*commentRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupCommentMockDB(t)
	repo := NewCommentRepository(db).(*commentRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func makeCommentRows(commentID, videoID, userID uuid.UUID, parentID *uuid.UUID, now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "video_id", "user_id", "parent_id", "body", "status", "flag_count",
		"edited_at", "created_at", "updated_at",
	}).AddRow(
		commentID, videoID, userID, parentID, "comment body", string(domain.CommentStatusActive), 0,
		nil, now, now,
	)
}

func makeCommentWithUserRows(commentID, videoID, userID uuid.UUID, parentID *uuid.UUID, now time.Time) *sqlmock.Rows {
	avatar := "bafy-avatar"
	return sqlmock.NewRows([]string{
		"id", "video_id", "user_id", "parent_id", "body", "status",
		"flag_count", "edited_at", "created_at", "updated_at",
		"username", "avatar",
	}).AddRow(
		commentID, videoID, userID, parentID, "comment body", string(domain.CommentStatusActive),
		0, nil, now, now,
		"unit-user", avatar,
	)
}

func makeCommentFlagRows(flagID, commentID, userID uuid.UUID, reason domain.CommentFlagReason, now time.Time) *sqlmock.Rows {
	details := "reason details"
	return sqlmock.NewRows([]string{
		"id", "comment_id", "user_id", "reason", "details", "created_at",
	}).AddRow(flagID, commentID, userID, string(reason), details, now)
}

func TestCommentRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		now := time.Now()
		commentID := uuid.New()
		videoID := uuid.New()
		userID := uuid.New()
		comment := &domain.Comment{
			VideoID: videoID,
			UserID:  userID,
			Body:    "hello",
		}

		mock.ExpectQuery(`(?s)INSERT INTO comments`).
			WithArgs(
				comment.VideoID,
				comment.UserID,
				comment.ParentID,
				comment.Body,
				domain.CommentStatusActive,
				sqlmock.AnyArg(),
				sqlmock.AnyArg(),
			).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(commentID, now, now))

		err := repo.Create(ctx, comment)
		require.NoError(t, err)
		assert.Equal(t, commentID, comment.ID)
		assert.Equal(t, domain.CommentStatusActive, comment.Status)
		assert.Equal(t, 0, comment.FlagCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		comment := &domain.Comment{VideoID: uuid.New(), UserID: uuid.New(), Body: "hello"}
		mock.ExpectQuery(`(?s)INSERT INTO comments`).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, comment)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create comment")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_Getters(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()

	t.Run("get by id success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, video_id, user_id, parent_id, body, status, flag_count`).
			WithArgs(commentID).
			WillReturnRows(makeCommentRows(commentID, videoID, userID, nil, now))

		got, err := repo.GetByID(ctx, commentID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, commentID, got.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by id not found", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, video_id, user_id, parent_id, body, status, flag_count`).
			WithArgs(commentID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByID(ctx, commentID)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by id query failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, video_id, user_id, parent_id, body, status, flag_count`).
			WithArgs(commentID).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetByID(ctx, commentID)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get comment")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by id with user success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(commentID).
			WillReturnRows(makeCommentWithUserRows(commentID, videoID, userID, nil, now))

		got, err := repo.GetByIDWithUser(ctx, commentID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "unit-user", got.Username)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by id with user not found", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(commentID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByIDWithUser(ctx, commentID)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_UpdateDelete(t *testing.T) {
	ctx := context.Background()
	commentID := uuid.New()

	t.Run("update success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET body = \$1, edited_at = \$2, updated_at = \$3`).
			WithArgs("updated body", sqlmock.AnyArg(), sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, commentID, "updated body")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update exec failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET body = \$1, edited_at = \$2, updated_at = \$3`).
			WillReturnError(errors.New("update failed"))

		err := repo.Update(ctx, commentID, "updated body")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update comment")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET body = \$1, edited_at = \$2, updated_at = \$3`).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Update(ctx, commentID, "updated body")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update not found", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET body = \$1, edited_at = \$2, updated_at = \$3`).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(ctx, commentID, "updated body")
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET status = 'deleted', updated_at = \$1`).
			WithArgs(sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, commentID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete not found", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET status = 'deleted', updated_at = \$1`).
			WithArgs(sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, commentID)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_Listing(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	videoID := uuid.New()
	parentID := uuid.New()

	t.Run("list by video top-level success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(videoID, 10, 0).
			WillReturnRows(makeCommentWithUserRows(uuid.New(), videoID, uuid.New(), nil, now))

		comments, err := repo.ListByVideo(ctx, domain.CommentListOptions{
			VideoID: videoID,
			Limit:   10,
			Offset:  0,
			OrderBy: "newest",
		})
		require.NoError(t, err)
		require.Len(t, comments, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list by video replies success", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(videoID, parentID, 5, 2).
			WillReturnRows(makeCommentWithUserRows(uuid.New(), videoID, uuid.New(), &parentID, now))

		comments, err := repo.ListByVideo(ctx, domain.CommentListOptions{
			VideoID:  videoID,
			ParentID: &parentID,
			Limit:    5,
			Offset:   2,
			OrderBy:  "oldest",
		})
		require.NoError(t, err)
		require.Len(t, comments, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list by video query failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(videoID, 20, 0).
			WillReturnError(errors.New("select failed"))

		comments, err := repo.ListByVideo(ctx, domain.CommentListOptions{
			VideoID: videoID,
			Limit:   20,
			Offset:  0,
		})
		require.Nil(t, comments)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list comments")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list replies success and failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(parentID, 10, 0).
			WillReturnRows(makeCommentWithUserRows(uuid.New(), videoID, uuid.New(), &parentID, now))

		replies, err := repo.ListReplies(ctx, parentID, 10, 0)
		require.NoError(t, err)
		require.Len(t, replies, 1)

		mock.ExpectQuery(`(?s)SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status`).
			WithArgs(parentID, 10, 0).
			WillReturnError(errors.New("select failed"))

		replies, err = repo.ListReplies(ctx, parentID, 10, 0)
		require.Nil(t, replies)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list replies")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count by video activeOnly true/false and error", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM comments WHERE video_id = $1 AND status = 'active'`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

		count, err := repo.CountByVideo(ctx, videoID, true)
		require.NoError(t, err)
		assert.Equal(t, 3, count)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM comments WHERE video_id = $1`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

		count, err = repo.CountByVideo(ctx, videoID, false)
		require.NoError(t, err)
		assert.Equal(t, 5, count)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM comments WHERE video_id = $1 AND status = 'active'`)).
			WithArgs(videoID).
			WillReturnError(errors.New("count failed"))

		count, err = repo.CountByVideo(ctx, videoID, true)
		require.Error(t, err)
		assert.Equal(t, 0, count)
		assert.Contains(t, err.Error(), "failed to count comments")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCommentRepository_Unit_FlagsStatusOwner(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	commentID := uuid.New()
	userID := uuid.New()

	t.Run("flag success and failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		flagID := uuid.New()
		details := "spam details"
		flag := &domain.CommentFlag{
			CommentID: commentID,
			UserID:    userID,
			Reason:    domain.FlagReasonSpam,
			Details:   &details,
		}

		mock.ExpectQuery(`(?s)INSERT INTO comment_flags`).
			WithArgs(
				flag.CommentID,
				flag.UserID,
				flag.Reason,
				flag.Details,
				sqlmock.AnyArg(),
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(flagID))

		err := repo.FlagComment(ctx, flag)
		require.NoError(t, err)
		assert.Equal(t, flagID, flag.ID)
		assert.False(t, flag.CreatedAt.IsZero())

		flag2 := &domain.CommentFlag{CommentID: commentID, UserID: userID, Reason: domain.FlagReasonSpam}
		mock.ExpectQuery(`(?s)INSERT INTO comment_flags`).
			WillReturnError(errors.New("insert failed"))

		err = repo.FlagComment(ctx, flag2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to flag comment")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unflag success and error branches", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM comment_flags WHERE comment_id = $1 AND user_id = $2`)).
			WithArgs(commentID, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.UnflagComment(ctx, commentID, userID))

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM comment_flags WHERE comment_id = $1 AND user_id = $2`)).
			WithArgs(commentID, userID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))
		err := repo.UnflagComment(ctx, commentID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM comment_flags WHERE comment_id = $1 AND user_id = $2`)).
			WithArgs(commentID, userID).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err = repo.UnflagComment(ctx, commentID, userID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM comment_flags WHERE comment_id = $1 AND user_id = $2`)).
			WithArgs(commentID, userID).
			WillReturnError(errors.New("delete failed"))
		err = repo.UnflagComment(ctx, commentID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unflag comment")

		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get flags success and failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		flagRows := makeCommentFlagRows(uuid.New(), commentID, userID, domain.FlagReasonOther, now)
		mock.ExpectQuery(`(?s)SELECT id, comment_id, user_id, reason, details, created_at`).
			WithArgs(commentID).
			WillReturnRows(flagRows)

		flags, err := repo.GetFlags(ctx, commentID)
		require.NoError(t, err)
		require.Len(t, flags, 1)

		mock.ExpectQuery(`(?s)SELECT id, comment_id, user_id, reason, details, created_at`).
			WithArgs(commentID).
			WillReturnError(errors.New("select failed"))
		flags, err = repo.GetFlags(ctx, commentID)
		require.Nil(t, flags)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get comment flags")

		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update status success and branches", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE comments\s+SET status = \$1, updated_at = \$2`).
			WithArgs(domain.CommentStatusHidden, sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.UpdateStatus(ctx, commentID, domain.CommentStatusHidden))

		mock.ExpectExec(`(?s)UPDATE comments\s+SET status = \$1, updated_at = \$2`).
			WithArgs(domain.CommentStatusHidden, sqlmock.AnyArg(), commentID).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err := repo.UpdateStatus(ctx, commentID, domain.CommentStatusHidden)
		require.ErrorIs(t, err, domain.ErrNotFound)

		mock.ExpectExec(`(?s)UPDATE comments\s+SET status = \$1, updated_at = \$2`).
			WithArgs(domain.CommentStatusHidden, sqlmock.AnyArg(), commentID).
			WillReturnError(errors.New("update failed"))
		err = repo.UpdateStatus(ctx, commentID, domain.CommentStatusHidden)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update comment status")

		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is owner success and failure", func(t *testing.T) {
		repo, mock, cleanup := newCommentRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM comments WHERE id = $1 AND user_id = $2)`)).
			WithArgs(commentID, userID).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		ok, err := repo.IsOwner(ctx, commentID, userID)
		require.NoError(t, err)
		assert.True(t, ok)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM comments WHERE id = $1 AND user_id = $2)`)).
			WithArgs(commentID, userID).
			WillReturnError(errors.New("query failed"))
		ok, err = repo.IsOwner(ctx, commentID, userID)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "failed to check comment ownership")

		require.NoError(t, mock.ExpectationsWereMet())
	})
}
