package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCaptionGenerationMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func newCaptionGenerationRepo(t *testing.T) (*captionGenerationRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupCaptionGenerationMockDB(t)
	repo := NewCaptionGenerationRepository(db).(*captionGenerationRepository)
	return repo, mock, func() { _ = db.Close() }
}

func captionGenerationColumns() []string {
	return []string{
		"id", "video_id", "user_id", "source_audio_path", "target_language", "detected_language",
		"status", "progress", "error_message", "model_size", "provider", "generated_caption_id",
		"output_format", "transcription_time_seconds", "is_automatic", "retry_count", "max_retries",
		"started_at", "completed_at", "created_at", "updated_at",
	}
}

func sampleCaptionGenerationJob() *domain.CaptionGenerationJob {
	targetLang := "en"
	now := time.Now().Truncate(time.Second)
	return &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         uuid.New(),
		UserID:          uuid.New(),
		SourceAudioPath: "/tmp/audio.wav",
		TargetLanguage:  &targetLang,
		Status:          domain.CaptionGenStatusPending,
		Progress:        0,
		ModelSize:       domain.WhisperModelBase,
		Provider:        domain.WhisperProviderLocal,
		OutputFormat:    domain.CaptionFormatVTT,
		IsAutomatic:     true,
		RetryCount:      0,
		MaxRetries:      3,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func captionGenerationRow(job *domain.CaptionGenerationJob) []driver.Value {
	return []driver.Value{
		job.ID, job.VideoID, job.UserID, job.SourceAudioPath, job.TargetLanguage, job.DetectedLanguage,
		job.Status, job.Progress, job.ErrorMessage, job.ModelSize, job.Provider, job.GeneratedCaptionID,
		job.OutputFormat, job.TranscriptionTimeSecs, job.IsAutomatic, job.RetryCount, job.MaxRetries,
		job.StartedAt, job.CompletedAt, job.CreatedAt, job.UpdatedAt,
	}
}

func TestCaptionGenerationRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newCaptionGenerationRepo(t)
	defer cleanup()

	job := sampleCaptionGenerationJob()
	job.ID = uuid.Nil

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO caption_generation_jobs (
			id, video_id, user_id, source_audio_path, target_language, status,
			progress, model_size, provider, output_format, is_automatic,
			retry_count, max_retries, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		RETURNING id`)).
		WithArgs(
			sqlmock.AnyArg(), job.VideoID, job.UserID, job.SourceAudioPath, job.TargetLanguage,
			job.Status, job.Progress, job.ModelSize, job.Provider, job.OutputFormat,
			job.IsAutomatic, job.RetryCount, job.MaxRetries, sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	require.NoError(t, repo.Create(ctx, job))
	require.NotEqual(t, uuid.Nil, job.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptionGenerationRepository_Unit_Create_Error(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newCaptionGenerationRepo(t)
	defer cleanup()

	job := sampleCaptionGenerationJob()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO caption_generation_jobs`)).
		WillReturnError(errors.New("insert failed"))

	err := repo.Create(ctx, job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create caption generation job")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptionGenerationRepository_Unit_GetByID(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newCaptionGenerationRepo(t)
	defer cleanup()

	job := sampleCaptionGenerationJob()

	rows := sqlmock.NewRows(captionGenerationColumns()).AddRow(captionGenerationRow(job)...)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE id = $1`)).
		WithArgs(job.ID).
		WillReturnRows(rows)

	got, err := repo.GetByID(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, job.ID, got.ID)

	missingID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE id = $1`)).
		WithArgs(missingID).
		WillReturnError(sql.ErrNoRows)

	_, err = repo.GetByID(ctx, missingID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptionGenerationRepository_Unit_UpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newCaptionGenerationRepo(t)
	defer cleanup()

	job := sampleCaptionGenerationJob()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET target_language = $2,
			detected_language = $3,
			status = $4,
			progress = $5,
			error_message = $6,
			generated_caption_id = $7,
			transcription_time_seconds = $8,
			retry_count = $9,
			started_at = $10,
			completed_at = $11,
			updated_at = $12
		WHERE id = $1`)).
		WithArgs(
			job.ID, job.TargetLanguage, job.DetectedLanguage, job.Status,
			job.Progress, job.ErrorMessage, job.GeneratedCaptionID, job.TranscriptionTimeSecs,
			job.RetryCount, job.StartedAt, job.CompletedAt, sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Update(ctx, job))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET target_language = $2,
			detected_language = $3,
			status = $4,
			progress = $5,
			error_message = $6,
			generated_caption_id = $7,
			transcription_time_seconds = $8,
			retry_count = $9,
			started_at = $10,
			completed_at = $11,
			updated_at = $12
		WHERE id = $1`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.Update(ctx, job), domain.ErrNotFound)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM caption_generation_jobs WHERE id = $1`)).
		WithArgs(job.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Delete(ctx, job.ID))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM caption_generation_jobs WHERE id = $1`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.Delete(ctx, uuid.New()), domain.ErrNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptionGenerationRepository_Unit_QueueAndListingMethods(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newCaptionGenerationRepo(t)
	defer cleanup()

	job := sampleCaptionGenerationJob()

	nextRows := sqlmock.NewRows(captionGenerationColumns()).AddRow(captionGenerationRow(job)...)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`)).
		WithArgs(domain.CaptionGenStatusPending).
		WillReturnRows(nextRows)

	nextJob, err := repo.GetNextPendingJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, nextJob)
	assert.Equal(t, job.ID, nextJob.ID)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`)).
		WithArgs(domain.CaptionGenStatusPending).
		WillReturnError(sql.ErrNoRows)

	none, err := repo.GetNextPendingJob(ctx)
	require.NoError(t, err)
	assert.Nil(t, none)

	pendingRows := sqlmock.NewRows(captionGenerationColumns()).AddRow(captionGenerationRow(job)...)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2`)).
		WithArgs(domain.CaptionGenStatusPending, 10).
		WillReturnRows(pendingRows)

	jobs, err := repo.GetPendingJobs(ctx, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE video_id = $1
		ORDER BY created_at DESC`)).
		WithArgs(job.VideoID).
		WillReturnRows(sqlmock.NewRows(captionGenerationColumns()).AddRow(captionGenerationRow(job)...))
	byVideo, err := repo.GetByVideoID(ctx, job.VideoID)
	require.NoError(t, err)
	require.Len(t, byVideo, 1)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, user_id, source_audio_path, target_language, detected_language,
			   status, progress, error_message, model_size, provider, generated_caption_id,
			   output_format, transcription_time_seconds, is_automatic, retry_count, max_retries,
			   started_at, completed_at, created_at, updated_at
		FROM caption_generation_jobs
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`)).
		WithArgs(job.UserID, 5, 0).
		WillReturnRows(sqlmock.NewRows(captionGenerationColumns()).AddRow(captionGenerationRow(job)...))
	byUser, err := repo.GetByUserID(ctx, job.UserID, 5, 0)
	require.NoError(t, err)
	require.Len(t, byUser, 1)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptionGenerationRepository_Unit_StatusAndCounters(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newCaptionGenerationRepo(t)
	defer cleanup()

	jobID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM caption_generation_jobs WHERE status = $1`)).
		WithArgs(domain.CaptionGenStatusPending).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	count, err := repo.CountByStatus(ctx, domain.CaptionGenStatusPending)
	require.NoError(t, err)
	assert.Equal(t, 7, count)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET status = $2,
			started_at = CASE WHEN $2 = 'processing' AND started_at IS NULL THEN NOW() ELSE started_at END,
			updated_at = NOW()
		WHERE id = $1`)).
		WithArgs(jobID, domain.CaptionGenStatusProcessing).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateStatus(ctx, jobID, domain.CaptionGenStatusProcessing))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET status = $2,
			started_at = CASE WHEN $2 = 'processing' AND started_at IS NULL THEN NOW() ELSE started_at END,
			updated_at = NOW()
		WHERE id = $1`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.UpdateStatus(ctx, uuid.New(), domain.CaptionGenStatusProcessing), domain.ErrNotFound)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET progress = $2,
			updated_at = NOW()
		WHERE id = $1`)).
		WithArgs(jobID, 42).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateProgress(ctx, jobID, 42))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET status = $2,
			error_message = $3,
			completed_at = NOW(),
			updated_at = NOW()
		WHERE id = $1`)).
		WithArgs(jobID, domain.CaptionGenStatusFailed, "boom").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkFailed(ctx, jobID, "boom"))

	captionID := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE caption_generation_jobs
		SET status = $2,
			generated_caption_id = $3,
			detected_language = $4,
			transcription_time_seconds = $5,
			progress = 100,
			completed_at = NOW(),
			updated_at = NOW()
		WHERE id = $1`)).
		WithArgs(jobID, domain.CaptionGenStatusCompleted, captionID, "en", 123).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkCompleted(ctx, jobID, captionID, "en", 123))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM caption_generation_jobs
		WHERE status IN ($1, $2)
		  AND completed_at < NOW() - INTERVAL '1 day' * $3`)).
		WithArgs(domain.CaptionGenStatusCompleted, domain.CaptionGenStatusFailed, 30).
		WillReturnResult(sqlmock.NewResult(0, 9))
	deleted, err := repo.DeleteOldCompletedJobs(ctx, 30)
	require.NoError(t, err)
	assert.EqualValues(t, 9, deleted)

	require.NoError(t, mock.ExpectationsWereMet())
}
