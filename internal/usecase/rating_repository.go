package usecase

import (
	"athena/internal/domain"
	"context"

	"github.com/google/uuid"
)

// RatingRepository defines the interface for rating data operations
type RatingRepository interface {
	SetRating(ctx context.Context, userID, videoID uuid.UUID, rating domain.RatingValue) error
	GetRating(ctx context.Context, userID, videoID uuid.UUID) (domain.RatingValue, error)
	RemoveRating(ctx context.Context, userID, videoID uuid.UUID) error
	GetVideoRatingStats(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error)
	GetUserRatings(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error)
	GetVideoRatings(ctx context.Context, videoID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error)
	BatchGetVideoStats(ctx context.Context, videoIDs []uuid.UUID, userID *uuid.UUID) (map[uuid.UUID]*domain.VideoRatingStats, error)
}
