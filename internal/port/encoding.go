package port

import (
	"context"
	"time"
	"vidra-core/internal/domain"
)

type EncodingRepository interface {
	// Encoding job management
	CreateJob(ctx context.Context, job *domain.EncodingJob) error
	GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error)
	GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error)
	UpdateJob(ctx context.Context, job *domain.EncodingJob) error
	DeleteJob(ctx context.Context, jobID string) error

	// Queue operations
	GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error)
	GetNextJob(ctx context.Context) (*domain.EncodingJob, error)
	ResetStaleJobs(ctx context.Context, staleDuration time.Duration) (int64, error)

	// Job status tracking
	UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error
	UpdateJobProgress(ctx context.Context, jobID string, progress int) error
	SetJobError(ctx context.Context, jobID string, errorMsg string) error
	GetJobCounts(ctx context.Context) (map[string]int64, error)

	// Job queries
	GetJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error)
	GetActiveJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error)
	ListJobsByStatus(ctx context.Context, status string) ([]*domain.EncodingJob, error)
}
