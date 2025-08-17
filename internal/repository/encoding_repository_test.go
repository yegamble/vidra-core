package repository

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"
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

// Helper function to create test encoding job
func createTestEncodingJob(t *testing.T, repo EncodingRepository, ctx context.Context, videoID string) *domain.EncodingJob {
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