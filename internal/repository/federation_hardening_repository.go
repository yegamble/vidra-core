package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"athena/internal/domain"
	"github.com/jmoiron/sqlx"
)

// FederationHardeningRepository handles federation hardening data persistence
type FederationHardeningRepository struct {
	db *sqlx.DB
}

// NewFederationHardeningRepository creates a new federation hardening repository
func NewFederationHardeningRepository(db *sqlx.DB) *FederationHardeningRepository {
	return &FederationHardeningRepository{db: db}
}

// Dead Letter Queue Operations

// MoveToDLQ moves a failed job to the dead letter queue
func (r *FederationHardeningRepository) MoveToDLQ(ctx context.Context, job *domain.FederationJob, errorMsg string) error {
	dlqJob := &domain.DeadLetterJob{
		OriginalJobID: &job.ID,
		JobType:       job.JobType,
		Payload:       job.Payload,
		ErrorMessage:  &errorMsg,
		ErrorCount:    job.Attempts,
		LastErrorAt:   time.Now(),
		CreatedAt:     time.Now(),
		CanRetry:      job.Attempts < job.MaxAttempts,
	}

	query := `
		INSERT INTO federation_dlq (
			original_job_id, job_type, payload, error_message,
			error_count, last_error_at, created_at, can_retry, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	metadata, _ := json.Marshal(map[string]interface{}{
		"original_attempts": job.Attempts,
		"max_attempts":      job.MaxAttempts,
		"last_error":        job.LastError,
	})

	return r.db.GetContext(ctx, &dlqJob.ID, query,
		dlqJob.OriginalJobID, dlqJob.JobType, dlqJob.Payload,
		dlqJob.ErrorMessage, dlqJob.ErrorCount, dlqJob.LastErrorAt,
		dlqJob.CreatedAt, dlqJob.CanRetry, metadata,
	)
}

// GetDLQJobs retrieves jobs from the dead letter queue
func (r *FederationHardeningRepository) GetDLQJobs(ctx context.Context, limit int, canRetryOnly bool) ([]domain.DeadLetterJob, error) {
	query := `SELECT * FROM federation_dlq`
	if canRetryOnly {
		query += ` WHERE can_retry = true`
	}
	query += ` ORDER BY created_at DESC LIMIT $1`

	var jobs []domain.DeadLetterJob
	err := r.db.SelectContext(ctx, &jobs, query, limit)
	return jobs, err
}

// RetryDLQJob attempts to retry a job from the DLQ
func (r *FederationHardeningRepository) RetryDLQJob(ctx context.Context, dlqID string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Get the DLQ job
	var dlqJob domain.DeadLetterJob
	err = tx.GetContext(ctx, &dlqJob, `SELECT * FROM federation_dlq WHERE id = $1`, dlqID)
	if err != nil {
		return err
	}

	// Re-enqueue the job
	query := `
		INSERT INTO federation_jobs (job_type, payload, status, attempts, max_attempts, next_attempt_at)
		VALUES ($1, $2, 'pending', 0, 5, CURRENT_TIMESTAMP)`
	_, err = tx.ExecContext(ctx, query, dlqJob.JobType, dlqJob.Payload)
	if err != nil {
		return err
	}

	// Update DLQ entry
	_, err = tx.ExecContext(ctx, `UPDATE federation_dlq SET can_retry = false WHERE id = $1`, dlqID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Blocklist Operations

// AddInstanceBlock adds an instance to the blocklist
func (r *FederationHardeningRepository) AddInstanceBlock(ctx context.Context, block *domain.InstanceBlock) error {
	query := `
		INSERT INTO federation_instance_blocks (
			instance_domain, reason, severity, blocked_by, created_at, expires_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (instance_domain) DO UPDATE SET
			reason = EXCLUDED.reason,
			severity = EXCLUDED.severity,
			blocked_by = EXCLUDED.blocked_by,
			expires_at = EXCLUDED.expires_at,
			metadata = EXCLUDED.metadata
		RETURNING id`

	return r.db.GetContext(ctx, &block.ID, query,
		block.InstanceDomain, block.Reason, block.Severity,
		block.BlockedBy, block.CreatedAt, block.ExpiresAt, block.Metadata,
	)
}

// RemoveInstanceBlock removes an instance from the blocklist
func (r *FederationHardeningRepository) RemoveInstanceBlock(ctx context.Context, domain string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM federation_instance_blocks WHERE instance_domain = $1`, domain)
	return err
}

