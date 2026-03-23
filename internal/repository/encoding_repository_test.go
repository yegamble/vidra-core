package repository

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingRepository_CreateJob(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test user and video
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video.ID,
		SourceFilePath:    "/path/to/source.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"1080p", "720p", "480p", "360p", "240p"},
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	err := encodingRepo.CreateJob(ctx, job)
	require.NoError(t, err)

	// Verify job was created
	retrievedJob, err := encodingRepo.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, job.ID, retrievedJob.ID)
	assert.Equal(t, job.VideoID, retrievedJob.VideoID)
	assert.Equal(t, job.SourceFilePath, retrievedJob.SourceFilePath)
	assert.Equal(t, job.SourceResolution, retrievedJob.SourceResolution)
	assert.Equal(t, job.TargetResolutions, retrievedJob.TargetResolutions)
	assert.Equal(t, job.Status, retrievedJob.Status)
	assert.Equal(t, job.Progress, retrievedJob.Progress)
}

func TestEncodingRepository_GetJobByVideoID(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Get job by video ID
	retrievedJob, err := encodingRepo.GetJobByVideoID(ctx, video.ID)
	require.NoError(t, err)
	assert.Equal(t, job.ID, retrievedJob.ID)
	assert.Equal(t, job.VideoID, retrievedJob.VideoID)
}

func TestEncodingRepository_UpdateJob(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Update job
	now := time.Now()
	job.Status = domain.EncodingStatusProcessing
	job.Progress = 50
	job.StartedAt = &now
	job.UpdatedAt = now

	err := encodingRepo.UpdateJob(ctx, job)
	require.NoError(t, err)

	// Verify update
	retrievedJob, err := encodingRepo.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusProcessing, retrievedJob.Status)
	assert.Equal(t, 50, retrievedJob.Progress)
	assert.NotNil(t, retrievedJob.StartedAt)
}

func TestEncodingRepository_UpdateJobStatus(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Update status
	err := encodingRepo.UpdateJobStatus(ctx, job.ID, domain.EncodingStatusCompleted)
	require.NoError(t, err)

	// Verify update
	retrievedJob, err := encodingRepo.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusCompleted, retrievedJob.Status)
}

func TestEncodingRepository_UpdateJobProgress(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Update progress
	err := encodingRepo.UpdateJobProgress(ctx, job.ID, 75)
	require.NoError(t, err)

	// Verify update
	retrievedJob, err := encodingRepo.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, 75, retrievedJob.Progress)
}

func TestEncodingRepository_SetJobError(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Set error
	errorMsg := "Encoding failed: invalid input format"
	err := encodingRepo.SetJobError(ctx, job.ID, errorMsg)
	require.NoError(t, err)

	// Verify update
	retrievedJob, err := encodingRepo.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusFailed, retrievedJob.Status)
	assert.Equal(t, errorMsg, retrievedJob.ErrorMessage)
}

func TestEncodingRepository_GetPendingJobs(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video1 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 1")
	video2 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 2")
	video3 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 3")

	// Create jobs with different statuses
	job1 := createTestEncodingJob(t, encodingRepo, ctx, video1.ID)
	job2 := createTestEncodingJob(t, encodingRepo, ctx, video2.ID)
	job3 := createTestEncodingJob(t, encodingRepo, ctx, video3.ID)

	// Update one job to processing
	err := encodingRepo.UpdateJobStatus(ctx, job2.ID, domain.EncodingStatusProcessing)
	require.NoError(t, err)

	// Get pending jobs
	pendingJobs, err := encodingRepo.GetPendingJobs(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, pendingJobs, 2) // job1 and job3 should be pending

	// Verify they are the correct jobs
	pendingIDs := []string{pendingJobs[0].ID, pendingJobs[1].ID}
	assert.Contains(t, pendingIDs, job1.ID)
	assert.Contains(t, pendingIDs, job3.ID)
	assert.NotContains(t, pendingIDs, job2.ID)
}

