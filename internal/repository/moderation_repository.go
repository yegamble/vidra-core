package repository

import (
	"context"
	"database/sql"
	"fmt"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
)

// ModerationRepository handles database operations for moderation features
type ModerationRepository struct {
	db *sqlx.DB
}

// NewModerationRepository creates a new instance of ModerationRepository
func NewModerationRepository(db *sqlx.DB) *ModerationRepository {
	return &ModerationRepository{db: db}
}

// GetUserRole returns the role for a given user ID.
// This helper allows HTTP handlers to authorize requests without
// depending on an additional repository instance.
func (r *ModerationRepository) GetUserRole(ctx context.Context, userID string) (domain.UserRole, error) {
	var role string
	err := r.db.GetContext(ctx, &role, "SELECT role FROM users WHERE id = $1", userID)
	if err == sql.ErrNoRows {
		return "", domain.NewDomainError("NOT_FOUND", "User not found")
	}
	return domain.UserRole(role), err
}

// CreateAbuseReport creates a new abuse report
func (r *ModerationRepository) CreateAbuseReport(ctx context.Context, report *domain.AbuseReport) error {
	query := `
		INSERT INTO abuse_reports (
			reporter_id, reason, details, status,
			reported_entity_type, reported_video_id, reported_comment_id,
			reported_user_id, reported_channel_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query,
		report.ReporterID,
		report.Reason,
		report.Details,
		report.Status,
		report.EntityType,
		report.VideoID,
		report.CommentID,
		report.UserID,
		report.ChannelID,
	).Scan(&report.ID, &report.CreatedAt, &report.UpdatedAt)

	return err
}

// GetAbuseReport retrieves an abuse report by ID
func (r *ModerationRepository) GetAbuseReport(ctx context.Context, reportID string) (*domain.AbuseReport, error) {
	var report domain.AbuseReport
	query := `
		SELECT id, reporter_id, reason, details, status, moderator_notes,
		       moderated_by, moderated_at, reported_entity_type,
		       reported_video_id, reported_comment_id, reported_user_id,
		       reported_channel_id, created_at, updated_at
		FROM abuse_reports
		WHERE id = $1`

	err := r.db.GetContext(ctx, &report, query, reportID)
	if err == sql.ErrNoRows {
		return nil, domain.NewDomainError("NOT_FOUND", "Abuse report not found")
	}
	return &report, err
}

// ListAbuseReports lists abuse reports with pagination
func (r *ModerationRepository) ListAbuseReports(ctx context.Context, status string, entityType string, limit, offset int) ([]*domain.AbuseReport, int64, error) {
	var reports []*domain.AbuseReport
	var total int64

	// Build query with optional status filter
	whereClause := " WHERE 1=1"
	args := []interface{}{}
	argCount := 1

	if status != "" {
		whereClause += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, status)
		argCount++
	}

	if entityType != "" {
		whereClause += fmt.Sprintf(" AND reported_entity_type = $%d", argCount)
		args = append(args, entityType)
		argCount++
	}

	// Count total
	countQuery := "SELECT COUNT(*) FROM abuse_reports" + whereClause
	err := r.db.GetContext(ctx, &total, countQuery, args...)
	if err != nil {
		return nil, 0, err
	}

	// Get paginated results
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
        SELECT id, reporter_id, reason, details, status, moderator_notes,
               moderated_by, moderated_at, reported_entity_type,
               reported_video_id, reported_comment_id, reported_user_id,
               reported_channel_id, created_at, updated_at
        FROM abuse_reports
        %s
        ORDER BY created_at DESC
        LIMIT $%d OFFSET $%d`, whereClause, argCount, argCount+1)

	err = r.db.SelectContext(ctx, &reports, query, args...)
	return reports, total, err
}

// UpdateAbuseReport updates an abuse report with moderator action
func (r *ModerationRepository) UpdateAbuseReport(ctx context.Context, reportID, moderatorID string, status domain.AbuseReportStatus, notes string) error {
	query := `
		UPDATE abuse_reports
		SET status = $2, moderator_notes = $3, moderated_by = $4, moderated_at = CURRENT_TIMESTAMP
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, reportID, status, sql.NullString{String: notes, Valid: notes != ""}, moderatorID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("NOT_FOUND", "Abuse report not found")
	}

	return nil
}

// DeleteAbuseReport deletes an abuse report
func (r *ModerationRepository) DeleteAbuseReport(ctx context.Context, reportID string) error {
	query := "DELETE FROM abuse_reports WHERE id = $1"
	result, err := r.db.ExecContext(ctx, query, reportID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("NOT_FOUND", "Abuse report not found")
	}

	return nil
}

// CreateBlocklistEntry creates a new blocklist entry
func (r *ModerationRepository) CreateBlocklistEntry(ctx context.Context, entry *domain.BlocklistEntry) error {
	query := `
		INSERT INTO blocklist (block_type, blocked_value, reason, blocked_by, expires_at, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query,
		entry.BlockType,
		entry.BlockedValue,
		entry.Reason,
		entry.BlockedBy,
		entry.ExpiresAt,
		entry.IsActive,
	).Scan(&entry.ID, &entry.CreatedAt, &entry.UpdatedAt)

	return err
}

