package repository

import (
	"vidra-core/internal/domain"
	"vidra-core/internal/usecase"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type encodingRepository struct {
	db *sqlx.DB
}

func NewEncodingRepository(db *sqlx.DB) usecase.EncodingRepository {
	return &encodingRepository{db: db}
}

func (r *encodingRepository) ListJobsByStatus(ctx context.Context, status string) ([]*domain.EncodingJob, error) {
	query := `SELECT id, video_id, source_file_path, source_resolution, target_resolutions,
		status, progress, error_message, started_at, completed_at, created_at, updated_at
		FROM encoding_jobs WHERE status = $1 ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, status)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs by status: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.EncodingJob
	for rows.Next() {
		var job domain.EncodingJob
		var targetResolutions pq.StringArray
		err := rows.Scan(
			&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
			&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
			&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		job.TargetResolutions = []string(targetResolutions)
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

func (r *encodingRepository) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	query := `
		INSERT INTO encoding_jobs (
			id, video_id, source_file_path, source_resolution,
			target_resolutions, status, progress, error_message,
			started_at, completed_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)`

	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.VideoID, job.SourceFilePath, job.SourceResolution,
		pq.Array(job.TargetResolutions), job.Status, job.Progress,
		job.ErrorMessage, job.StartedAt, job.CompletedAt,
		job.CreatedAt, job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create encoding job: %w", err)
	}
	return nil
}

func (r *encodingRepository) GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error) {
	query := `
		SELECT id, video_id, source_file_path, source_resolution,
		       target_resolutions, status, progress, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM encoding_jobs WHERE id = $1`

	var job domain.EncodingJob
	var targetResolutions pq.StringArray

	err := r.db.QueryRowContext(ctx, query, jobID).Scan(
		&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
		&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
		&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found")
		}
		return nil, fmt.Errorf("failed to get encoding job: %w", err)
	}

	job.TargetResolutions = []string(targetResolutions)
	return &job, nil
}

func (r *encodingRepository) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	query := `
		SELECT id, video_id, source_file_path, source_resolution,
		       target_resolutions, status, progress, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM encoding_jobs WHERE video_id = $1
		ORDER BY created_at DESC LIMIT 1`

	var job domain.EncodingJob
	var targetResolutions pq.StringArray

	err := r.db.QueryRowContext(ctx, query, videoID).Scan(
		&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
		&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
		&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found for video")
		}
		return nil, fmt.Errorf("failed to get encoding job by video ID: %w", err)
	}

	job.TargetResolutions = []string(targetResolutions)
	return &job, nil
}

func (r *encodingRepository) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	query := `
		UPDATE encoding_jobs SET
			source_file_path = $2, source_resolution = $3,
			target_resolutions = $4, status = $5, progress = $6,
			error_message = $7, started_at = $8, completed_at = $9,
			updated_at = $10
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query,
		job.ID, job.SourceFilePath, job.SourceResolution,
		pq.Array(job.TargetResolutions), job.Status, job.Progress,
		job.ErrorMessage, job.StartedAt, job.CompletedAt, job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update encoding job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found")
	}

	return nil
}

