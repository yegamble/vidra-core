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

func setupRatingMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

// setupRatingMockDBWithAnyValues creates a mock that accepts any driver.Value
// (needed when passing []uuid.UUID slices to QueryContext).
func setupRatingMockDBWithAnyValues(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New(sqlmock.ValueConverterOption(anyValueConverter{}))
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newRatingRepo(t *testing.T) (*ratingRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupRatingMockDB(t)
	repo := NewRatingRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo.(*ratingRepository), mock, cleanup
}

func newRatingRepoWithAnyValues(t *testing.T) (*ratingRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupRatingMockDBWithAnyValues(t)
	repo := NewRatingRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo.(*ratingRepository), mock, cleanup
}

// ---------------------------------------------------------------------------
// SetRating
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_SetRating(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	videoID := uuid.New()

	t.Run("success like", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)INSERT INTO video_ratings`).
			WithArgs(userID, videoID, domain.RatingLike, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetRating(ctx, userID, videoID, domain.RatingLike)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success dislike", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)INSERT INTO video_ratings`).
			WithArgs(userID, videoID, domain.RatingDislike, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetRating(ctx, userID, videoID, domain.RatingDislike)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid rating value", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		err := repo.SetRating(ctx, userID, videoID, domain.RatingValue(5))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid rating value")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)INSERT INTO video_ratings`).
			WithArgs(userID, videoID, domain.RatingLike, sqlmock.AnyArg()).
			WillReturnError(errors.New("insert failed"))

		err := repo.SetRating(ctx, userID, videoID, domain.RatingLike)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to set rating")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetRating
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_GetRating(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	videoID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT rating FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnRows(sqlmock.NewRows([]string{"rating"}).AddRow(domain.RatingLike))

		rating, err := repo.GetRating(ctx, userID, videoID)
		require.NoError(t, err)
		assert.Equal(t, domain.RatingLike, rating)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns RatingNone", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT rating FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnError(sql.ErrNoRows)

		rating, err := repo.GetRating(ctx, userID, videoID)
		require.NoError(t, err)
		assert.Equal(t, domain.RatingNone, rating)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT rating FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnError(errors.New("db down"))

		rating, err := repo.GetRating(ctx, userID, videoID)
		require.Error(t, err)
		assert.Equal(t, domain.RatingNone, rating)
		assert.Contains(t, err.Error(), "failed to get rating")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// RemoveRating
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_RemoveRating(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	videoID := uuid.New()

	t.Run("success row deleted", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.RemoveRating(ctx, userID, videoID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("idempotent no rows affected", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.RemoveRating(ctx, userID, videoID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnError(errors.New("delete failed"))

		err := repo.RemoveRating(ctx, userID, videoID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove rating")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.RemoveRating(ctx, userID, videoID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetVideoRatingStats
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_GetVideoRatingStats(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()

	t.Run("success without user", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT likes_count, dislikes_count FROM videos WHERE id = $1`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows([]string{"likes_count", "dislikes_count"}).AddRow(10, 3))

		stats, err := repo.GetVideoRatingStats(ctx, videoID, nil)
		require.NoError(t, err)
		require.NotNil(t, stats)
		assert.Equal(t, videoID, stats.VideoID)
		assert.Equal(t, 10, stats.LikesCount)
		assert.Equal(t, 3, stats.DislikesCount)
		assert.Equal(t, domain.RatingNone, stats.UserRating)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with user rating", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT likes_count, dislikes_count FROM videos WHERE id = $1`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows([]string{"likes_count", "dislikes_count"}).AddRow(5, 2))

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT rating FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnRows(sqlmock.NewRows([]string{"rating"}).AddRow(domain.RatingLike))

		stats, err := repo.GetVideoRatingStats(ctx, videoID, &userID)
		require.NoError(t, err)
		require.NotNil(t, stats)
		assert.Equal(t, 5, stats.LikesCount)
		assert.Equal(t, 2, stats.DislikesCount)
		assert.Equal(t, domain.RatingLike, stats.UserRating)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("video not found", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT likes_count, dislikes_count FROM videos WHERE id = $1`)).
			WithArgs(videoID).
			WillReturnError(sql.ErrNoRows)

		stats, err := repo.GetVideoRatingStats(ctx, videoID, nil)
		require.Nil(t, stats)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT likes_count, dislikes_count FROM videos WHERE id = $1`)).
			WithArgs(videoID).
			WillReturnError(errors.New("db error"))

		stats, err := repo.GetVideoRatingStats(ctx, videoID, nil)
		require.Nil(t, stats)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get video rating stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user rating query failure is non-fatal", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT likes_count, dislikes_count FROM videos WHERE id = $1`)).
			WithArgs(videoID).
			WillReturnRows(sqlmock.NewRows([]string{"likes_count", "dislikes_count"}).AddRow(1, 0))

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT rating FROM video_ratings WHERE user_id = $1 AND video_id = $2`)).
			WithArgs(userID, videoID).
			WillReturnError(errors.New("user rating lookup failed"))

		stats, err := repo.GetVideoRatingStats(ctx, videoID, &userID)
		require.NoError(t, err)
		require.NotNil(t, stats)
		assert.Equal(t, domain.RatingNone, stats.UserRating)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetUserRatings
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_GetUserRatings(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		videoID1 := uuid.New()
		videoID2 := uuid.New()

		rows := sqlmock.NewRows([]string{"user_id", "video_id", "rating", "created_at", "updated_at"}).
			AddRow(userID, videoID1, domain.RatingLike, now, now).
			AddRow(userID, videoID2, domain.RatingDislike, now, now)

		mock.ExpectQuery(`(?s)SELECT user_id, video_id, rating, created_at, updated_at.*FROM video_ratings.*WHERE user_id = \$1`).
			WithArgs(userID, 10, 0).
			WillReturnRows(rows)

		ratings, err := repo.GetUserRatings(ctx, userID, 10, 0)
		require.NoError(t, err)
		require.Len(t, ratings, 2)
		assert.Equal(t, domain.RatingLike, ratings[0].Rating)
		assert.Equal(t, domain.RatingDislike, ratings[1].Rating)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"user_id", "video_id", "rating", "created_at", "updated_at"})

		mock.ExpectQuery(`(?s)SELECT user_id, video_id, rating, created_at, updated_at.*FROM video_ratings.*WHERE user_id = \$1`).
			WithArgs(userID, 10, 0).
			WillReturnRows(rows)

		ratings, err := repo.GetUserRatings(ctx, userID, 10, 0)
		require.NoError(t, err)
		require.Empty(t, ratings)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT user_id, video_id, rating, created_at, updated_at.*FROM video_ratings.*WHERE user_id = \$1`).
			WithArgs(userID, 10, 0).
			WillReturnError(errors.New("query failed"))

		ratings, err := repo.GetUserRatings(ctx, userID, 10, 0)
		require.Nil(t, ratings)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user ratings")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetVideoRatings
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_GetVideoRatings(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		userID1 := uuid.New()
		userID2 := uuid.New()

		rows := sqlmock.NewRows([]string{"user_id", "video_id", "rating", "created_at", "updated_at"}).
			AddRow(userID1, videoID, domain.RatingLike, now, now).
			AddRow(userID2, videoID, domain.RatingDislike, now, now)

		mock.ExpectQuery(`(?s)SELECT user_id, video_id, rating, created_at, updated_at.*FROM video_ratings.*WHERE video_id = \$1`).
			WithArgs(videoID, 20, 0).
			WillReturnRows(rows)

		ratings, err := repo.GetVideoRatings(ctx, videoID, 20, 0)
		require.NoError(t, err)
		require.Len(t, ratings, 2)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT user_id, video_id, rating, created_at, updated_at.*FROM video_ratings.*WHERE video_id = \$1`).
			WithArgs(videoID, 20, 0).
			WillReturnError(errors.New("query failed"))

		ratings, err := repo.GetVideoRatings(ctx, videoID, 20, 0)
		require.Nil(t, ratings)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get video ratings")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// BatchGetVideoStats
// ---------------------------------------------------------------------------

func TestRatingRepository_Unit_BatchGetVideoStats(t *testing.T) {
	ctx := context.Background()

	t.Run("empty videoIDs returns empty map", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepo(t)
		defer cleanup()

		result, err := repo.BatchGetVideoStats(ctx, []uuid.UUID{}, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Empty(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success without user", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepoWithAnyValues(t)
		defer cleanup()

		vid1 := uuid.New()
		vid2 := uuid.New()
		videoIDs := []uuid.UUID{vid1, vid2}

		rows := sqlmock.NewRows([]string{"id", "likes_count", "dislikes_count"}).
			AddRow(vid1, 10, 2).
			AddRow(vid2, 5, 1)

		mock.ExpectQuery(`(?s)SELECT id, likes_count, dislikes_count FROM videos WHERE id = ANY`).
			WithArgs(videoIDs).
			WillReturnRows(rows)

		result, err := repo.BatchGetVideoStats(ctx, videoIDs, nil)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Equal(t, 10, result[vid1].LikesCount)
		assert.Equal(t, 2, result[vid1].DislikesCount)
		assert.Equal(t, domain.RatingNone, result[vid1].UserRating)
		assert.Equal(t, 5, result[vid2].LikesCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with user ratings", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepoWithAnyValues(t)
		defer cleanup()

		vid1 := uuid.New()
		userID := uuid.New()
		videoIDs := []uuid.UUID{vid1}

		statsRows := sqlmock.NewRows([]string{"id", "likes_count", "dislikes_count"}).
			AddRow(vid1, 7, 0)

		mock.ExpectQuery(`(?s)SELECT id, likes_count, dislikes_count FROM videos WHERE id = ANY`).
			WithArgs(videoIDs).
			WillReturnRows(statsRows)

		userRows := sqlmock.NewRows([]string{"video_id", "rating"}).
			AddRow(vid1, domain.RatingLike)

		mock.ExpectQuery(`(?s)SELECT video_id, rating.*FROM video_ratings.*WHERE user_id = .+ AND video_id = ANY`).
			WithArgs(userID, videoIDs).
			WillReturnRows(userRows)

		result, err := repo.BatchGetVideoStats(ctx, videoIDs, &userID)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, domain.RatingLike, result[vid1].UserRating)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("stats query failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepoWithAnyValues(t)
		defer cleanup()

		videoIDs := []uuid.UUID{uuid.New()}

		mock.ExpectQuery(`(?s)SELECT id, likes_count, dislikes_count FROM videos WHERE id = ANY`).
			WithArgs(videoIDs).
			WillReturnError(errors.New("batch query failed"))

		result, err := repo.BatchGetVideoStats(ctx, videoIDs, nil)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get batch video stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan failure", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepoWithAnyValues(t)
		defer cleanup()

		videoIDs := []uuid.UUID{uuid.New()}

		// Return a row with a value that cannot be scanned into uuid.UUID
		rows := sqlmock.NewRows([]string{"id", "likes_count", "dislikes_count"}).
			AddRow("not-a-uuid", "not-an-int", 0)

		mock.ExpectQuery(`(?s)SELECT id, likes_count, dislikes_count FROM videos WHERE id = ANY`).
			WithArgs(videoIDs).
			WillReturnRows(rows)

		result, err := repo.BatchGetVideoStats(ctx, videoIDs, nil)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan video stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user ratings query failure is non-fatal", func(t *testing.T) {
		repo, mock, cleanup := newRatingRepoWithAnyValues(t)
		defer cleanup()

		vid1 := uuid.New()
		userID := uuid.New()
		videoIDs := []uuid.UUID{vid1}

		statsRows := sqlmock.NewRows([]string{"id", "likes_count", "dislikes_count"}).
			AddRow(vid1, 3, 1)

		mock.ExpectQuery(`(?s)SELECT id, likes_count, dislikes_count FROM videos WHERE id = ANY`).
			WithArgs(videoIDs).
			WillReturnRows(statsRows)

		mock.ExpectQuery(`(?s)SELECT video_id, rating.*FROM video_ratings.*WHERE user_id = .+ AND video_id = ANY`).
			WithArgs(userID, videoIDs).
			WillReturnError(errors.New("user ratings batch failed"))

		result, err := repo.BatchGetVideoStats(ctx, videoIDs, &userID)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, domain.RatingNone, result[vid1].UserRating)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
