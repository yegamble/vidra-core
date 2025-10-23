package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// RedundancyRepository handles database operations for video redundancy
type RedundancyRepository struct {
	db *sqlx.DB
}

// NewRedundancyRepository creates a new redundancy repository
func NewRedundancyRepository(db *sqlx.DB) *RedundancyRepository {
	return &RedundancyRepository{db: db}
}

// ==================== InstancePeer Operations ====================

// CreateInstancePeer creates a new instance peer
func (r *RedundancyRepository) CreateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	query := `
		INSERT INTO instance_peers (
			instance_url, instance_name, instance_host, software, version,
			auto_accept_redundancy, max_redundancy_size_gb, accepts_new_redundancy,
			actor_url, inbox_url, shared_inbox_url, public_key,
			is_active
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		) RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		peer.InstanceURL, peer.InstanceName, peer.InstanceHost, peer.Software, peer.Version,
		peer.AutoAcceptRedundancy, peer.MaxRedundancySizeGB, peer.AcceptsNewRedundancy,
		peer.ActorURL, peer.InboxURL, peer.SharedInboxURL, peer.PublicKey,
		peer.IsActive,
	).Scan(&peer.ID, &peer.CreatedAt, &peer.UpdatedAt)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return domain.ErrInstancePeerAlreadyExists
		}
		return fmt.Errorf("failed to create instance peer: %w", err)
	}

	return nil
}

// GetInstancePeerByID retrieves an instance peer by ID
func (r *RedundancyRepository) GetInstancePeerByID(ctx context.Context, id string) (*domain.InstancePeer, error) {
	var peer domain.InstancePeer
	query := `
		SELECT * FROM instance_peers WHERE id = $1
	`

	err := r.db.GetContext(ctx, &peer, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrInstancePeerNotFound
		}
		return nil, fmt.Errorf("failed to get instance peer: %w", err)
	}

	return &peer, nil
}

// GetInstancePeerByURL retrieves an instance peer by URL
func (r *RedundancyRepository) GetInstancePeerByURL(ctx context.Context, instanceURL string) (*domain.InstancePeer, error) {
	var peer domain.InstancePeer
	query := `
		SELECT * FROM instance_peers WHERE instance_url = $1
	`

	err := r.db.GetContext(ctx, &peer, query, instanceURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrInstancePeerNotFound
		}
		return nil, fmt.Errorf("failed to get instance peer: %w", err)
	}

	return &peer, nil
}

// ListInstancePeers retrieves all instance peers with pagination
func (r *RedundancyRepository) ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
	var peers []*domain.InstancePeer

	query := `
		SELECT * FROM instance_peers
		WHERE ($1 = false OR is_active = true)
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	err := r.db.SelectContext(ctx, &peers, query, activeOnly, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance peers: %w", err)
	}

	return peers, nil
}

// UpdateInstancePeer updates an existing instance peer
func (r *RedundancyRepository) UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	query := `
		UPDATE instance_peers
		SET
			instance_name = $2,
			software = $3,
			version = $4,
			auto_accept_redundancy = $5,
			max_redundancy_size_gb = $6,
			accepts_new_redundancy = $7,
			is_active = $8,
			actor_url = $9,
			inbox_url = $10,
			shared_inbox_url = $11,
			public_key = $12,
			updated_at = NOW()
		WHERE id = $1
		RETURNING updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		peer.ID, peer.InstanceName, peer.Software, peer.Version,
		peer.AutoAcceptRedundancy, peer.MaxRedundancySizeGB, peer.AcceptsNewRedundancy,
		peer.IsActive, peer.ActorURL, peer.InboxURL, peer.SharedInboxURL, peer.PublicKey,
	).Scan(&peer.UpdatedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrInstancePeerNotFound
		}
		return fmt.Errorf("failed to update instance peer: %w", err)
	}

	return nil
}

// UpdateInstancePeerContact updates the last contacted time
func (r *RedundancyRepository) UpdateInstancePeerContact(ctx context.Context, id string) error {
	query := `
		UPDATE instance_peers
		SET last_contacted_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to update instance peer contact: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrInstancePeerNotFound
	}

	return nil
}

