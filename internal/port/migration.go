package port

import (
	"context"

	"vidra-core/internal/domain"
)

// MigrationJobRepository defines the interface for migration job persistence
type MigrationJobRepository interface {
	Create(ctx context.Context, job *domain.MigrationJob) error
	GetByID(ctx context.Context, id string) (*domain.MigrationJob, error)
	List(ctx context.Context, limit, offset int) ([]*domain.MigrationJob, int64, error)
	Update(ctx context.Context, job *domain.MigrationJob) error
	Delete(ctx context.Context, id string) error
	GetRunning(ctx context.Context) (*domain.MigrationJob, error)
}