// IsInstanceBlocked checks if an instance is blocked
func (r *FederationHardeningRepository) IsInstanceBlocked(ctx context.Context, domain string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM federation_instance_blocks
			WHERE instance_domain = $1
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, domain)
	return exists, err
}

// GetInstanceBlocks retrieves all instance blocks
func (r *FederationHardeningRepository) GetInstanceBlocks(ctx context.Context) ([]domain.InstanceBlock, error) {
	query := `
		SELECT * FROM federation_instance_blocks
		WHERE expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP
		ORDER BY created_at DESC`
	var blocks []domain.InstanceBlock
	err := r.db.SelectContext(ctx, &blocks, query)
	return blocks, err
}

// AddActorBlock adds an actor to the blocklist
func (r *FederationHardeningRepository) AddActorBlock(ctx context.Context, block *domain.ActorBlock) error {
	query := `
		INSERT INTO federation_actor_blocks (
			actor_did, actor_handle, reason, severity, blocked_by, created_at, expires_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (actor_did) DO UPDATE SET
			actor_handle = EXCLUDED.actor_handle,
			reason = EXCLUDED.reason,
			severity = EXCLUDED.severity,
			blocked_by = EXCLUDED.blocked_by,
			expires_at = EXCLUDED.expires_at,
			metadata = EXCLUDED.metadata
		RETURNING id`

	return r.db.GetContext(ctx, &block.ID, query,
		block.ActorDID, block.ActorHandle, block.Reason, block.Severity,
		block.BlockedBy, block.CreatedAt, block.ExpiresAt, block.Metadata,
	)
}

// IsActorBlocked checks if an actor is blocked
func (r *FederationHardeningRepository) IsActorBlocked(ctx context.Context, did, handle string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM federation_actor_blocks
			WHERE (actor_did = $1 OR actor_handle = $2)
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, did, handle)
	return exists, err
}

// Metrics Operations