// DeleteInstancePeer deletes an instance peer
func (r *RedundancyRepository) DeleteInstancePeer(ctx context.Context, id string) error {
	query := `DELETE FROM instance_peers WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete instance peer: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrInstancePeerNotFound
	}

	return nil
}

// GetActiveInstancesWithCapacity retrieves active instances that can accept redundancy
func (r *RedundancyRepository) GetActiveInstancesWithCapacity(ctx context.Context, videoSizeBytes int64) ([]*domain.InstancePeer, error) {
	var peers []*domain.InstancePeer

	query := `
		SELECT * FROM instance_peers
		WHERE is_active = true
		  AND accepts_new_redundancy = true
		  AND (
			max_redundancy_size_gb = 0 OR
			(total_storage_bytes + $1) <= (max_redundancy_size_gb::BIGINT * 1024 * 1024 * 1024)
		  )
		ORDER BY failed_sync_count ASC, last_sync_success_at DESC NULLS LAST
	`

	err := r.db.SelectContext(ctx, &peers, query, videoSizeBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get active instances with capacity: %w", err)
	}

	return peers, nil
}

// ==================== VideoRedundancy Operations ====================

// CreateVideoRedundancy creates a new video redundancy
func (r *RedundancyRepository) CreateVideoRedundancy(ctx context.Context, redundancy *domain.VideoRedundancy) error {
	query := `
		INSERT INTO video_redundancy (
			video_id, target_instance_id, strategy, status,
			file_size_bytes, priority, auto_resync, max_sync_attempts
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		) RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		redundancy.VideoID, redundancy.TargetInstanceID, redundancy.Strategy, redundancy.Status,
		redundancy.FileSizeBytes, redundancy.Priority, redundancy.AutoResync, redundancy.MaxSyncAttempts,
	).Scan(&redundancy.ID, &redundancy.CreatedAt, &redundancy.UpdatedAt)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return domain.ErrRedundancyAlreadyExists
		}
		return fmt.Errorf("failed to create video redundancy: %w", err)
	}

	return nil
}

// GetVideoRedundancyByID retrieves a video redundancy by ID
func (r *RedundancyRepository) GetVideoRedundancyByID(ctx context.Context, id string) (*domain.VideoRedundancy, error) {
	var redundancy domain.VideoRedundancy
	query := `
		SELECT * FROM video_redundancy WHERE id = $1
	`

	err := r.db.GetContext(ctx, &redundancy, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrRedundancyNotFound
		}
		return nil, fmt.Errorf("failed to get video redundancy: %w", err)
	}

	return &redundancy, nil
}

// GetVideoRedundanciesByVideoID retrieves all redundancies for a video
func (r *RedundancyRepository) GetVideoRedundanciesByVideoID(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error) {
	var redundancies []*domain.VideoRedundancy

	query := `
		SELECT * FROM video_redundancy
		WHERE video_id = $1
		ORDER BY created_at DESC
	`

	err := r.db.SelectContext(ctx, &redundancies, query, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video redundancies: %w", err)
	}

	return redundancies, nil
}

// GetVideoRedundanciesByInstanceID retrieves all redundancies for an instance
func (r *RedundancyRepository) GetVideoRedundanciesByInstanceID(ctx context.Context, instanceID string) ([]*domain.VideoRedundancy, error) {
	var redundancies []*domain.VideoRedundancy

	query := `
		SELECT * FROM video_redundancy
		WHERE target_instance_id = $1
		ORDER BY created_at DESC
	`

	err := r.db.SelectContext(ctx, &redundancies, query, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video redundancies by instance: %w", err)
	}

	return redundancies, nil
}

// ListPendingRedundancies retrieves all pending redundancies
func (r *RedundancyRepository) ListPendingRedundancies(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error) {
	var redundancies []*domain.VideoRedundancy

	query := `
		SELECT * FROM video_redundancy
		WHERE status = 'pending'
		  AND sync_attempt_count < max_sync_attempts
		ORDER BY priority DESC, created_at ASC
		LIMIT $1
	`

	err := r.db.SelectContext(ctx, &redundancies, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending redundancies: %w", err)
	}

	return redundancies, nil
}

// ListFailedRedundancies retrieves all failed redundancies that can be retried
func (r *RedundancyRepository) ListFailedRedundancies(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error) {
	var redundancies []*domain.VideoRedundancy

	query := `
		SELECT * FROM video_redundancy
		WHERE status = 'failed'
		  AND sync_attempt_count < max_sync_attempts
		  AND (next_sync_at IS NULL OR next_sync_at <= NOW())
		ORDER BY priority DESC, next_sync_at ASC NULLS FIRST
		LIMIT $1
	`

	err := r.db.SelectContext(ctx, &redundancies, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list failed redundancies: %w", err)
	}

	return redundancies, nil
}

