package port

import (
	"athena/internal/domain"
	"context"
)

// VideoStoryboardRepository defines data operations for video storyboards.
type VideoStoryboardRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error)
}
