package rating

import (
	"context"
	"fmt"

	"athena/internal/domain"
	"athena/internal/port"

	"github.com/google/uuid"
)

// Service handles business logic for video ratings
type Service struct {
	ratingRepo port.RatingRepository
	videoRepo  port.VideoRepository
}

// NewService creates a new rating service
func NewService(ratingRepo port.RatingRepository, videoRepo port.VideoRepository) *Service {
	return &Service{ratingRepo: ratingRepo, videoRepo: videoRepo}
}

// SetRating sets or updates a user's rating for a video
func (s *Service) SetRating(ctx context.Context, userID, videoID uuid.UUID, rating domain.RatingValue) error {
	// Verify video exists
	if _, err := s.videoRepo.GetByID(ctx, videoID.String()); err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrNotFound
		}
		return fmt.Errorf("failed to verify video: %w", err)
	}

	// Validate rating value
	if !rating.IsValid() {
		return fmt.Errorf("invalid rating value: %d", rating)
	}

	// Set the rating (idempotent operation)
	if err := s.ratingRepo.SetRating(ctx, userID, videoID, rating); err != nil {
		return fmt.Errorf("failed to set rating: %w", err)
	}
	return nil
}

// GetRating gets a user's rating for a video
func (s *Service) GetRating(ctx context.Context, userID, videoID uuid.UUID) (domain.RatingValue, error) {
	rating, err := s.ratingRepo.GetRating(ctx, userID, videoID)
	if err != nil {
		return domain.RatingNone, fmt.Errorf("failed to get rating: %w", err)
	}
	return rating, nil
}

// RemoveRating removes a user's rating from a video
func (s *Service) RemoveRating(ctx context.Context, userID, videoID uuid.UUID) error {
	if err := s.ratingRepo.RemoveRating(ctx, userID, videoID); err != nil {
		return fmt.Errorf("failed to remove rating: %w", err)
	}
	return nil
}

// GetVideoRatingStats gets rating statistics for a video
func (s *Service) GetVideoRatingStats(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error) {
	stats, err := s.ratingRepo.GetVideoRatingStats(ctx, videoID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video rating stats: %w", err)
	}
	return stats, nil
}

// GetUserRatings gets all ratings by a user
func (s *Service) GetUserRatings(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
	// Set default and max limits
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	ratings, err := s.ratingRepo.GetUserRatings(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get user ratings: %w", err)
	}
	return ratings, nil
}