// ListRedundanciesForResync retrieves redundancies that need checksum verification
func (r *RedundancyRepository) ListRedundanciesForResync(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error) {
	var redundancies []*domain.VideoRedundancy

	query := `
		SELECT * FROM video_redundancy
		WHERE status = 'synced'
		  AND auto_resync = true
		  AND (
			checksum_verified_at IS NULL OR
			checksum_verified_at < NOW() - INTERVAL '7 days'
		  )
		ORDER BY checksum_verified_at ASC NULLS FIRST
		LIMIT $1
	`

	err := r.db.SelectContext(ctx, &redundancies, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list redundancies for resync: %w", err)
	}

	return redundancies, nil
}

// UpdateVideoRedundancy updates an existing video redundancy
func (r *RedundancyRepository) UpdateVideoRedundancy(ctx context.Context, redundancy *domain.VideoRedundancy) error {
	query := `
		UPDATE video_redundancy
		SET
			status = $2,
			target_video_url = $3,
			target_video_id = $4,
			bytes_transferred = $5,
			transfer_speed_bps = $6,
			checksum_sha256 = $7,
			checksum_verified_at = $8,
			sync_started_at = $9,
			last_sync_at = $10,
			next_sync_at = $11,
			sync_attempt_count = $12,
			sync_error = $13,
			priority = $14,
			updated_at = NOW()
		WHERE id = $1
		RETURNING updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		redundancy.ID, redundancy.Status, redundancy.TargetVideoURL, redundancy.TargetVideoID,
		redundancy.BytesTransferred, redundancy.TransferSpeedBPS,
		redundancy.ChecksumSHA256, redundancy.ChecksumVerifiedAt,
		redundancy.SyncStartedAt, redundancy.LastSyncAt, redundancy.NextSyncAt,
		redundancy.SyncAttemptCount, redundancy.SyncError, redundancy.Priority,
	).Scan(&redundancy.UpdatedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRedundancyNotFound
		}
		return fmt.Errorf("failed to update video redundancy: %w", err)
	}

	return nil
}

// UpdateRedundancyProgress updates the sync progress
func (r *RedundancyRepository) UpdateRedundancyProgress(ctx context.Context, id string, bytesTransferred, speedBPS int64) error {
	query := `
		UPDATE video_redundancy
		SET
			bytes_transferred = $2,
			transfer_speed_bps = $3,
			updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id, bytesTransferred, speedBPS)
	if err != nil {
		return fmt.Errorf("failed to update redundancy progress: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrRedundancyNotFound
	}

	return nil
}

