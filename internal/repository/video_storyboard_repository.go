package repository

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

type videoStoryboardRepository struct {
	db *sqlx.DB
}

// NewVideoStoryboardRepository creates a new VideoStoryboardRepository.
func NewVideoStoryboardRepository(db *sqlx.DB) port.VideoStoryboardRepository {
	return &videoStoryboardRepository{db: db}
}

func (r *videoStoryboardRepository) Create(ctx context.Context, sb *domain.VideoStoryboard) error {
	query := `INSERT INTO video_storyboards (video_id, filename, total_height, total_width, sprite_height, sprite_width, sprite_duration)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`
	return r.db.QueryRowContext(ctx, query,
		sb.VideoID, sb.Filename, sb.TotalHeight, sb.TotalWidth,
		sb.SpriteHeight, sb.SpriteWidth, sb.SpriteDuration,
	).Scan(&sb.ID)
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
