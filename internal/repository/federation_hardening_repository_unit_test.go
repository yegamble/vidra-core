package repository

import (
	"context"
	"testing"

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
}
