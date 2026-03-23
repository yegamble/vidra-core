package port

import (
	"vidra-core/internal/domain"
	"context"
)

// VideoStoryboardRepository defines data operations for video storyboards.
type VideoStoryboardRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error)
}
