package repository

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
	"athena/internal/port"
)

type videoStoryboardRepository struct {
	db *sqlx.DB
}

// NewVideoStoryboardRepository creates a new VideoStoryboardRepository.
func NewVideoStoryboardRepository(db *sqlx.DB) port.VideoStoryboardRepository {
	return &videoStoryboardRepository{db: db}
}

func (r *videoStoryboardRepository) ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error) {
	var storyboards []domain.VideoStoryboard
	err := r.db.SelectContext(ctx, &storyboards,
		`SELECT id, video_id, filename, total_height, total_width,
		        sprite_height, sprite_width, sprite_duration
		 FROM video_storyboards
		 WHERE video_id = $1
		 ORDER BY id ASC`, videoID)
	if err != nil {
		return nil, fmt.Errorf("list video storyboards: %w", err)
	}
	return storyboards, nil
}
