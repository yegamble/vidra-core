package port

import (
	"vidra-core/internal/domain"
	"context"

	"github.com/google/uuid"
)

// CaptionRepository defines the interface for caption data operations
type CaptionRepository interface {
	Create(ctx context.Context, caption *domain.Caption) error
	GetByID(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error)
	GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.Caption, error)
	GetByVideoAndLanguage(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error)
	Update(ctx context.Context, caption *domain.Caption) error
	Delete(ctx context.Context, captionID uuid.UUID) error
	DeleteByVideoID(ctx context.Context, videoID uuid.UUID) error
	CountByVideoID(ctx context.Context, videoID uuid.UUID) (int, error)
}

// CaptionGenerationRepository defines the interface for caption generation job operations
type CaptionGenerationRepository interface {
	// Job lifecycle
	Create(ctx context.Context, job *domain.CaptionGenerationJob) error
	GetByID(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error)
	Update(ctx context.Context, job *domain.CaptionGenerationJob) error
	Delete(ctx context.Context, jobID uuid.UUID) error

	// Queue operations
	GetNextPendingJob(ctx context.Context) (*domain.CaptionGenerationJob, error)
	GetPendingJobs(ctx context.Context, limit int) ([]domain.CaptionGenerationJob, error)
	CountByStatus(ctx context.Context, status domain.CaptionGenerationStatus) (int, error)

	// Query operations
	GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]domain.CaptionGenerationJob, error)

	// Status updates
	UpdateStatus(ctx context.Context, jobID uuid.UUID, status domain.CaptionGenerationStatus) error
	UpdateProgress(ctx context.Context, jobID uuid.UUID, progress int) error
	MarkFailed(ctx context.Context, jobID uuid.UUID, errorMessage string) error
	MarkCompleted(ctx context.Context, jobID uuid.UUID, captionID uuid.UUID, detectedLanguage string, transcriptionTimeSecs int) error

	// Cleanup
	DeleteOldCompletedJobs(ctx context.Context, olderThanDays int) (int64, error)
}