func TestEncodingRepository_GetNextJob(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video1 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 1")
	video2 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 2")

	// Create jobs (video1 created first, should be returned first)
	job1 := createTestEncodingJob(t, encodingRepo, ctx, video1.ID)
	time.Sleep(10 * time.Millisecond) // Ensure different creation times
	createTestEncodingJob(t, encodingRepo, ctx, video2.ID)

	// Get next job
	nextJob, err := encodingRepo.GetNextJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, nextJob)
	assert.Equal(t, job1.ID, nextJob.ID)
	assert.Equal(t, domain.EncodingStatusProcessing, nextJob.Status)
	assert.NotNil(t, nextJob.StartedAt)

	// Get next job again - should return the second job
	nextJob2, err := encodingRepo.GetNextJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, nextJob2)
	assert.NotEqual(t, job1.ID, nextJob2.ID)
	assert.Equal(t, domain.EncodingStatusProcessing, nextJob2.Status)

	// Get next job again - should return nil (no more pending jobs)
	nextJob3, err := encodingRepo.GetNextJob(ctx)
	require.NoError(t, err)
	assert.Nil(t, nextJob3)
}

func TestEncodingRepository_UniqueActiveJobPerVideo(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	user := createTestUser(t, userRepo, ctx, "user-unique", "unique@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Unique Video")

	// First pending job should succeed
	job1 := createTestEncodingJob(t, encodingRepo, ctx, video.ID)
	require.NotNil(t, job1)

	// Second pending job for the same video should fail due to unique index
	job2 := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video.ID,
		SourceFilePath:    "/path/to/source2.mp4",
		SourceResolution:  "720p",
		TargetResolutions: []string{"720p", "480p"},
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	err := encodingRepo.CreateJob(ctx, job2)
	require.Error(t, err, "expected error creating second active job for same video")
}

