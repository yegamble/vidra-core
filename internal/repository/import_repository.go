package repository

import (
	"context"
	"database/sql"
	"fmt"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
)

// ImportRepository handles persistence operations for video imports
type ImportRepository struct {
	db *sqlx.DB
}

// NewImportRepository creates a new import repository
func NewImportRepository(db *sqlx.DB) *ImportRepository {
	return &ImportRepository{db: db}
}

// Create creates a new video import record
func (r *ImportRepository) Create(ctx context.Context, imp *domain.VideoImport) error {
	query := `
		INSERT INTO video_imports (
			user_id, channel_id, source_url, status, target_privacy, target_category, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		RETURNING id, created_at, updated_at, progress, downloaded_bytes
	`

	err := r.db.QueryRowContext(
		ctx, query,
		imp.UserID, imp.ChannelID, imp.SourceURL, imp.Status,
		imp.TargetPrivacy, imp.TargetCategory, imp.Metadata,
	).Scan(&imp.ID, &imp.CreatedAt, &imp.UpdatedAt, &imp.Progress, &imp.DownloadedBytes)

	if err != nil {
		return fmt.Errorf("failed to create import: %w", err)
	}

	return nil
}

// GetByID retrieves an import by ID
func (r *ImportRepository) GetByID(ctx context.Context, importID string) (*domain.VideoImport, error) {
	var imp domain.VideoImport

	query := `
		SELECT id, user_id, channel_id, source_url, status, video_id, error_message,
		       progress, metadata, file_size_bytes, downloaded_bytes, target_privacy,
		       target_category, created_at, started_at, completed_at, updated_at
		FROM video_imports
		WHERE id = $1
	`

	err := r.db.GetContext(ctx, &imp, query, importID)
	if err == sql.ErrNoRows {
		return nil, domain.ErrImportNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get import: %w", err)
	}

	return &imp, nil
}

// GetByUserID retrieves imports for a specific user with pagination
func (r *ImportRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, error) {
	query := `
		SELECT id, user_id, channel_id, source_url, status, video_id, error_message,
		       progress, metadata, file_size_bytes, downloaded_bytes, target_privacy,
		       target_category, created_at, started_at, completed_at, updated_at
		FROM video_imports
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	var imports []*domain.VideoImport
	err := r.db.SelectContext(ctx, &imports, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get imports by user: %w", err)
	}

	return imports, nil
}

// CountByUserID counts total imports for a user
func (r *ImportRepository) CountByUserID(ctx context.Context, userID string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM video_imports WHERE user_id = $1`

	err := r.db.GetContext(ctx, &count, query, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to count imports: %w", err)
	}

	return count, nil
}

