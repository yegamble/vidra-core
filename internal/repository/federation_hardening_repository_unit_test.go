package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestFederationHardeningRepository_BasicCoverage(t *testing.T) {
	ctx := context.Background()

	t.Run("constructor", func(t *testing.T) {
		mockDB, _, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)
		require.NotNil(t, repo)
	})

	t.Run("move to DLQ", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		errorMsg := "test error"
		job := &domain.FederationJob{
			ID:          uuid.NewString(),
			JobType:     "deliver",
			Payload:     []byte(`{}`),
			Attempts:    3,
			MaxAttempts: 5,
			LastError:   &errorMsg,
		}

		dlqID := uuid.NewString()
		mock.ExpectQuery(`INSERT INTO federation_dlq`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(dlqID))

		err = repo.MoveToDLQ(ctx, job, "error")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("add instance block", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		reason := "spam"
		block := &domain.InstanceBlock{
			InstanceDomain: "bad.example.com",
			Reason:         &reason,
		}

		blockID := uuid.NewString()
		mock.ExpectQuery(`INSERT INTO federation_instance_blocks`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(blockID))

		err = repo.AddInstanceBlock(ctx, block)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is instance blocked", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		blocked, err := repo.IsInstanceBlocked(ctx, "bad.example.com")
		require.NoError(t, err)
		require.True(t, blocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("add actor block", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		did := "did:plc:test"
		handle := "test@example.com"
		reason := "spam"
		block := &domain.ActorBlock{
			ActorDID:    &did,
			ActorHandle: &handle,
			Reason:      &reason,
		}

		blockID := uuid.NewString()
		mock.ExpectQuery(`INSERT INTO federation_actor_blocks`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(blockID))

		err = repo.AddActorBlock(ctx, block)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("record metric", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		metric := &domain.FederationMetric{
			MetricType:  "delivery",
			MetricValue: 1.0,
		}

		metricID := uuid.NewString()
		mock.ExpectQuery(`INSERT INTO federation_metrics`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(metricID))

		err = repo.RecordMetric(ctx, metric)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("check idempotency not found", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT \* FROM federation_idempotency`).
			WillReturnError(sqlmock.ErrCancelled)

		record, err := repo.CheckIdempotency(ctx, "test-key")
		_ = err
		_ = record
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("check request signature", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		exists, err := repo.CheckRequestSignature(ctx, "hash")
		require.NoError(t, err)
		require.False(t, exists)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create abuse report", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		reportedActorDID := "did:plc:test"
		reporterDID := "did:plc:reporter"
		description := "spam"
		report := &domain.FederationAbuseReport{
			ReportedActorDID: &reportedActorDID,
			ReporterDID:      &reporterDID,
			ReportType:       "spam",
			Description:      &description,
		}

		reportID := uuid.NewString()
		mock.ExpectQuery(`INSERT INTO federation_abuse_reports`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(reportID))

		err = repo.CreateAbuseReport(ctx, report)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get DLQ jobs - success", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		jobID := uuid.NewString()
		errorMsg := "test error"
		now := time.Now()
		mock.ExpectQuery(`SELECT \* FROM federation_dlq`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "original_job_id", "job_type", "payload", "error_message", "error_count", "last_error_at", "created_at", "can_retry", "metadata"}).
				AddRow(jobID, nil, "deliver", []byte(`{}`), errorMsg, 3, now, now, true, []byte(`{}`)))

		jobs, err := repo.GetDLQJobs(ctx, 10, false)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		require.Equal(t, jobID, jobs[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get DLQ jobs - can retry only", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT \* FROM federation_dlq WHERE can_retry = true`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "original_job_id", "job_type", "payload", "error_message", "error_count", "last_error_at", "created_at", "can_retry", "metadata"}))

		jobs, err := repo.GetDLQJobs(ctx, 10, true)
		require.NoError(t, err)
		require.Empty(t, jobs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("retry DLQ job - success", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		dlqID := uuid.NewString()
		now := time.Now()
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT \* FROM federation_dlq WHERE id`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "original_job_id", "job_type", "payload", "error_message", "error_count", "last_error_at", "created_at", "can_retry", "metadata"}).
				AddRow(dlqID, nil, "deliver", []byte(`{}`), "error", 2, now, now, true, []byte(`{}`)))
		mock.ExpectExec(`INSERT INTO federation_jobs`).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`UPDATE federation_dlq SET can_retry`).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err = repo.RetryDLQJob(ctx, dlqID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("remove instance block", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectExec(`DELETE FROM federation_instance_blocks WHERE instance_domain`).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.RemoveInstanceBlock(ctx, "bad.example.com")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get instance blocks", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		blockID := uuid.NewString()
		reason := "spam"
		now := time.Now()
		mock.ExpectQuery(`SELECT \* FROM federation_instance_blocks`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "instance_domain", "reason", "severity", "blocked_by", "created_at", "expires_at", "metadata"}).
				AddRow(blockID, "bad.example.com", reason, "block", nil, now, nil, []byte(`{}`)))

		blocks, err := repo.GetInstanceBlocks(ctx)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		require.Equal(t, "bad.example.com", blocks[0].InstanceDomain)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is actor blocked - blocked", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		blocked, err := repo.IsActorBlocked(ctx, "did:plc:test", "test@example.com")
		require.NoError(t, err)
		require.True(t, blocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is actor blocked - not blocked", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT EXISTS`).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		blocked, err := repo.IsActorBlocked(ctx, "did:plc:test", "test@example.com")
		require.NoError(t, err)
		require.False(t, blocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get metrics", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		metricID := uuid.NewString()
		now := time.Now()
		mock.ExpectQuery(`SELECT \* FROM federation_metrics`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "metric_type", "metric_value", "instance_domain", "actor_did", "job_type", "timestamp", "metadata"}).
				AddRow(metricID, "delivery", 1.0, nil, nil, nil, now, []byte(`{}`)))

		since := time.Now().Add(-time.Hour)
		metrics, err := repo.GetMetrics(ctx, "delivery", since, 10)
		require.NoError(t, err)
		require.Len(t, metrics, 1)
		require.Equal(t, "delivery", metrics[0].MetricType)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get health summary", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		now := time.Now()
		mock.ExpectQuery(`SELECT \* FROM federation_health_summary`).
			WillReturnRows(sqlmock.NewRows([]string{"hour", "metric_type", "event_count", "avg_value", "min_value", "max_value", "median_value", "p95_value"}).
				AddRow(now, "delivery", 100, 50.0, 10.0, 200.0, 45.0, 100.0))

		summary, err := repo.GetHealthSummary(ctx)
		require.NoError(t, err)
		require.Len(t, summary, 1)
		require.Equal(t, "delivery", summary[0].MetricType)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("refresh health summary", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectExec(`SELECT refresh_federation_health`).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = repo.RefreshHealthSummary(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("record idempotency", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		record := &domain.IdempotencyRecord{
			IdempotencyKey: "test-key",
			OperationType:  "deliver",
			Status:         "success",
		}

		mock.ExpectExec(`INSERT INTO federation_idempotency`).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.RecordIdempotency(ctx, record)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("record request signature", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		requestPath := "/inbox"
		sig := &domain.RequestSignature{
			SignatureHash:  "hash123",
			InstanceDomain: "example.com",
			RequestPath:    &requestPath,
		}

		mock.ExpectExec(`INSERT INTO federation_request_signatures`).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.RecordRequestSignature(ctx, sig)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("check rate limit - new entry allowed", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT \* FROM federation_rate_limits`).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectExec(`INSERT INTO federation_rate_limits`).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		allowed, err := repo.CheckRateLimit(ctx, "test-id", 100, time.Hour)
		require.NoError(t, err)
		require.True(t, allowed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get abuse reports", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		reportID := uuid.NewString()
		reporterDID := "did:plc:reporter"
		now := time.Now()
		mock.ExpectQuery(`SELECT \* FROM federation_abuse_reports`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "reporter_did", "reported_content_uri", "reported_actor_did", "report_type", "description", "evidence", "status", "created_at", "updated_at", "resolution", "resolved_by", "resolved_at"}).
				AddRow(reportID, reporterDID, nil, nil, "spam", nil, []byte(`{}`), "pending", now, now, nil, nil, nil))

		reports, err := repo.GetAbuseReports(ctx, "pending", 10)
		require.NoError(t, err)
		require.Len(t, reports, 1)
		require.Equal(t, "pending", reports[0].Status)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update abuse report", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		reportID := uuid.NewString()
		mock.ExpectExec(`UPDATE federation_abuse_reports`).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.UpdateAbuseReport(ctx, reportID, "resolved", "confirmed spam", "admin123")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cleanup expired", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectExec(`SELECT cleanup_federation_expired`).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = repo.CleanupExpired(ctx)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get federation config", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		mock.ExpectQuery(`SELECT key, value FROM instance_config`).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
				AddRow("federation_max_request_size", []byte("20971520")).
				AddRow("federation_rate_limit_requests", []byte("2000")))

		config, err := repo.GetFederationConfig(ctx)
		require.NoError(t, err)
		require.NotNil(t, config)
		require.Equal(t, int64(20971520), config.MaxRequestSize)
		require.Equal(t, 2000, config.RateLimitRequests)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update job with backoff", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		jobID := uuid.NewString()
		mock.ExpectExec(`UPDATE federation_jobs`).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.UpdateJobWithBackoff(ctx, jobID, 2, "timeout error")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get jobs for processing", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		db := sqlx.NewDb(mockDB, "sqlmock")
		repo := NewFederationHardeningRepository(db)

		jobID := uuid.NewString()
		now := time.Now()
		mock.ExpectQuery(`SELECT \* FROM federation_jobs`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "job_type", "payload", "status", "attempts", "max_attempts", "next_attempt_at", "last_error", "created_at", "updated_at"}).
				AddRow(jobID, "deliver", []byte(`{}`), "pending", 0, 5, now, nil, now, now))

		jobs, err := repo.GetJobsForProcessing(ctx, 10)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		require.Equal(t, "pending", string(jobs[0].Status))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCalculateBackoffDelay(t *testing.T) {
	config := domain.BackoffConfig{
		InitialDelay: 5 * time.Second,
		MaxDelay:     time.Hour,
		Multiplier:   1.5,
		MaxRetries:   5,
	}

	tests := []struct {
		name     string
		attempts int
		want     time.Duration
	}{
		{"zero attempts", 0, 5 * time.Second},
		{"one attempt", 1, 7500 * time.Millisecond},
		{"two attempts", 2, 11250 * time.Millisecond},
		{"max delay exceeded", 100, time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateBackoffDelay(tt.attempts, config)
			require.Equal(t, tt.want, got)
		})
	}
}