func TestEncodingRepository_DeleteJob(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Delete job
	err := encodingRepo.DeleteJob(ctx, job.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = encodingRepo.GetJob(ctx, job.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JOB_NOT_FOUND")
}

func TestEncodingRepository_ConcurrentGetNextJob(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Test concurrent access to GetNextJob
	done := make(chan *domain.EncodingJob, 2)

	go func() {
		job, err := encodingRepo.GetNextJob(ctx)
		require.NoError(t, err)
		done <- job
	}()

	go func() {
		job, err := encodingRepo.GetNextJob(ctx)
		require.NoError(t, err)
		done <- job
	}()

	// Collect results
	job1 := <-done
	job2 := <-done

	// Only one should get the job, the other should get nil
	if job1 != nil {
		assert.Nil(t, job2)
		assert.Equal(t, domain.EncodingStatusProcessing, job1.Status)
	} else {
		assert.NotNil(t, job2)
		assert.Equal(t, domain.EncodingStatusProcessing, job2.Status)
	}
}

func TestEncodingRepository_ResetStaleJobs(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")

	// Create three videos with jobs in different states
	video1 := createTestVideo(t, videoRepo, ctx, user.ID, "Stale Processing Video")
	video2 := createTestVideo(t, videoRepo, ctx, user.ID, "Fresh Processing Video")
	video3 := createTestVideo(t, videoRepo, ctx, user.ID, "Pending Video")

	job1 := createTestEncodingJob(t, encodingRepo, ctx, video1.ID)
	job2 := createTestEncodingJob(t, encodingRepo, ctx, video2.ID)
	job3 := createTestEncodingJob(t, encodingRepo, ctx, video3.ID)

	// Move job1 and job2 to processing
	err := encodingRepo.UpdateJobStatus(ctx, job1.ID, domain.EncodingStatusProcessing)
	require.NoError(t, err)
	err = encodingRepo.UpdateJobStatus(ctx, job2.ID, domain.EncodingStatusProcessing)
	require.NoError(t, err)
	// job3 stays pending

	// Disable the updated_at trigger so we can manually set old timestamps
	_, err = testDB.DB.ExecContext(ctx,
		`ALTER TABLE encoding_jobs DISABLE TRIGGER update_encoding_jobs_updated_at`)
	require.NoError(t, err)
	defer func() {
		_, _ = testDB.DB.ExecContext(ctx,
			`ALTER TABLE encoding_jobs ENABLE TRIGGER update_encoding_jobs_updated_at`)
	}()

	// Make job1 stale (2 hours old)
	_, err = testDB.DB.ExecContext(ctx,
		`UPDATE encoding_jobs SET updated_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, job1.ID)
	require.NoError(t, err)

	// Re-enable trigger
	_, err = testDB.DB.ExecContext(ctx,
		`ALTER TABLE encoding_jobs ENABLE TRIGGER update_encoding_jobs_updated_at`)
	require.NoError(t, err)

	// job2 was just updated so its updated_at is recent (not stale)

	// Reset stale jobs older than 1 hour
	resetCount, err := encodingRepo.ResetStaleJobs(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), resetCount) // Only job1 should be reset

	// Verify job1 was reset to pending
	resetJob, err := encodingRepo.GetJob(ctx, job1.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusPending, resetJob.Status)
	assert.Equal(t, 0, resetJob.Progress)
	assert.Nil(t, resetJob.StartedAt)
	assert.Empty(t, resetJob.ErrorMessage)

	// Verify job2 is still processing (active, NOT reset)
	freshJob, err := encodingRepo.GetJob(ctx, job2.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusProcessing, freshJob.Status)

	// Verify job3 is still pending (untouched)
	pendingJob, err := encodingRepo.GetJob(ctx, job3.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusPending, pendingJob.Status)
}

func TestEncodingRepository_ResetStaleJobs_NoStaleJobs(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)

	ctx := context.Background()

	// No jobs at all
	resetCount, err := encodingRepo.ResetStaleJobs(ctx, 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(0), resetCount)
}

func TestEncodingRepository_ResetStaleJobs_ThenPickedUp(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Recovery Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Move to processing and make it stale
	err := encodingRepo.UpdateJobStatus(ctx, job.ID, domain.EncodingStatusProcessing)
	require.NoError(t, err)

	// Disable trigger, set old timestamp, re-enable
	_, err = testDB.DB.ExecContext(ctx,
		`ALTER TABLE encoding_jobs DISABLE TRIGGER update_encoding_jobs_updated_at`)
	require.NoError(t, err)
	_, err = testDB.DB.ExecContext(ctx,
		`UPDATE encoding_jobs SET updated_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, job.ID)
	require.NoError(t, err)
	_, err = testDB.DB.ExecContext(ctx,
		`ALTER TABLE encoding_jobs ENABLE TRIGGER update_encoding_jobs_updated_at`)
	require.NoError(t, err)

	// Reset stale jobs
	resetCount, err := encodingRepo.ResetStaleJobs(ctx, 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), resetCount)

	// Verify GetNextJob can now pick it up again
	nextJob, err := encodingRepo.GetNextJob(ctx)
	require.NoError(t, err)
	require.NotNil(t, nextJob)
	assert.Equal(t, job.ID, nextJob.ID)
	assert.Equal(t, domain.EncodingStatusProcessing, nextJob.Status) // GetNextJob sets it to processing
}

func TestEncodingRepository_ResetStaleJobs_LongRunningJobNotReset(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Long Running Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// Move to processing
	err := encodingRepo.UpdateJobStatus(ctx, job.ID, domain.EncodingStatusProcessing)
	require.NoError(t, err)

	// Simulate a heartbeat that touched updated_at 20 minutes ago
	// (within the 30-minute threshold, so this is NOT stale)
	_, err = testDB.DB.ExecContext(ctx,
		`ALTER TABLE encoding_jobs DISABLE TRIGGER update_encoding_jobs_updated_at`)
	require.NoError(t, err)
	_, err = testDB.DB.ExecContext(ctx,
		`UPDATE encoding_jobs SET updated_at = NOW() - INTERVAL '20 minutes' WHERE id = $1`, job.ID)
	require.NoError(t, err)
	_, err = testDB.DB.ExecContext(ctx,
		`ALTER TABLE encoding_jobs ENABLE TRIGGER update_encoding_jobs_updated_at`)
	require.NoError(t, err)

	// Attempt to reset with 30-minute threshold
	resetCount, err := encodingRepo.ResetStaleJobs(ctx, 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(0), resetCount) // Should NOT reset — heartbeat kept it fresh

	// Verify job is still processing
	activeJob, err := encodingRepo.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.EncodingStatusProcessing, activeJob.Status)
}

// Helper function to create test encoding job
func createTestEncodingJob(t *testing.T, repo usecase.EncodingRepository, ctx context.Context, videoID string) *domain.EncodingJob {
	t.Helper()

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           videoID,
		SourceFilePath:    "/path/to/source.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"1080p", "720p", "480p", "360p", "240p"},
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	err := repo.CreateJob(ctx, job)
	require.NoError(t, err)

	return job
}

