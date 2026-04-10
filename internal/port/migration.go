package port

import (
	"context"
	"time"

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

// ReverseETLService defines the interface for syncing Vidra Core data back to PeerTube.
type ReverseETLService interface {
	ReverseSync(ctx context.Context, job *domain.MigrationJob, users []*domain.User, videos []*domain.Video, comments []*domain.Comment) (interface{}, error)
	ShouldSync(entityCreatedAt, migrationStartedAt time.Time) bool
}

// IDMappingRepository defines the interface for PeerTube↔Vidra ID mapping persistence
type IDMappingRepository interface {
	Upsert(ctx context.Context, mapping *domain.MigrationIDMapping) error
	GetVidraID(ctx context.Context, entityType string, peertubeID int) (string, error)
	GetPeertubeID(ctx context.Context, entityType string, vidraID string) (int, error)
	ListByJobID(ctx context.Context, jobID string) ([]*domain.MigrationIDMapping, error)
	UpsertCheckpoint(ctx context.Context, jobID string, entityType string) error
	GetCompletedPhases(ctx context.Context, jobID string) ([]string, error)
}
