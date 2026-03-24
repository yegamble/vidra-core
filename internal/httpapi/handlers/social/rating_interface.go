package social

import (
	"context"
	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type RatingServiceInterface interface {
	SetRating(ctx context.Context, userID uuid.UUID, videoID uuid.UUID, rating domain.RatingValue) error
	GetVideoRatingStats(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error)
	RemoveRating(ctx context.Context, userID uuid.UUID, videoID uuid.UUID) error
	GetUserRatings(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error)
}