func TestEncodingRepository_GetJobsByVideoID(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test user and videos
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video1 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 1")
	video2 := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video 2")

	// Create multiple jobs for video1
	job1 := createTestEncodingJob(t, encodingRepo, ctx, video1.ID)
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps

	job2 := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video1.ID,
		SourceFilePath:    "/path/to/source2.mp4",
		SourceResolution:  "720p",
		TargetResolutions: []string{"720p", "480p"},
		Status:            domain.EncodingStatusCompleted,
		Progress:          100,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		CompletedAt:       &time.Time{},
	}
	err := encodingRepo.CreateJob(ctx, job2)
	require.NoError(t, err)

	job3 := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video1.ID,
		SourceFilePath:    "/path/to/source3.mp4",
		SourceResolution:  "480p",
		TargetResolutions: []string{"480p"},
		Status:            domain.EncodingStatusFailed,
		Progress:          50,
		ErrorMessage:      "FFmpeg error",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	err = encodingRepo.CreateJob(ctx, job3)
	require.NoError(t, err)

	// Create job for video2
	job4 := createTestEncodingJob(t, encodingRepo, ctx, video2.ID)

	// Test GetJobsByVideoID for video1
	t.Run("get all jobs for video1", func(t *testing.T) {
		jobs, err := encodingRepo.GetJobsByVideoID(ctx, video1.ID)
		require.NoError(t, err)
		require.Len(t, jobs, 3)

		// Verify order (newest first)
		assert.Equal(t, job3.ID, jobs[0].ID)
		assert.Equal(t, job2.ID, jobs[1].ID)
		assert.Equal(t, job1.ID, jobs[2].ID)

		// Verify statuses
		assert.Equal(t, domain.EncodingStatusFailed, jobs[0].Status)
		assert.Equal(t, domain.EncodingStatusCompleted, jobs[1].Status)
		assert.Equal(t, domain.EncodingStatusPending, jobs[2].Status)
	})

	// Test GetJobsByVideoID for video2
	t.Run("get all jobs for video2", func(t *testing.T) {
		jobs, err := encodingRepo.GetJobsByVideoID(ctx, video2.ID)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		assert.Equal(t, job4.ID, jobs[0].ID)
	})

	// Test GetJobsByVideoID for non-existent video
	t.Run("get jobs for non-existent video", func(t *testing.T) {
		jobs, err := encodingRepo.GetJobsByVideoID(ctx, uuid.NewString())
		require.NoError(t, err)
		assert.Empty(t, jobs)
	})
}

func TestEncodingRepository_GetActiveJobsByVideoID(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test user and video
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")

	// Create jobs with different statuses
	pendingJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video.ID,
		SourceFilePath:    "/path/to/pending.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"1080p", "720p"},
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	err := encodingRepo.CreateJob(ctx, pendingJob)
	require.NoError(t, err)

	processingJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video.ID,
		SourceFilePath:    "/path/to/processing.mp4",
		SourceResolution:  "720p",
		TargetResolutions: []string{"720p", "480p"},
		Status:            domain.EncodingStatusProcessing,
		Progress:          45,
		StartedAt:         &time.Time{},
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	err = encodingRepo.CreateJob(ctx, processingJob)
	require.NoError(t, err)

	completedJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video.ID,
		SourceFilePath:    "/path/to/completed.mp4",
		SourceResolution:  "480p",
		TargetResolutions: []string{"480p"},
		Status:            domain.EncodingStatusCompleted,
		Progress:          100,
		CompletedAt:       &time.Time{},
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	err = encodingRepo.CreateJob(ctx, completedJob)
	require.NoError(t, err)

	failedJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           video.ID,
		SourceFilePath:    "/path/to/failed.mp4",
		SourceResolution:  "360p",
		TargetResolutions: []string{"360p"},
		Status:            domain.EncodingStatusFailed,
		Progress:          30,
		ErrorMessage:      "FFmpeg crash",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	err = encodingRepo.CreateJob(ctx, failedJob)
	require.NoError(t, err)

	// Test GetActiveJobsByVideoID
	t.Run("get only active jobs", func(t *testing.T) {
		jobs, err := encodingRepo.GetActiveJobsByVideoID(ctx, video.ID)
		require.NoError(t, err)
		require.Len(t, jobs, 2) // Only pending and processing

		// Check we got the right jobs
		jobIDs := make(map[string]bool)
		for _, job := range jobs {
			jobIDs[job.ID] = true
			assert.True(t, job.Status == domain.EncodingStatusPending || job.Status == domain.EncodingStatusProcessing)
		}

		assert.True(t, jobIDs[pendingJob.ID])
		assert.True(t, jobIDs[processingJob.ID])
		assert.False(t, jobIDs[completedJob.ID])
		assert.False(t, jobIDs[failedJob.ID])
	})

	// Test GetActiveJobsByVideoID for non-existent video
	t.Run("get active jobs for non-existent video", func(t *testing.T) {
		jobs, err := encodingRepo.GetActiveJobsByVideoID(ctx, uuid.NewString())
		require.NoError(t, err)
		assert.Empty(t, jobs)
	})
}
