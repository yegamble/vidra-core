package port

import (
	"context"
	"vidra-core/internal/domain"
)

// VideoPasswordRepository defines data operations for video passwords.
type VideoPasswordRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoPassword, error)
	Create(ctx context.Context, videoID string, passwordHash string) (*domain.VideoPassword, error)
	ReplaceAll(ctx context.Context, videoID string, passwordHashes []string) ([]domain.VideoPassword, error)
	Delete(ctx context.Context, passwordID int64) error
}