// GetBlocklistEntry retrieves a blocklist entry by ID
func (r *ModerationRepository) GetBlocklistEntry(ctx context.Context, entryID string) (*domain.BlocklistEntry, error) {
	var entry domain.BlocklistEntry
	query := `
		SELECT id, block_type, blocked_value, reason, blocked_by,
		       expires_at, is_active, created_at, updated_at
		FROM blocklist
		WHERE id = $1`

	err := r.db.GetContext(ctx, &entry, query, entryID)
	if err == sql.ErrNoRows {
		return nil, domain.NewDomainError("NOT_FOUND", "Blocklist entry not found")
	}
	return &entry, err
}

// ListBlocklistEntries lists blocklist entries with pagination
func (r *ModerationRepository) ListBlocklistEntries(ctx context.Context, blockType string, activeOnly bool, limit, offset int) ([]*domain.BlocklistEntry, int64, error) {
	var entries []*domain.BlocklistEntry
	var total int64

	// Build query with filters
	whereClause := " WHERE 1=1"
	args := []interface{}{}
	argCount := 1

	if blockType != "" {
		whereClause += fmt.Sprintf(" AND block_type = $%d", argCount)
		args = append(args, blockType)
		argCount++
	}

	if activeOnly {
		whereClause += fmt.Sprintf(" AND is_active = $%d", argCount)
		args = append(args, true)
		argCount++
	}

	// Count total
	countQuery := "SELECT COUNT(*) FROM blocklist" + whereClause
	err := r.db.GetContext(ctx, &total, countQuery, args...)
	if err != nil {
		return nil, 0, err
	}

	// Get paginated results
	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT id, block_type, blocked_value, reason, blocked_by,
		       expires_at, is_active, created_at, updated_at
		FROM blocklist
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, whereClause, argCount, argCount+1)

	err = r.db.SelectContext(ctx, &entries, query, args...)
	return entries, total, err
}

// UpdateBlocklistEntry updates a blocklist entry
func (r *ModerationRepository) UpdateBlocklistEntry(ctx context.Context, entryID string, isActive bool, expiresAt sql.NullTime) error {
	query := `
		UPDATE blocklist
		SET is_active = $2, expires_at = $3
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, entryID, isActive, expiresAt)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("NOT_FOUND", "Blocklist entry not found")
	}

	return nil
}

// DeleteBlocklistEntry deletes a blocklist entry
func (r *ModerationRepository) DeleteBlocklistEntry(ctx context.Context, entryID string) error {
	query := "DELETE FROM blocklist WHERE id = $1"
	result, err := r.db.ExecContext(ctx, query, entryID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("NOT_FOUND", "Blocklist entry not found")
	}

	return nil
}

// IsBlocked checks if a value is blocked
func (r *ModerationRepository) IsBlocked(ctx context.Context, blockType domain.BlockType, value string) (bool, error) {
	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM blocklist
			WHERE block_type = $1
			AND blocked_value = $2
			AND is_active = true
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`

	err := r.db.GetContext(ctx, &exists, query, blockType, value)
	return exists, err
}

// GetInstanceConfig retrieves an instance configuration by key
func (r *ModerationRepository) GetInstanceConfig(ctx context.Context, key string) (*domain.InstanceConfig, error) {
	var config domain.InstanceConfig
	query := `
		SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config
		WHERE key = $1`

	err := r.db.GetContext(ctx, &config, query, key)
	if err == sql.ErrNoRows {
		return nil, domain.NewDomainError("NOT_FOUND", "Configuration key not found")
	}
	return &config, err
}

// ListInstanceConfigs lists all instance configurations
func (r *ModerationRepository) ListInstanceConfigs(ctx context.Context, publicOnly bool) ([]*domain.InstanceConfig, error) {
	var configs []*domain.InstanceConfig

	query := `
		SELECT key, value, description, is_public, created_at, updated_at
		FROM instance_config`

	if publicOnly {
		query += " WHERE is_public = true"
	}
	query += " ORDER BY key"

	err := r.db.SelectContext(ctx, &configs, query)
	return configs, err
}

// UpdateInstanceConfig updates an instance configuration
func (r *ModerationRepository) UpdateInstanceConfig(ctx context.Context, key string, value []byte, isPublic bool) error {
	query := `
		INSERT INTO instance_config (key, value, is_public)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, is_public = EXCLUDED.is_public`

	_, err := r.db.ExecContext(ctx, query, key, value, isPublic)
	return err
}

// GetInstanceStats retrieves instance statistics
func (r *ModerationRepository) GetInstanceStats(ctx context.Context) (totalUsers, totalVideos, totalLocalVideos, totalViews int64, err error) {
	// Get user count
	err = r.db.GetContext(ctx, &totalUsers, "SELECT COUNT(*) FROM users WHERE is_active = true")
	if err != nil {
		return
	}

	// Get video counts
	err = r.db.GetContext(ctx, &totalVideos, "SELECT COUNT(*) FROM videos WHERE privacy = 'public'")
	if err != nil {
		return
	}

	// For now, all videos are local
	totalLocalVideos = totalVideos

	// Get total views (count all view records)
	err = r.db.GetContext(ctx, &totalViews, "SELECT COUNT(*) FROM user_views")
	return
}
