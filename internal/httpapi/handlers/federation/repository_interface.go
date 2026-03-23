package federation

import (
	"context"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
)

type FederationRepositoryInterface interface {
	ListJobs(ctx context.Context, status string, limit, offset int) ([]domain.FederationJob, int, error)
	GetJob(ctx context.Context, id string) (*domain.FederationJob, error)
	RetryJob(ctx context.Context, id string, when time.Time) error
	DeleteJob(ctx context.Context, id string) error

	ListActors(ctx context.Context, limit, offset int) ([]repository.FederationActor, int, error)
	UpsertActor(ctx context.Context, actor string, enabled bool, rateLimitSeconds int) error
	UpdateActor(ctx context.Context, actor string, enabled *bool, rateLimitSeconds *int, cursor *string, nextAt *time.Time, attempts *int) error
	DeleteActor(ctx context.Context, actor string) error
}
