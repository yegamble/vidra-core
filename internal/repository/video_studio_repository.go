package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
)

// StudioJobRepository is the SQLX implementation of port.StudioJobRepository.
type StudioJobRepository struct {
	db *sqlx.DB
}

// NewStudioJobRepository creates a new StudioJobRepository backed by the given database.
func NewStudioJobRepository(db *sqlx.DB) *StudioJobRepository {
	return &StudioJobRepository{db: db}
}

// Create inserts a new studio editing job.
func (r *StudioJobRepository) Create(ctx context.Context, job *domain.StudioJob) error {
	query := `INSERT INTO video_studio_jobs (id, video_id, user_id, status, tasks, created_at, updated_at)
	           VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.ExecContext(ctx, query,
		job.ID, job.VideoID, job.UserID, job.Status, job.Tasks, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create studio job: %w", err)
	}
	return nil
}

// GetByID retrieves a single studio job by its ID.
func (r *StudioJobRepository) GetByID(ctx context.Context, id string) (*domain.StudioJob, error) {
	var job domain.StudioJob
	err := r.db.GetContext(ctx, &job,
		`SELECT id, video_id, user_id, status, tasks, created_at, updated_at, completed_at, error_message
		 FROM video_studio_jobs WHERE id = $1`, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrStudioJobNotFound
		}
		return nil, fmt.Errorf("get studio job: %w", err)
	}
	return &job, nil
}

// GetByVideoID returns all studio jobs for a given video, ordered newest first.
func (r *StudioJobRepository) GetByVideoID(ctx context.Context, videoID string) ([]*domain.StudioJob, error) {
	var jobs []*domain.StudioJob
	err := r.db.SelectContext(ctx, &jobs,
		`SELECT id, video_id, user_id, status, tasks, created_at, updated_at, completed_at, error_message
		 FROM video_studio_jobs WHERE video_id = $1 ORDER BY created_at DESC`, videoID)
	if err != nil {
		return nil, fmt.Errorf("list studio jobs for video: %w", err)
	}
	return jobs, nil
}

// UpdateStatus transitions a studio job to a new status with an optional error message.
func (r *StudioJobRepository) UpdateStatus(ctx context.Context, id string, status domain.StudioJobStatus, errorMessage string) error {
	now := time.Now().UTC()
	var completedAt *time.Time
	if status == domain.StudioJobStatusCompleted || status == domain.StudioJobStatusFailed {
		completedAt = &now
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE video_studio_jobs
		 SET status = $1, error_message = $2, updated_at = $3, completed_at = $4
		 WHERE id = $5`,
		status, errorMessage, now, completedAt, id)
	if err != nil {
		return fmt.Errorf("update studio job status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrStudioJobNotFound
	}
	return nil
}

// List returns a paginated list of all studio jobs, ordered newest first.
func (r *StudioJobRepository) List(ctx context.Context, limit, offset int) ([]*domain.StudioJob, int64, error) {
	var total int64
	err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM video_studio_jobs`)
	if err != nil {
		return nil, 0, fmt.Errorf("count studio jobs: %w", err)
	}

	var jobs []*domain.StudioJob
	err = r.db.SelectContext(ctx, &jobs,
		`SELECT id, video_id, user_id, status, tasks, created_at, updated_at, completed_at, error_message
		 FROM video_studio_jobs ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list studio jobs: %w", err)
	}

	return jobs, total, nil
}
