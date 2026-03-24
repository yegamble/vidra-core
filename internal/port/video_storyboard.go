package port

import (
	"context"
	"vidra-core/internal/domain"
)

// VideoStoryboardRepository defines data operations for video storyboards.
type VideoStoryboardRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error)
}
