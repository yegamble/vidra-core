package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
)

// MigrationRepository handles persistence operations for migration jobs
type MigrationRepository struct {
	db *sqlx.DB
}

// NewMigrationRepository creates a new migration repository
func NewMigrationRepository(db *sqlx.DB) *MigrationRepository {
	return &MigrationRepository{db: db}
}

// Create creates a new migration job record
func (r *MigrationRepository) Create(ctx context.Context, job *domain.MigrationJob) error {
	query := `
		INSERT INTO migration_jobs (
			admin_user_id, source_host, status, dry_run,
			source_db_host, source_db_port, source_db_name,
			source_db_user, source_db_password, source_media_path, stats_json
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		job.AdminUserID, job.SourceHost, job.Status, job.DryRun,
		job.SourceDBHost, job.SourceDBPort, job.SourceDBName,
		job.SourceDBUser, job.SourceDBPassword, job.SourceMediaPath,
		job.StatsJSON,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create migration job: %w", err)
	}

	return nil
}

// GetByID retrieves a migration job by ID
func (r *MigrationRepository) GetByID(ctx context.Context, id string) (*domain.MigrationJob, error) {
	var job domain.MigrationJob

	query := `
		SELECT id, admin_user_id, source_host, status, dry_run, error_message,
		       stats_json, source_db_host, source_db_port, source_db_name,
		       source_db_user, source_db_password, source_media_path,
		       created_at, started_at, completed_at, updated_at
		FROM migration_jobs
		WHERE id = $1
	`

	err := r.db.GetContext(ctx, &job, query, id)
	if err == sql.ErrNoRows {
		return nil, domain.ErrMigrationNotFound
	}
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "invalid input syntax for type uuid") ||
			strings.Contains(errStr, "invalid UUID") {
			return nil, domain.ErrMigrationNotFound
		}
		return nil, fmt.Errorf("failed to get migration job: %w", err)
	}

	return &job, nil
}

// List retrieves migration jobs with pagination
func (r *MigrationRepository) List(ctx context.Context, limit, offset int) ([]*domain.MigrationJob, int64, error) {
	var total int64
	countQuery := `SELECT COUNT(*) FROM migration_jobs`
	if err := r.db.GetContext(ctx, &total, countQuery); err != nil {
		return nil, 0, fmt.Errorf("failed to count migration jobs: %w", err)
	}

	query := `
		SELECT id, admin_user_id, source_host, status, dry_run, error_message,
		       stats_json, source_db_host, source_db_port, source_db_name,
		       source_db_user, source_db_password, source_media_path,
		       created_at, started_at, completed_at, updated_at
		FROM migration_jobs
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	var jobs []*domain.MigrationJob
	if err := r.db.SelectContext(ctx, &jobs, query, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("failed to list migration jobs: %w", err)
	}

	return jobs, total, nil
}

// Update updates an existing migration job
func (r *MigrationRepository) Update(ctx context.Context, job *domain.MigrationJob) error {
	query := `
		UPDATE migration_jobs
		SET status = $1, dry_run = $2, error_message = $3, stats_json = $4,
		    started_at = $5, completed_at = $6, updated_at = NOW()
		WHERE id = $7
	`

	result, err := r.db.ExecContext(
		ctx, query,
		job.Status, job.DryRun, job.ErrorMessage, job.StatsJSON,
		job.StartedAt, job.CompletedAt, job.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update migration job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrMigrationNotFound
	}

	return nil
}

// Delete deletes a migration job by ID
func (r *MigrationRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM migration_jobs WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete migration job: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrMigrationNotFound
	}

	return nil
}

// GetRunning retrieves a currently running migration job, if any
func (r *MigrationRepository) GetRunning(ctx context.Context) (*domain.MigrationJob, error) {
	var job domain.MigrationJob

	query := `
		SELECT id, admin_user_id, source_host, status, dry_run, error_message,
		       stats_json, source_db_host, source_db_port, source_db_name,
		       source_db_user, source_db_password, source_media_path,
		       created_at, started_at, completed_at, updated_at
		FROM migration_jobs
		WHERE status IN ('pending', 'running', 'validating')
		ORDER BY created_at DESC
		LIMIT 1
	`

	err := r.db.GetContext(ctx, &job, query)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get running migration job: %w", err)
	}

	return &job, nil
}
