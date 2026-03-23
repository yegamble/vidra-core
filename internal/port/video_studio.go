package port

import (
	"vidra-core/internal/domain"
	"context"
)

// StudioJobRepository defines data operations for video studio editing jobs.
type StudioJobRepository interface {
	Create(ctx context.Context, job *domain.StudioJob) error
	GetByID(ctx context.Context, id string) (*domain.StudioJob, error)
	GetByVideoID(ctx context.Context, videoID string) ([]*domain.StudioJob, error)
	UpdateStatus(ctx context.Context, id string, status domain.StudioJobStatus, errorMessage string) error
	List(ctx context.Context, limit, offset int) ([]*domain.StudioJob, int64, error)
}
