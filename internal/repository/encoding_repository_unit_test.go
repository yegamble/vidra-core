package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupEncodingMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newEncodingRepo(t *testing.T) (usecase_EncodingRepo, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupEncodingMockDB(t)
	repo := NewEncodingRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

type usecase_EncodingRepo = interface {
	CreateJob(ctx context.Context, job *domain.EncodingJob) error
	GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error)
	GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error)
	GetJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error)
	GetActiveJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error)
	UpdateJob(ctx context.Context, job *domain.EncodingJob) error
	DeleteJob(ctx context.Context, jobID string) error
	GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error)
	GetNextJob(ctx context.Context) (*domain.EncodingJob, error)
	ResetStaleJobs(ctx context.Context, olderThan time.Duration) (int64, error)
	UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error
	UpdateJobProgress(ctx context.Context, jobID string, progress int) error
	SetJobError(ctx context.Context, jobID string, errorMsg string) error
	GetJobCounts(ctx context.Context) (map[string]int64, error)
}

func sampleEncodingJob() *domain.EncodingJob {
	now := time.Now()
	return &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/path/to/source.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"1080p", "720p", "480p"},
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		ErrorMessage:      "",
		StartedAt:         nil,
		CompletedAt:       nil,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

var encodingJobColumns = []string{
	"id", "video_id", "source_file_path", "source_resolution",
	"target_resolutions", "status", "progress", "error_message",
	"started_at", "completed_at", "created_at", "updated_at",
}

func makeEncodingJobRows(job *domain.EncodingJob) *sqlmock.Rows {
	return sqlmock.NewRows(encodingJobColumns).AddRow(
		job.ID, job.VideoID, job.SourceFilePath, job.SourceResolution,
		pq.Array(job.TargetResolutions), job.Status, job.Progress, job.ErrorMessage,
		job.StartedAt, job.CompletedAt, job.CreatedAt, job.UpdatedAt,
	)
}