// CountByUserIDAndStatus counts imports for a user with a specific status
func (r *ImportRepository) CountByUserIDAndStatus(ctx context.Context, userID string, status domain.ImportStatus) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM video_imports WHERE user_id = $1 AND status = $2`

	err := r.db.GetContext(ctx, &count, query, userID, status)
	if err != nil {
		return 0, fmt.Errorf("failed to count imports by status: %w", err)
	}

	return count, nil
}

// CountByUserIDToday counts imports created by a user today
func (r *ImportRepository) CountByUserIDToday(ctx context.Context, userID string) (int, error) {
	var count int
	query := `
		SELECT COUNT(*)
		FROM video_imports
		WHERE user_id = $1
		  AND created_at >= CURRENT_DATE
		  AND created_at < CURRENT_DATE + INTERVAL '1 day'
	`

	err := r.db.GetContext(ctx, &count, query, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to count today's imports: %w", err)
	}

	return count, nil
}

// GetPending retrieves all pending imports (for background worker)
func (r *ImportRepository) GetPending(ctx context.Context, limit int) ([]*domain.VideoImport, error) {
	query := `
		SELECT id, user_id, channel_id, source_url, status, video_id, error_message,
		       progress, metadata, file_size_bytes, downloaded_bytes, target_privacy,
		       target_category, created_at, started_at, completed_at, updated_at
		FROM video_imports
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2
	`

	var imports []*domain.VideoImport
	err := r.db.SelectContext(ctx, &imports, query, domain.ImportStatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending imports: %w", err)
	}

	return imports, nil
}

// Update updates an import record
func (r *ImportRepository) Update(ctx context.Context, imp *domain.VideoImport) error {
	query := `
		UPDATE video_imports
		SET status = $2,
		    video_id = $3,
		    error_message = $4,
		    progress = $5,
		    metadata = $6,
		    file_size_bytes = $7,
		    downloaded_bytes = $8,
		    started_at = $9,
		    completed_at = $10,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := r.db.ExecContext(
		ctx, query,
		imp.ID, imp.Status, imp.VideoID, imp.ErrorMessage,
		imp.Progress, imp.Metadata, imp.FileSizeBytes, imp.DownloadedBytes,
		imp.StartedAt, imp.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update import: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// UpdateProgress atomically updates the progress and downloaded bytes
func (r *ImportRepository) UpdateProgress(ctx context.Context, importID string, progress int, downloadedBytes int64) error {
	query := `
		UPDATE video_imports
		SET progress = $2,
		    downloaded_bytes = $3,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, importID, progress, downloadedBytes)
	if err != nil {
		return fmt.Errorf("failed to update import progress: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// UpdateStatus updates only the status field
func (r *ImportRepository) UpdateStatus(ctx context.Context, importID string, status domain.ImportStatus) error {
	query := `
		UPDATE video_imports
		SET status = $2,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, importID, status)
	if err != nil {
		return fmt.Errorf("failed to update import status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// UpdateMetadata updates the metadata field
func (r *ImportRepository) UpdateMetadata(ctx context.Context, importID string, metadata []byte) error {
	query := `
		UPDATE video_imports
		SET metadata = $2,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, importID, metadata)
	if err != nil {
		return fmt.Errorf("failed to update import metadata: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// MarkFailed marks an import as failed with an error message
func (r *ImportRepository) MarkFailed(ctx context.Context, importID string, errorMessage string) error {
	query := `
		UPDATE video_imports
		SET status = $2,
		    error_message = $3,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, importID, domain.ImportStatusFailed, errorMessage)
	if err != nil {
		return fmt.Errorf("failed to mark import as failed: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// MarkCompleted marks an import as completed and links it to a video
func (r *ImportRepository) MarkCompleted(ctx context.Context, importID string, videoID string) error {
	query := `
		UPDATE video_imports
		SET status = $2,
		    video_id = $3,
		    progress = 100,
		    completed_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, importID, domain.ImportStatusCompleted, videoID)
	if err != nil {
		return fmt.Errorf("failed to mark import as completed: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// Delete deletes an import record
func (r *ImportRepository) Delete(ctx context.Context, importID string) error {
	query := `DELETE FROM video_imports WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, importID)
	if err != nil {
		return fmt.Errorf("failed to delete import: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrImportNotFound
	}

	return nil
}

// CleanupOldImports deletes completed/failed imports older than the specified days
func (r *ImportRepository) CleanupOldImports(ctx context.Context, daysOld int) (int64, error) {
	query := `
		DELETE FROM video_imports
		WHERE status IN ($1, $2, $3)
		  AND updated_at < CURRENT_TIMESTAMP - INTERVAL '1 day' * $4
	`

	result, err := r.db.ExecContext(
		ctx, query,
		domain.ImportStatusCompleted,
		domain.ImportStatusFailed,
		domain.ImportStatusCancelled,
		daysOld,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old imports: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}

// GetStuckImports finds imports that have been in downloading/processing status for too long
func (r *ImportRepository) GetStuckImports(ctx context.Context, hoursStuck int) ([]*domain.VideoImport, error) {
	query := `
		SELECT id, user_id, channel_id, source_url, status, video_id, error_message,
		       progress, metadata, file_size_bytes, downloaded_bytes, target_privacy,
		       target_category, created_at, started_at, completed_at, updated_at
		FROM video_imports
		WHERE status IN ($1, $2)
		  AND updated_at < CURRENT_TIMESTAMP - INTERVAL '1 hour' * $3
		ORDER BY updated_at ASC
	`

	var imports []*domain.VideoImport
	err := r.db.SelectContext(
		ctx, &imports, query,
		domain.ImportStatusDownloading,
		domain.ImportStatusProcessing,
		hoursStuck,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get stuck imports: %w", err)
	}

	return imports, nil
}