// RecordMetric records a federation metric
func (r *FederationHardeningRepository) RecordMetric(ctx context.Context, metric *domain.FederationMetric) error {
	query := `
		INSERT INTO federation_metrics (
			metric_type, metric_value, instance_domain, actor_did,
			job_type, timestamp, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	return r.db.GetContext(ctx, &metric.ID, query,
		metric.MetricType, metric.MetricValue, metric.InstanceDomain,
		metric.ActorDID, metric.JobType, metric.Timestamp, metric.Metadata,
	)
}

// GetMetrics retrieves metrics for a time range
func (r *FederationHardeningRepository) GetMetrics(ctx context.Context, metricType string, since time.Time, limit int) ([]domain.FederationMetric, error) {
	query := `
		SELECT * FROM federation_metrics
		WHERE metric_type = $1 AND timestamp > $2
		ORDER BY timestamp DESC
		LIMIT $3`

	var metrics []domain.FederationMetric
	err := r.db.SelectContext(ctx, &metrics, query, metricType, since, limit)
	return metrics, err
}

// GetHealthSummary retrieves the federation health summary
func (r *FederationHardeningRepository) GetHealthSummary(ctx context.Context) ([]domain.FederationHealthSummary, error) {
	query := `SELECT * FROM federation_health_summary ORDER BY hour DESC, metric_type`
	var summary []domain.FederationHealthSummary
	err := r.db.SelectContext(ctx, &summary, query)
	return summary, err
}

// RefreshHealthSummary refreshes the materialized view
func (r *FederationHardeningRepository) RefreshHealthSummary(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `SELECT refresh_federation_health()`)
	return err
}

// Idempotency Operations

// CheckIdempotency checks if an operation has been performed
func (r *FederationHardeningRepository) CheckIdempotency(ctx context.Context, key string) (*domain.IdempotencyRecord, error) {
	var record domain.IdempotencyRecord
	query := `
		SELECT * FROM federation_idempotency
		WHERE idempotency_key = $1 AND expires_at > CURRENT_TIMESTAMP`
	err := r.db.GetContext(ctx, &record, query, key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &record, err
}

// RecordIdempotency records an idempotent operation
func (r *FederationHardeningRepository) RecordIdempotency(ctx context.Context, record *domain.IdempotencyRecord) error {
	query := `
		INSERT INTO federation_idempotency (
			idempotency_key, operation_type, payload, result, status, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			result = EXCLUDED.result,
			status = EXCLUDED.status`

	_, err := r.db.ExecContext(ctx, query,
		record.IdempotencyKey, record.OperationType, record.Payload,
		record.Result, record.Status, record.CreatedAt, record.ExpiresAt,
	)
	return err
}

// Security Operations

// CheckRequestSignature checks if a request signature has been seen
func (r *FederationHardeningRepository) CheckRequestSignature(ctx context.Context, signatureHash string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM federation_request_signatures
			WHERE signature_hash = $1 AND expires_at > CURRENT_TIMESTAMP
		)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, signatureHash)
	return exists, err
}

// RecordRequestSignature records a request signature
func (r *FederationHardeningRepository) RecordRequestSignature(ctx context.Context, sig *domain.RequestSignature) error {
	query := `
		INSERT INTO federation_request_signatures (
			signature_hash, instance_domain, request_path, received_at, expires_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (signature_hash) DO NOTHING`

	_, err := r.db.ExecContext(ctx, query,
		sig.SignatureHash, sig.InstanceDomain, sig.RequestPath,
		sig.ReceivedAt, sig.ExpiresAt,
	)
	return err
}

// Rate Limiting Operations

// CheckRateLimit checks and updates rate limit for an ID
func (r *FederationHardeningRepository) CheckRateLimit(ctx context.Context, id string, limit int, window time.Duration) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var entry domain.RateLimitEntry
	err = tx.GetContext(ctx, &entry, `
		SELECT * FROM federation_rate_limits
		WHERE id = $1
		FOR UPDATE`, id)

	now := time.Now()
	if err == sql.ErrNoRows {
		// Create new entry
		entry = domain.RateLimitEntry{
			ID:           id,
			RequestCount: 1,
			WindowStart:  now,
			LastRequest:  now,
			IsBlocked:    false,
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO federation_rate_limits (id, request_count, window_start, last_request, is_blocked)
			VALUES ($1, $2, $3, $4, $5)`,
			entry.ID, entry.RequestCount, entry.WindowStart, entry.LastRequest, entry.IsBlocked,
		)
		if err != nil {
			return false, err
		}
		return tx.Commit() == nil, nil
	}

	if err != nil {
		return false, err
	}

	// Check if blocked
	if entry.IsBlocked && entry.BlockedUntil != nil && entry.BlockedUntil.After(now) {
		return false, nil
	}

	// Check window
	if now.Sub(entry.WindowStart) > window {
		// Reset window
		entry.RequestCount = 1
		entry.WindowStart = now
		entry.IsBlocked = false
		entry.BlockedUntil = nil
	} else {
		entry.RequestCount++
		if entry.RequestCount > limit {
			// Block for remaining window time
			blockedUntil := entry.WindowStart.Add(window)
			entry.IsBlocked = true
			entry.BlockedUntil = &blockedUntil
		}
	}

	entry.LastRequest = now

	_, err = tx.ExecContext(ctx, `
		UPDATE federation_rate_limits
		SET request_count = $2, window_start = $3, last_request = $4, is_blocked = $5, blocked_until = $6
		WHERE id = $1`,
		entry.ID, entry.RequestCount, entry.WindowStart, entry.LastRequest, entry.IsBlocked, entry.BlockedUntil,
	)
	if err != nil {
		return false, err
	}

	err = tx.Commit()
	return !entry.IsBlocked, err
}

// Abuse Reporting Operations

// CreateAbuseReport creates a new abuse report
func (r *FederationHardeningRepository) CreateAbuseReport(ctx context.Context, report *domain.AbuseReport) error {
	query := `
		INSERT INTO federation_abuse_reports (
			reporter_did, reported_content_uri, reported_actor_did,
			report_type, description, evidence, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	return r.db.GetContext(ctx, &report.ID, query,
		report.ReporterDID, report.ReportedContentURI, report.ReportedActorDID,
		report.ReportType, report.Description, report.Evidence,
		report.Status, report.CreatedAt, report.UpdatedAt,
	)
}

// GetAbuseReports retrieves abuse reports by status
func (r *FederationHardeningRepository) GetAbuseReports(ctx context.Context, status string, limit int) ([]domain.AbuseReport, error) {
	query := `
		SELECT * FROM federation_abuse_reports
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2`

	var reports []domain.AbuseReport
	err := r.db.SelectContext(ctx, &reports, query, status, limit)
	return reports, err
}

// UpdateAbuseReport updates an abuse report status
func (r *FederationHardeningRepository) UpdateAbuseReport(ctx context.Context, id, status, resolution, resolvedBy string) error {
	now := time.Now()
	query := `
		UPDATE federation_abuse_reports
		SET status = $2, resolution = $3, resolved_by = $4, resolved_at = $5, updated_at = $6
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id, status, resolution, resolvedBy, now, now)
	return err
}