func TestEncodingRepository_Unit_CreateJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectExec(regexp.QuoteMeta(
			`INSERT INTO encoding_jobs (`)).
			WithArgs(
				job.ID, job.VideoID, job.SourceFilePath, job.SourceResolution,
				pq.Array(job.TargetResolutions), job.Status, job.Progress,
				job.ErrorMessage, job.StartedAt, job.CompletedAt,
				job.CreatedAt, job.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateJob(ctx, job)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectExec(regexp.QuoteMeta(
			`INSERT INTO encoding_jobs (`)).
			WithArgs(
				job.ID, job.VideoID, job.SourceFilePath, job.SourceResolution,
				pq.Array(job.TargetResolutions), job.Status, job.Progress,
				job.ErrorMessage, job.StartedAt, job.CompletedAt,
				job.CreatedAt, job.UpdatedAt,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateJob(ctx, job)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create encoding job")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(job.ID).
			WillReturnRows(makeEncodingJobRows(job))

		got, err := repo.GetJob(ctx, job.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, job.ID, got.ID)
		assert.Equal(t, job.VideoID, got.VideoID)
		assert.Equal(t, job.TargetResolutions, got.TargetResolutions)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs("missing-id").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetJob(ctx, "missing-id")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs("some-id").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetJob(ctx, "some-id")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get encoding job")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetJobByVideoID(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(job.VideoID).
			WillReturnRows(makeEncodingJobRows(job))

		got, err := repo.GetJobByVideoID(ctx, job.VideoID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, job.ID, got.ID)
		assert.Equal(t, job.VideoID, got.VideoID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs("missing-video").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetJobByVideoID(ctx, "missing-video")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs("vid-id").
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetJobByVideoID(ctx, "vid-id")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get encoding job by video ID")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_UpdateJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		job.Status = domain.EncodingStatusProcessing
		job.Progress = 50

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs SET`)).
			WithArgs(
				job.ID, job.SourceFilePath, job.SourceResolution,
				pq.Array(job.TargetResolutions), job.Status, job.Progress,
				job.ErrorMessage, job.StartedAt, job.CompletedAt, job.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateJob(ctx, job)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs SET`)).
			WithArgs(
				job.ID, job.SourceFilePath, job.SourceResolution,
				pq.Array(job.TargetResolutions), job.Status, job.Progress,
				job.ErrorMessage, job.StartedAt, job.CompletedAt, job.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateJob(ctx, job)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs SET`)).
			WithArgs(
				job.ID, job.SourceFilePath, job.SourceResolution,
				pq.Array(job.TargetResolutions), job.Status, job.Progress,
				job.ErrorMessage, job.StartedAt, job.CompletedAt, job.UpdatedAt,
			).
			WillReturnError(errors.New("update failed"))

		err := repo.UpdateJob(ctx, job)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update encoding job")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs SET`)).
			WithArgs(
				job.ID, job.SourceFilePath, job.SourceResolution,
				pq.Array(job.TargetResolutions), job.Status, job.Progress,
				job.ErrorMessage, job.StartedAt, job.CompletedAt, job.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := repo.UpdateJob(ctx, job)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_DeleteJob(t *testing.T) {
	ctx := context.Background()
	jobID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM encoding_jobs WHERE id = $1`)).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteJob(ctx, jobID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM encoding_jobs WHERE id = $1`)).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteJob(ctx, jobID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM encoding_jobs WHERE id = $1`)).
			WithArgs(jobID).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteJob(ctx, jobID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete encoding job")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM encoding_jobs WHERE id = $1`)).
			WithArgs(jobID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.DeleteJob(ctx, jobID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetPendingJobs(t *testing.T) {
	ctx := context.Background()

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job1 := sampleEncodingJob()
		job2 := sampleEncodingJob()

		rows := makeEncodingJobRows(job1)
		rows.AddRow(
			job2.ID, job2.VideoID, job2.SourceFilePath, job2.SourceResolution,
			pq.Array(job2.TargetResolutions), job2.Status, job2.Progress,
			job2.ErrorMessage, job2.StartedAt, job2.CompletedAt,
			job2.CreatedAt, job2.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(5).
			WillReturnRows(rows)

		got, err := repo.GetPendingJobs(ctx, 5)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, job1.ID, got[0].ID)
		assert.Equal(t, job2.ID, got[1].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with empty results", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(10).
			WillReturnRows(sqlmock.NewRows(encodingJobColumns))

		got, err := repo.GetPendingJobs(ctx, 10)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("default limit when zero", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(10).
			WillReturnRows(sqlmock.NewRows(encodingJobColumns))

		got, err := repo.GetPendingJobs(ctx, 0)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("default limit when negative", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(10).
			WillReturnRows(sqlmock.NewRows(encodingJobColumns))

		got, err := repo.GetPendingJobs(ctx, -1)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(10).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetPendingJobs(ctx, 10)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get pending jobs")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		badRows := sqlmock.NewRows([]string{"id"}).AddRow("only-one-col")
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WithArgs(10).
			WillReturnRows(badRows)

		got, err := repo.GetPendingJobs(ctx, 10)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan encoding job")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetNextJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WillReturnRows(makeEncodingJobRows(job))
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(job.ID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		got, err := repo.GetNextJob(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, job.ID, got.ID)
		assert.Equal(t, domain.EncodingStatusProcessing, got.Status)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no pending jobs returns nil", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()

		got, err := repo.GetNextJob(ctx)
		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin transaction failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		got, err := repo.GetNextJob(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to begin transaction")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select query failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WillReturnError(errors.New("select failed"))
		mock.ExpectRollback()

		got, err := repo.GetNextJob(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get next job")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update status failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WillReturnRows(makeEncodingJobRows(job))
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(job.ID).
			WillReturnError(errors.New("update failed"))
		mock.ExpectRollback()

		got, err := repo.GetNextJob(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update job status")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		job := sampleEncodingJob()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, video_id, source_file_path, source_resolution,`)).
			WillReturnRows(makeEncodingJobRows(job))
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(job.ID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

		got, err := repo.GetNextJob(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to commit transaction")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_UpdateJobStatus(t *testing.T) {
	ctx := context.Background()
	jobID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, domain.EncodingStatusCompleted).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateJobStatus(ctx, jobID, domain.EncodingStatusCompleted)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, domain.EncodingStatusProcessing).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateJobStatus(ctx, jobID, domain.EncodingStatusProcessing)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, domain.EncodingStatusFailed).
			WillReturnError(errors.New("exec failed"))

		err := repo.UpdateJobStatus(ctx, jobID, domain.EncodingStatusFailed)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update job status")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, domain.EncodingStatusCompleted).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := repo.UpdateJobStatus(ctx, jobID, domain.EncodingStatusCompleted)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_UpdateJobProgress(t *testing.T) {
	ctx := context.Background()
	jobID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, 75).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateJobProgress(ctx, jobID, 75)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, 50).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateJobProgress(ctx, jobID, 50)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, 25).
			WillReturnError(errors.New("exec failed"))

		err := repo.UpdateJobProgress(ctx, jobID, 25)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update job progress")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, 100).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := repo.UpdateJobProgress(ctx, jobID, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetJobCounts(t *testing.T) {
	ctx := context.Background()

	t.Run("success with all statuses", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"status", "count"}).
			AddRow("pending", 5).
			AddRow("processing", 2).
			AddRow("completed", 10).
			AddRow("failed", 1)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT status, COUNT(*) FROM encoding_jobs GROUP BY status`)).
			WillReturnRows(rows)

		got, err := repo.GetJobCounts(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, int64(5), got["pending"])
		assert.Equal(t, int64(2), got["processing"])
		assert.Equal(t, int64(10), got["completed"])
		assert.Equal(t, int64(1), got["failed"])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with partial statuses keeps defaults", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"status", "count"}).
			AddRow("pending", 3)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT status, COUNT(*) FROM encoding_jobs GROUP BY status`)).
			WillReturnRows(rows)

		got, err := repo.GetJobCounts(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, int64(3), got["pending"])
		assert.Equal(t, int64(0), got["processing"])
		assert.Equal(t, int64(0), got["completed"])
		assert.Equal(t, int64(0), got["failed"])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with no rows", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"status", "count"})

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT status, COUNT(*) FROM encoding_jobs GROUP BY status`)).
			WillReturnRows(rows)

		got, err := repo.GetJobCounts(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, int64(0), got["pending"])
		assert.Equal(t, int64(0), got["processing"])
		assert.Equal(t, int64(0), got["completed"])
		assert.Equal(t, int64(0), got["failed"])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT status, COUNT(*) FROM encoding_jobs GROUP BY status`)).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetJobCounts(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to count jobs")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		badRows := sqlmock.NewRows([]string{"status", "count"}).
			AddRow("pending", "not-an-int")

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT status, COUNT(*) FROM encoding_jobs GROUP BY status`)).
			WillReturnRows(badRows)

		got, err := repo.GetJobCounts(ctx)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan count")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_SetJobError(t *testing.T) {
	ctx := context.Background()
	jobID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, "encoding failed: bad format").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetJobError(ctx, jobID, "encoding failed: bad format")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, "some error").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.SetJobError(ctx, jobID, "some error")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, "error msg").
			WillReturnError(errors.New("exec failed"))

		err := repo.SetJobError(ctx, jobID, "error msg")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to set job error")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE encoding_jobs`)).
			WithArgs(jobID, "error msg").
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := repo.SetJobError(ctx, jobID, "error msg")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetJobsByVideoID(t *testing.T) {
	ctx := context.Background()
	videoID := "video-123"

	t.Run("success with multiple jobs", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "source_file_path", "source_resolution",
			"target_resolutions", "status", "progress", "error_message",
			"started_at", "completed_at", "created_at", "updated_at",
		}).
			AddRow("job1", videoID, "/path/1", "1080p", pq.StringArray{"720p", "480p"}, "completed", 100, "", time.Now(), time.Now(), time.Now(), time.Now()).
			AddRow("job2", videoID, "/path/2", "720p", pq.StringArray{"480p"}, "pending", 0, "", nil, nil, time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, source_file_path, source_resolution, target_resolutions, status, progress, error_message, started_at, completed_at, created_at, updated_at FROM encoding_jobs WHERE video_id = $1 ORDER BY created_at DESC`)).
			WithArgs(videoID).
			WillReturnRows(rows)

		jobs, err := repo.GetJobsByVideoID(ctx, videoID)
		require.NoError(t, err)
		require.Len(t, jobs, 2)
		assert.Equal(t, "job1", jobs[0].ID)
		assert.Equal(t, "job2", jobs[1].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with no jobs", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "source_file_path", "source_resolution",
			"target_resolutions", "status", "progress", "error_message",
			"started_at", "completed_at", "created_at", "updated_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, source_file_path, source_resolution, target_resolutions, status, progress, error_message, started_at, completed_at, created_at, updated_at FROM encoding_jobs WHERE video_id = $1 ORDER BY created_at DESC`)).
			WithArgs(videoID).
			WillReturnRows(rows)

		jobs, err := repo.GetJobsByVideoID(ctx, videoID)
		require.NoError(t, err)
		require.Len(t, jobs, 0)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, source_file_path, source_resolution, target_resolutions, status, progress, error_message, started_at, completed_at, created_at, updated_at FROM encoding_jobs WHERE video_id = $1 ORDER BY created_at DESC`)).
			WithArgs(videoID).
			WillReturnError(errors.New("query failed"))

		jobs, err := repo.GetJobsByVideoID(ctx, videoID)
		require.Error(t, err)
		require.Nil(t, jobs)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_GetActiveJobsByVideoID(t *testing.T) {
	ctx := context.Background()
	videoID := "video-456"

	t.Run("success with active jobs", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "source_file_path", "source_resolution",
			"target_resolutions", "status", "progress", "error_message",
			"started_at", "completed_at", "created_at", "updated_at",
		}).
			AddRow("job1", videoID, "/path/1", "1080p", pq.StringArray{"720p"}, "processing", 50, "", time.Now(), nil, time.Now(), time.Now()).
			AddRow("job2", videoID, "/path/2", "720p", pq.StringArray{"480p"}, "pending", 0, "", nil, nil, time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, source_file_path, source_resolution, target_resolutions, status, progress, error_message, started_at, completed_at, created_at, updated_at FROM encoding_jobs WHERE video_id = $1 AND status IN ('pending', 'processing') ORDER BY created_at DESC`)).
			WithArgs(videoID).
			WillReturnRows(rows)

		jobs, err := repo.GetActiveJobsByVideoID(ctx, videoID)
		require.NoError(t, err)
		require.Len(t, jobs, 2)
		assert.Equal(t, domain.EncodingStatusProcessing, jobs[0].Status)
		assert.Equal(t, domain.EncodingStatusPending, jobs[1].Status)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with no active jobs", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "source_file_path", "source_resolution",
			"target_resolutions", "status", "progress", "error_message",
			"started_at", "completed_at", "created_at", "updated_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, source_file_path, source_resolution, target_resolutions, status, progress, error_message, started_at, completed_at, created_at, updated_at FROM encoding_jobs WHERE video_id = $1 AND status IN ('pending', 'processing') ORDER BY created_at DESC`)).
			WithArgs(videoID).
			WillReturnRows(rows)

		jobs, err := repo.GetActiveJobsByVideoID(ctx, videoID)
		require.NoError(t, err)
		require.Len(t, jobs, 0)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, source_file_path, source_resolution, target_resolutions, status, progress, error_message, started_at, completed_at, created_at, updated_at FROM encoding_jobs WHERE video_id = $1 AND status IN ('pending', 'processing') ORDER BY created_at DESC`)).
			WithArgs(videoID).
			WillReturnError(sql.ErrConnDone)

		jobs, err := repo.GetActiveJobsByVideoID(ctx, videoID)
		require.Error(t, err)
		require.Nil(t, jobs)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEncodingRepository_Unit_ResetStaleJobs(t *testing.T) {
	ctx := context.Background()

	t.Run("success with stale jobs reset", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		duration := 30 * time.Minute

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE encoding_jobs SET status = 'pending', progress = 0, started_at = NULL, error_message = '' WHERE status = 'processing' AND updated_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 5))

		count, err := repo.ResetStaleJobs(ctx, duration)
		require.NoError(t, err)
		assert.Equal(t, int64(5), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with no stale jobs", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		duration := 1 * time.Hour

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE encoding_jobs SET status = 'pending', progress = 0, started_at = NULL, error_message = '' WHERE status = 'processing' AND updated_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		count, err := repo.ResetStaleJobs(ctx, duration)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newEncodingRepo(t)
		defer cleanup()

		duration := 30 * time.Minute

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE encoding_jobs SET status = 'pending', progress = 0, started_at = NULL, error_message = '' WHERE status = 'processing' AND updated_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnError(sql.ErrConnDone)

		count, err := repo.ResetStaleJobs(ctx, duration)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
