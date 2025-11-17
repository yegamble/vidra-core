package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ratingRepository struct {
	db *sqlx.DB
	tm *TransactionManager
}

func NewRatingRepository(db *sqlx.DB) usecase.RatingRepository {
	return &ratingRepository{
		db: db,
		tm: NewTransactionManager(db),
	}
}

// SetRating sets or updates a user's rating for a video (idempotent)
func (r *ratingRepository) SetRating(ctx context.Context, userID, videoID uuid.UUID, rating domain.RatingValue) error {
	if !rating.IsValid() {
		return fmt.Errorf("invalid rating value: %d", rating)
	}

	// Get executor (either transaction from context or DB)
	exec := GetExecutor(ctx, r.db)

	query := `
		INSERT INTO video_ratings (user_id, video_id, rating, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (user_id, video_id)
		DO UPDATE SET rating = $3, updated_at = $4
		WHERE video_ratings.rating != $3`

	_, err := exec.ExecContext(ctx, query, userID, videoID, rating, time.Now())
	if err != nil {
		return fmt.Errorf("failed to set rating: %w", err)
	}

	return nil
}

// GetRating gets a user's rating for a video
func (r *ratingRepository) GetRating(ctx context.Context, userID, videoID uuid.UUID) (domain.RatingValue, error) {
	var rating domain.RatingValue
	query := `SELECT rating FROM video_ratings WHERE user_id = $1 AND video_id = $2`

	err := r.db.GetContext(ctx, &rating, query, userID, videoID)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.RatingNone, nil // No rating is treated as none/neutral
		}
		return domain.RatingNone, fmt.Errorf("failed to get rating: %w", err)
	}

	return rating, nil
}

// RemoveRating removes a user's rating for a video
func (r *ratingRepository) RemoveRating(ctx context.Context, userID, videoID uuid.UUID) error {
	query := `DELETE FROM video_ratings WHERE user_id = $1 AND video_id = $2`

	result, err := r.db.ExecContext(ctx, query, userID, videoID)
	if err != nil {
		return fmt.Errorf("failed to remove rating: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// No rating to remove, but this is not an error (idempotent)
		return nil
	}

	return nil
}

// GetVideoRatingStats gets aggregated rating statistics for a video
func (r *ratingRepository) GetVideoRatingStats(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error) {
	stats := &domain.VideoRatingStats{
		VideoID:    videoID,
		UserRating: domain.RatingNone,
	}

	// Get aggregated counts from the videos table (maintained by trigger)
	query := `SELECT likes_count, dislikes_count FROM videos WHERE id = $1`
	err := r.db.QueryRowContext(ctx, query, videoID).Scan(&stats.LikesCount, &stats.DislikesCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get video rating stats: %w", err)
	}

	// Get user's rating if userID is provided
	if userID != nil {
		rating, err := r.GetRating(ctx, *userID, videoID)
		if err != nil {
			// Log error but don't fail the whole request
			fmt.Printf("failed to get user rating: %v\n", err)
		} else {
			stats.UserRating = rating
		}
	}

	return stats, nil
}

// GetUserRatings gets all ratings by a user
func (r *ratingRepository) GetUserRatings(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
	ratings := []*domain.VideoRating{}

	query := `
		SELECT user_id, video_id, rating, created_at, updated_at
		FROM video_ratings
		WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &ratings, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get user ratings: %w", err)
	}

	return ratings, nil
}

// GetVideoRatings gets all ratings for a video (mainly for analytics)
func (r *ratingRepository) GetVideoRatings(ctx context.Context, videoID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
	ratings := []*domain.VideoRating{}

	query := `
		SELECT user_id, video_id, rating, created_at, updated_at
		FROM video_ratings
		WHERE video_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &ratings, query, videoID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get video ratings: %w", err)
	}

	return ratings, nil
}

// BatchGetVideoStats gets rating stats for multiple videos (efficient for lists)
func (r *ratingRepository) BatchGetVideoStats(ctx context.Context, videoIDs []uuid.UUID, userID *uuid.UUID) (map[uuid.UUID]*domain.VideoRatingStats, error) {
	if len(videoIDs) == 0 {
		return make(map[uuid.UUID]*domain.VideoRatingStats), nil
	}

	statsMap := make(map[uuid.UUID]*domain.VideoRatingStats)

	// Get aggregated counts for all videos
	query := `SELECT id, likes_count, dislikes_count FROM videos WHERE id = ANY($1)`
	rows, err := r.db.QueryContext(ctx, query, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get batch video stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var videoID uuid.UUID
		stats := &domain.VideoRatingStats{UserRating: domain.RatingNone}

		err := rows.Scan(&videoID, &stats.LikesCount, &stats.DislikesCount)
		if err != nil {
			return nil, fmt.Errorf("failed to scan video stats: %w", err)
		}

		stats.VideoID = videoID
		statsMap[videoID] = stats
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating video stats: %w", err)
	}

	// Get user ratings if userID is provided
	if userID != nil && len(videoIDs) > 0 {
		userRatingsQuery := `
			SELECT video_id, rating
			FROM video_ratings
			WHERE user_id = $1 AND video_id = ANY($2)`

		userRows, err := r.db.QueryContext(ctx, userRatingsQuery, *userID, videoIDs)
		if err != nil {
			// Log error but don't fail the whole request
			fmt.Printf("failed to get user ratings batch: %v\n", err)
		} else {
			defer func() { _ = userRows.Close() }()

			for userRows.Next() {
				var videoID uuid.UUID
				var rating domain.RatingValue

				if err := userRows.Scan(&videoID, &rating); err != nil {
					continue
				}

				if stats, exists := statsMap[videoID]; exists {
					stats.UserRating = rating
				}
			}

			if err := userRows.Err(); err != nil {
				fmt.Printf("error iterating user ratings: %v\n", err)
			}
		}
	}

	return statsMap, nil
}