// DeleteVideoRedundancy deletes a video redundancy
func (r *RedundancyRepository) DeleteVideoRedundancy(ctx context.Context, id string) error {
	query := `DELETE FROM video_redundancy WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete video redundancy: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrRedundancyNotFound
	}

	return nil
}

// DeleteVideoRedundanciesByVideoID deletes all redundancies for a video
func (r *RedundancyRepository) DeleteVideoRedundanciesByVideoID(ctx context.Context, videoID string) error {
	query := `DELETE FROM video_redundancy WHERE video_id = $1`

	_, err := r.db.ExecContext(ctx, query, videoID)
	if err != nil {
		return fmt.Errorf("failed to delete video redundancies: %w", err)
	}

	return nil
}

// ==================== RedundancyPolicy Operations ====================

// CreateRedundancyPolicy creates a new redundancy policy
func (r *RedundancyRepository) CreateRedundancyPolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	query := `
		INSERT INTO redundancy_policies (
			name, description, strategy, enabled,
			min_views, min_age_days, max_age_days, privacy_types,
			target_instance_count, min_instance_count,
			max_video_size_gb, max_total_size_gb,
			evaluation_interval_hours, next_evaluation_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		) RETURNING id, created_at, updated_at
	`

	nextEval := time.Now().Add(time.Duration(policy.EvaluationIntervalHours) * time.Hour)

	err := r.db.QueryRowContext(
		ctx, query,
		policy.Name, policy.Description, policy.Strategy, policy.Enabled,
		policy.MinViews, policy.MinAgeDays, policy.MaxAgeDays, pq.Array(policy.PrivacyTypes),
		policy.TargetInstanceCount, policy.MinInstanceCount,
		policy.MaxVideoSizeGB, policy.MaxTotalSizeGB,
		policy.EvaluationIntervalHours, nextEval,
	).Scan(&policy.ID, &policy.CreatedAt, &policy.UpdatedAt)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return domain.ErrPolicyAlreadyExists
		}
		return fmt.Errorf("failed to create redundancy policy: %w", err)
	}

	policy.NextEvaluationAt = &nextEval
	return nil
}

// GetRedundancyPolicyByID retrieves a redundancy policy by ID
func (r *RedundancyRepository) GetRedundancyPolicyByID(ctx context.Context, id string) (*domain.RedundancyPolicy, error) {
	var policy domain.RedundancyPolicy
	query := `
		SELECT * FROM redundancy_policies WHERE id = $1
	`

	err := r.db.GetContext(ctx, &policy, query, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPolicyNotFound
		}
		return nil, fmt.Errorf("failed to get redundancy policy: %w", err)
	}

	return &policy, nil
}

// GetRedundancyPolicyByName retrieves a redundancy policy by name
func (r *RedundancyRepository) GetRedundancyPolicyByName(ctx context.Context, name string) (*domain.RedundancyPolicy, error) {
	var policy domain.RedundancyPolicy
	query := `
		SELECT * FROM redundancy_policies WHERE name = $1
	`

	err := r.db.GetContext(ctx, &policy, query, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPolicyNotFound
		}
		return nil, fmt.Errorf("failed to get redundancy policy: %w", err)
	}

	return &policy, nil
}

// ListRedundancyPolicies retrieves all redundancy policies
func (r *RedundancyRepository) ListRedundancyPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error) {
	var policies []*domain.RedundancyPolicy

	query := `
		SELECT * FROM redundancy_policies
		WHERE ($1 = false OR enabled = true)
		ORDER BY created_at DESC
	`

	err := r.db.SelectContext(ctx, &policies, query, enabledOnly)
	if err != nil {
		return nil, fmt.Errorf("failed to list redundancy policies: %w", err)
	}

	return policies, nil
}

// ListPoliciesToEvaluate retrieves policies that should be evaluated
func (r *RedundancyRepository) ListPoliciesToEvaluate(ctx context.Context) ([]*domain.RedundancyPolicy, error) {
	var policies []*domain.RedundancyPolicy

	query := `
		SELECT * FROM redundancy_policies
		WHERE enabled = true
		  AND (next_evaluation_at IS NULL OR next_evaluation_at <= NOW())
		ORDER BY next_evaluation_at ASC NULLS FIRST
	`

	err := r.db.SelectContext(ctx, &policies, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list policies to evaluate: %w", err)
	}

	return policies, nil
}

// UpdateRedundancyPolicy updates an existing redundancy policy
func (r *RedundancyRepository) UpdateRedundancyPolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	query := `
		UPDATE redundancy_policies
		SET
			description = $2,
			strategy = $3,
			enabled = $4,
			min_views = $5,
			min_age_days = $6,
			max_age_days = $7,
			privacy_types = $8,
			target_instance_count = $9,
			min_instance_count = $10,
			max_video_size_gb = $11,
			max_total_size_gb = $12,
			evaluation_interval_hours = $13,
			updated_at = NOW()
		WHERE id = $1
		RETURNING updated_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		policy.ID, policy.Description, policy.Strategy, policy.Enabled,
		policy.MinViews, policy.MinAgeDays, policy.MaxAgeDays, pq.Array(policy.PrivacyTypes),
		policy.TargetInstanceCount, policy.MinInstanceCount,
		policy.MaxVideoSizeGB, policy.MaxTotalSizeGB,
		policy.EvaluationIntervalHours,
	).Scan(&policy.UpdatedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrPolicyNotFound
		}
		return fmt.Errorf("failed to update redundancy policy: %w", err)
	}

	return nil
}

// UpdatePolicyEvaluationTime updates the policy's evaluation timestamps
func (r *RedundancyRepository) UpdatePolicyEvaluationTime(ctx context.Context, id string) error {
	query := `
		UPDATE redundancy_policies
		SET
			last_evaluated_at = NOW(),
			next_evaluation_at = NOW() + (evaluation_interval_hours || ' hours')::INTERVAL,
			updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to update policy evaluation time: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrPolicyNotFound
	}

	return nil
}

// DeleteRedundancyPolicy deletes a redundancy policy
func (r *RedundancyRepository) DeleteRedundancyPolicy(ctx context.Context, id string) error {
	query := `DELETE FROM redundancy_policies WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete redundancy policy: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrPolicyNotFound
	}

	return nil
}

