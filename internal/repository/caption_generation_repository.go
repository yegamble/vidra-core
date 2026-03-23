package repository

import (
	"vidra-core/internal/domain"
	"vidra-core/internal/port"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type captionGenerationRepository struct {
	db *sqlx.DB
}

func NewCaptionGenerationRepository(db *sqlx.DB) port.CaptionGenerationRepository {
	return &captionGenerationRepository{db: db}
}

// Create creates a new caption generation job
func (r *captionGenerationRepository) Create(ctx context.Context, job *domain.CaptionGenerationJob) error {
	query := `
		INSERT INTO caption_generation_jobs (
			id, video_id, user_id, source_audio_path, target_language, status,
			progress, model_size, provider, output_format, is_automatic,
			retry_count, max_retries, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		RETURNING id`

	if job.ID == uuid.Nil {
		job.ID = uuid.New()
	}

	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now

	err := r.db.QueryRowContext(
		ctx, query,
		job.ID, job.VideoID, job.UserID, job.SourceAudioPath, job.TargetLanguage,
		job.Status, job.Progress, job.ModelSize, job.Provider, job.OutputFormat,
		job.IsAutomatic, job.RetryCount, job.MaxRetries, job.CreatedAt,
	).Scan(&job.ID)

	if err != nil {
		return fmt.Errorf("failed to create caption generation job: %w", err)
	}

	return nil
}

// GetByID retrieves a caption generation job by ID
func (r *captionGenerationRepository) GetByID(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error) {
	var job domain.CaptionGenerationJob
	query := `
		SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE id = $1`

	err := r.db.GetContext(ctx, &job, query, jobID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get caption generation job: %w", err)
	}

	return &job, nil
}

// Update updates a caption generation job
func (r *captionGenerationRepository) Update(ctx context.Context, job *domain.CaptionGenerationJob) error {
	query := `
		UPDATE caption_generation_jobs
		SET target_language = $2,
			detected_language = $3,
			status = $4,
			progress = $5,
			error_message = $6,
			generated_caption_id = $7,
			transcription_time_seconds = $8,
			retry_count = $9,
			started_at = $10,
			completed_at = $11,
			updated_at = $12
		WHERE id = $1`

	job.UpdatedAt = time.Now()

	result, err := r.db.ExecContext(
		ctx, query,
		job.ID, job.TargetLanguage, job.DetectedLanguage, job.Status,
		job.Progress, job.ErrorMessage, job.GeneratedCaptionID,
		job.TranscriptionTimeSecs, job.RetryCount, job.StartedAt,
		job.CompletedAt, job.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update caption generation job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// Delete deletes a caption generation job
func (r *captionGenerationRepository) Delete(ctx context.Context, jobID uuid.UUID) error {
	query := `DELETE FROM caption_generation_jobs WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, jobID)
	if err != nil {
		return fmt.Errorf("failed to delete caption generation job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// GetNextPendingJob retrieves the next pending job for processing (FIFO)
func (r *captionGenerationRepository) GetNextPendingJob(ctx context.Context) (*domain.CaptionGenerationJob, error) {
	var job domain.CaptionGenerationJob
	query := `
		SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`

	err := r.db.GetContext(ctx, &job, query, domain.CaptionGenStatusPending)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No pending jobs
		}
		return nil, fmt.Errorf("failed to get next pending job: %w", err)
	}

	return &job, nil
}

// GetPendingJobs retrieves pending jobs up to the specified limit
func (r *captionGenerationRepository) GetPendingJobs(ctx context.Context, limit int) ([]domain.CaptionGenerationJob, error) {
	var jobs []domain.CaptionGenerationJob
	query := `
		SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2`

	err := r.db.SelectContext(ctx, &jobs, query, domain.CaptionGenStatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}

	return jobs, nil
}

// CountByStatus counts jobs by status
func (r *captionGenerationRepository) CountByStatus(ctx context.Context, status domain.CaptionGenerationStatus) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM caption_generation_jobs WHERE status = $1`

	err := r.db.GetContext(ctx, &count, query, status)
	if err != nil {
		return 0, fmt.Errorf("failed to count jobs by status: %w", err)
	}

	return count, nil
}

// GetByVideoID retrieves all caption generation jobs for a video
func (r *captionGenerationRepository) GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error) {
	var jobs []domain.CaptionGenerationJob
	query := `
		SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE video_id = $1
		ORDER BY created_at DESC`

	err := r.db.SelectContext(ctx, &jobs, query, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs by video ID: %w", err)
	}

	return jobs, nil
}

// GetByUserID retrieves caption generation jobs for a user with pagination
func (r *captionGenerationRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]domain.CaptionGenerationJob, error) {
	var jobs []domain.CaptionGenerationJob
	query := `
		SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &jobs, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs by user ID: %w", err)
	}

	return jobs, nil
}

// UpdateStatus updates the status of a caption generation job
func (r *captionGenerationRepository) UpdateStatus(ctx context.Context, jobID uuid.UUID, status domain.CaptionGenerationStatus) error {
	query := `
		UPDATE caption_generation_jobs
		SET status = $2,
			started_at = CASE WHEN $2 = 'processing' AND started_at IS NULL THEN NOW() ELSE started_at END,
			updated_at = NOW()
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, jobID, status)
	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// UpdateProgress updates the progress of a caption generation job
func (r *captionGenerationRepository) UpdateProgress(ctx context.Context, jobID uuid.UUID, progress int) error {
	query := `
		UPDATE caption_generation_jobs
		SET progress = $2,
			updated_at = NOW()
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, jobID, progress)
	if err != nil {
		return fmt.Errorf("failed to update job progress: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// MarkFailed marks a job as failed with an error message
func (r *captionGenerationRepository) MarkFailed(ctx context.Context, jobID uuid.UUID, errorMessage string) error {
	query := `
		UPDATE caption_generation_jobs
		SET status = $2,
			error_message = $3,
			completed_at = NOW(),
			updated_at = NOW()
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, jobID, domain.CaptionGenStatusFailed, errorMessage)
	if err != nil {
		return fmt.Errorf("failed to mark job as failed: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// MarkCompleted marks a job as completed with the generated caption ID and metadata
func (r *captionGenerationRepository) MarkCompleted(
	ctx context.Context,
	jobID uuid.UUID,
	captionID uuid.UUID,
	detectedLanguage string,
	transcriptionTimeSecs int,
) error {
	query := `
		UPDATE caption_generation_jobs
		SET status = $2,
			generated_caption_id = $3,
			detected_language = $4,
			transcription_time_seconds = $5,
			progress = 100,
			completed_at = NOW(),
			updated_at = NOW()
		WHERE id = $1`

	result, err := r.db.ExecContext(
		ctx, query,
		jobID, domain.CaptionGenStatusCompleted, captionID,
		detectedLanguage, transcriptionTimeSecs,
	)
	if err != nil {
		return fmt.Errorf("failed to mark job as completed: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// DeleteOldCompletedJobs deletes completed or failed jobs older than the specified number of days
func (r *captionGenerationRepository) DeleteOldCompletedJobs(ctx context.Context, olderThanDays int) (int64, error) {
	query := `
		DELETE FROM caption_generation_jobs
		WHERE status IN ($1, $2)
		  AND completed_at < NOW() - INTERVAL '1 day' * $3`

	result, err := r.db.ExecContext(
		ctx, query,
		domain.CaptionGenStatusCompleted,
		domain.CaptionGenStatusFailed,
		olderThanDays,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old completed jobs: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}