// Cleanup Operations

// CleanupExpired removes expired data
func (r *FederationHardeningRepository) CleanupExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `SELECT cleanup_federation_expired()`)
	return err
}

// GetBackoffDelay calculates exponential backoff delay
func CalculateBackoffDelay(attempts int, config domain.BackoffConfig) time.Duration {
	if attempts <= 0 {
		return config.InitialDelay
	}

	delay := config.InitialDelay
	for i := 0; i < attempts; i++ {
		delay = time.Duration(float64(delay) * config.Multiplier)
		if delay > config.MaxDelay {
			return config.MaxDelay
		}
	}
	return delay
}

// GetFederationConfig retrieves federation configuration from instance config
func (r *FederationHardeningRepository) GetFederationConfig(ctx context.Context) (*domain.FederationSecurityConfig, error) {
	config := &domain.FederationSecurityConfig{
		MaxRequestSize:         10485760, // 10MB default
		SignatureWindowSeconds: 300,      // 5 minutes
		RateLimitRequests:      1000,
		RateLimitWindow:        time.Hour,
		EnableAbuseReporting:   true,
		MetricsEnabled:         true,
	}

	// Load from instance_config table
	query := `SELECT key, value FROM instance_config WHERE key LIKE 'federation_%'`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return config, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}

		switch key {
		case "federation_max_request_size":
			var size int64
			if err := json.Unmarshal(value, &size); err == nil {
				config.MaxRequestSize = size
			}
		case "federation_signature_window_seconds":
			var seconds int
			if err := json.Unmarshal(value, &seconds); err == nil {
				config.SignatureWindowSeconds = seconds
			}
		case "federation_rate_limit_requests":
			var requests int
			if err := json.Unmarshal(value, &requests); err == nil {
				config.RateLimitRequests = requests
			}
		case "federation_rate_limit_window_seconds":
			var seconds int
			if err := json.Unmarshal(value, &seconds); err == nil {
				config.RateLimitWindow = time.Duration(seconds) * time.Second
			}
		case "federation_enable_abuse_reporting":
			var enabled bool
			if err := json.Unmarshal(value, &enabled); err == nil {
				config.EnableAbuseReporting = enabled
			}
		case "federation_metrics_enabled":
			var enabled bool
			if err := json.Unmarshal(value, &enabled); err == nil {
				config.MetricsEnabled = enabled
			}
		}
	}

	return config, nil
}

// UpdateJobWithBackoff updates a job with exponential backoff
func (r *FederationHardeningRepository) UpdateJobWithBackoff(ctx context.Context, jobID string, attempts int, lastError string) error {
	config := domain.BackoffConfig{
		InitialDelay: 5 * time.Second,
		MaxDelay:     time.Hour,
		Multiplier:   1.5,
		MaxRetries:   5,
	}

	delay := CalculateBackoffDelay(attempts, config)
	nextAttempt := time.Now().Add(delay)

	query := `
		UPDATE federation_jobs
		SET attempts = $2, last_error = $3, next_attempt_at = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, jobID, attempts, lastError, nextAttempt)
	return err
}

// GetJobsForProcessing retrieves jobs ready for processing with backoff consideration
func (r *FederationHardeningRepository) GetJobsForProcessing(ctx context.Context, limit int) ([]domain.FederationJob, error) {
	query := `
		SELECT * FROM federation_jobs
		WHERE status IN ('pending', 'processing')
		AND next_attempt_at <= CURRENT_TIMESTAMP
		AND attempts < max_attempts
		ORDER BY next_attempt_at ASC
		LIMIT $1`

	var jobs []domain.FederationJob
	err := r.db.SelectContext(ctx, &jobs, query, limit)
	return jobs, err
}