// ==================== RedundancySyncLog Operations ====================

// CreateSyncLog creates a new sync log entry
func (r *RedundancyRepository) CreateSyncLog(ctx context.Context, log *domain.RedundancySyncLog) error {
	query := `
		INSERT INTO redundancy_sync_log (
			redundancy_id, attempt_number, started_at, completed_at,
			bytes_transferred, transfer_duration_seconds, average_speed_bps,
			success, error_message, error_type,
			http_status_code, retry_after_seconds
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		) RETURNING id, created_at
	`

	err := r.db.QueryRowContext(
		ctx, query,
		log.RedundancyID, log.AttemptNumber, log.StartedAt, log.CompletedAt,
		log.BytesTransferred, log.TransferDurationSec, log.AverageSpeedBPS,
		log.Success, log.ErrorMessage, log.ErrorType,
		log.HTTPStatusCode, log.RetryAfterSec,
	).Scan(&log.ID, &log.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create sync log: %w", err)
	}

	return nil
}

// GetSyncLogsByRedundancyID retrieves sync logs for a redundancy
func (r *RedundancyRepository) GetSyncLogsByRedundancyID(ctx context.Context, redundancyID string, limit int) ([]*domain.RedundancySyncLog, error) {
	var logs []*domain.RedundancySyncLog

	query := `
		SELECT * FROM redundancy_sync_log
		WHERE redundancy_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	err := r.db.SelectContext(ctx, &logs, query, redundancyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get sync logs: %w", err)
	}

	return logs, nil
}

// CleanupOldSyncLogs removes old sync logs (keep last 100 per redundancy)
func (r *RedundancyRepository) CleanupOldSyncLogs(ctx context.Context) (int, error) {
	query := `SELECT cleanup_old_redundancy_logs()`

	var deletedCount int
	err := r.db.QueryRowContext(ctx, query).Scan(&deletedCount)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old sync logs: %w", err)
	}

	return deletedCount, nil
}

// ==================== Statistics and Health Operations ====================

// GetRedundancyStats retrieves overall redundancy statistics
func (r *RedundancyRepository) GetRedundancyStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Video redundancy stats
	var redundancyStats struct {
		Total   int `db:"total"`
		Pending int `db:"pending"`
		Syncing int `db:"syncing"`
		Synced  int `db:"synced"`
		Failed  int `db:"failed"`
	}

	query := `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'pending') as pending,
			COUNT(*) FILTER (WHERE status = 'syncing') as syncing,
			COUNT(*) FILTER (WHERE status = 'synced') as synced,
			COUNT(*) FILTER (WHERE status = 'failed') as failed
		FROM video_redundancy
	`

	err := r.db.GetContext(ctx, &redundancyStats, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get redundancy stats: %w", err)
	}

	stats["redundancies"] = redundancyStats

	// Instance peer stats
	var instanceStats struct {
		Total  int `db:"total"`
		Active int `db:"active"`
	}

	query = `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE is_active = true) as active
		FROM instance_peers
	`

	err = r.db.GetContext(ctx, &instanceStats, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance stats: %w", err)
	}

	stats["instances"] = instanceStats

	// Policy stats
	var policyStats struct {
		Total   int `db:"total"`
		Enabled int `db:"enabled"`
	}

	query = `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE enabled = true) as enabled
		FROM redundancy_policies
	`

	err = r.db.GetContext(ctx, &policyStats, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get policy stats: %w", err)
	}

	stats["policies"] = policyStats

	return stats, nil
}

// GetVideoRedundancyHealth calculates the redundancy health score for a video
func (r *RedundancyRepository) GetVideoRedundancyHealth(ctx context.Context, videoID string) (float64, error) {
	var health float64

	query := `SELECT get_video_redundancy_health($1)`

	err := r.db.QueryRowContext(ctx, query, videoID).Scan(&health)
	if err != nil {
		return 0, fmt.Errorf("failed to get video redundancy health: %w", err)
	}

	return health, nil
}

// CheckInstanceHealth checks and marks unhealthy instances as inactive
func (r *RedundancyRepository) CheckInstanceHealth(ctx context.Context) (int, error) {
	query := `SELECT check_instance_health()`

	var updatedCount int
	err := r.db.QueryRowContext(ctx, query).Scan(&updatedCount)
	if err != nil {
		return 0, fmt.Errorf("failed to check instance health: %w", err)
	}

	return updatedCount, nil
}