func (r *encodingRepository) DeleteJob(ctx context.Context, jobID string) error {
	query := `DELETE FROM encoding_jobs WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, jobID)
	if err != nil {
		return fmt.Errorf("failed to delete encoding job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found")
	}

	return nil
}

func (r *encodingRepository) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	if limit <= 0 {
		limit = 10 // Default limit
	}

	query := `
		SELECT id, video_id, source_file_path, source_resolution,
		       target_resolutions, status, progress, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM encoding_jobs
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobs []*domain.EncodingJob
	for rows.Next() {
		var job domain.EncodingJob
		var targetResolutions pq.StringArray

		err := rows.Scan(
			&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
			&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
			&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan encoding job: %w", err)
		}

		job.TargetResolutions = []string(targetResolutions)
		jobs = append(jobs, &job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return jobs, nil
}

func (r *encodingRepository) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	// Use a transaction to atomically get and update the job status
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		SELECT id, video_id, source_file_path, source_resolution,
		       target_resolutions, status, progress, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM encoding_jobs
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`

	var job domain.EncodingJob
	var targetResolutions pq.StringArray

	err = tx.QueryRowContext(ctx, query).Scan(
		&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
		&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
		&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No pending jobs
		}
		return nil, fmt.Errorf("failed to get next job: %w", err)
	}

	job.TargetResolutions = []string(targetResolutions)

	// Update job status to processing
	updateQuery := `
		UPDATE encoding_jobs
		SET status = 'processing', started_at = NOW(), updated_at = NOW()
		WHERE id = $1`

	_, err = tx.ExecContext(ctx, updateQuery, job.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update job status: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Update the job object with new status
	job.Status = domain.EncodingStatusProcessing
	job.StartedAt = &job.UpdatedAt

	return &job, nil
}

func (r *encodingRepository) UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error {
	query := `
		UPDATE encoding_jobs
		SET status = $2, updated_at = NOW()
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
		return domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found")
	}

	return nil
}

func (r *encodingRepository) UpdateJobProgress(ctx context.Context, jobID string, progress int) error {
	query := `
		UPDATE encoding_jobs
		SET progress = $2, updated_at = NOW()
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
		return domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found")
	}

	return nil
}

// GetJobCounts returns counts of jobs by status
func (r *encodingRepository) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM encoding_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("failed to count jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int64{"pending": 0, "processing": 0, "completed": 0, "failed": 0}
	for rows.Next() {
		var status string
		var c int64
		if err := rows.Scan(&status, &c); err != nil {
			return nil, fmt.Errorf("failed to scan count: %w", err)
		}
		counts[status] = c
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return counts, nil
}

func (r *encodingRepository) SetJobError(ctx context.Context, jobID string, errorMsg string) error {
	query := `
		UPDATE encoding_jobs
		SET status = 'failed', error_message = $2, updated_at = NOW()
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, jobID, errorMsg)
	if err != nil {
		return fmt.Errorf("failed to set job error: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found")
	}

	return nil
}

func (r *encodingRepository) ResetStaleJobs(ctx context.Context, staleDuration time.Duration) (int64, error) {
	cutoff := time.Now().Add(-staleDuration)

	query := `
		UPDATE encoding_jobs
		SET status = 'pending', progress = 0, started_at = NULL, error_message = ''
		WHERE status = 'processing'
		  AND updated_at < $1`

	result, err := r.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to reset stale encoding jobs: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}

// GetJobsByVideoID returns all encoding jobs for a specific video
func (r *encodingRepository) GetJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	query := `
		SELECT id, video_id, source_file_path, source_resolution,
		       target_resolutions, status, progress, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM encoding_jobs
		WHERE video_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs for video: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.EncodingJob
	for rows.Next() {
		var job domain.EncodingJob
		var targetResolutions pq.StringArray

		err := rows.Scan(
			&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
			&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
			&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		job.TargetResolutions = []string(targetResolutions)
		jobs = append(jobs, &job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return jobs, nil
}

// GetActiveJobsByVideoID returns only active (pending or processing) encoding jobs for a specific video
func (r *encodingRepository) GetActiveJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	query := `
		SELECT id, video_id, source_file_path, source_resolution,
		       target_resolutions, status, progress, error_message,
		       started_at, completed_at, created_at, updated_at
		FROM encoding_jobs
		WHERE video_id = $1 AND status IN ('pending', 'processing')
		ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active jobs for video: %w", err)
	}
	defer rows.Close()

	var jobs []*domain.EncodingJob
	for rows.Next() {
		var job domain.EncodingJob
		var targetResolutions pq.StringArray

		err := rows.Scan(
			&job.ID, &job.VideoID, &job.SourceFilePath, &job.SourceResolution,
			&targetResolutions, &job.Status, &job.Progress, &job.ErrorMessage,
			&job.StartedAt, &job.CompletedAt, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		job.TargetResolutions = []string(targetResolutions)
		jobs = append(jobs, &job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return jobs, nil
}
