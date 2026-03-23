package encoding

import (
	"context"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetStaleJobs_OnlyResetsOrphanedJobs(t *testing.T) {
	repo := NewMockEncodingRepository()

	// Stale processing job: updated_at 2 hours ago (orphaned from crash)
	staleTime := time.Now().Add(-2 * time.Hour)
	staleJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/path/to/source.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"720p", "480p"},
		Status:            domain.EncodingStatusProcessing,
		Progress:          45,
		ErrorMessage:      "",
		CreatedAt:         staleTime,
		UpdatedAt:         staleTime,
	}
	startedAt := staleTime
	staleJob.StartedAt = &startedAt
	repo.jobs[staleJob.ID] = staleJob

	// Fresh processing job: updated_at is recent (actively encoding)
	freshJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/path/to/source2.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"720p", "480p"},
		Status:            domain.EncodingStatusProcessing,
		Progress:          60,
		CreatedAt:         time.Now().Add(-1 * time.Hour),
		UpdatedAt:         time.Now(), // Recently updated by heartbeat
	}
	freshStarted := time.Now().Add(-1 * time.Hour)
	freshJob.StartedAt = &freshStarted
	repo.jobs[freshJob.ID] = freshJob

	// Reset with 30 minute threshold
	count, err := repo.ResetStaleJobs(context.Background(), 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Stale job should be reset to pending
	assert.Equal(t, domain.EncodingStatusPending, repo.jobs[staleJob.ID].Status)
	assert.Equal(t, 0, repo.jobs[staleJob.ID].Progress)
	assert.Nil(t, repo.jobs[staleJob.ID].StartedAt)
	assert.Empty(t, repo.jobs[staleJob.ID].ErrorMessage)

	// Fresh job should be untouched
	assert.Equal(t, domain.EncodingStatusProcessing, repo.jobs[freshJob.ID].Status)
	assert.Equal(t, 60, repo.jobs[freshJob.ID].Progress)
	assert.NotNil(t, repo.jobs[freshJob.ID].StartedAt)
}

func TestResetStaleJobs_LongRunningActiveJobNotReset(t *testing.T) {
	repo := NewMockEncodingRepository()

	// Simulate a long-running 4K encode: started 3 hours ago, but heartbeat
	// updated updated_at 10 minutes ago (well within 30-minute threshold)
	longRunningJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/path/to/4k_video.mp4",
		SourceResolution:  "2160p",
		TargetResolutions: []string{"2160p", "1080p", "720p", "480p", "360p", "240p"},
		Status:            domain.EncodingStatusProcessing,
		Progress:          30,
		CreatedAt:         time.Now().Add(-3 * time.Hour),
		UpdatedAt:         time.Now().Add(-10 * time.Minute), // Heartbeat 10 min ago
	}
	started := time.Now().Add(-3 * time.Hour)
	longRunningJob.StartedAt = &started
	repo.jobs[longRunningJob.ID] = longRunningJob

	// Reset with 30 minute threshold
	count, err := repo.ResetStaleJobs(context.Background(), 30*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count) // Should NOT reset

	// Job should be completely untouched
	assert.Equal(t, domain.EncodingStatusProcessing, repo.jobs[longRunningJob.ID].Status)
	assert.Equal(t, 30, repo.jobs[longRunningJob.ID].Progress)
	assert.NotNil(t, repo.jobs[longRunningJob.ID].StartedAt)
}

func TestResetStaleJobs_CalledOnServiceRun(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()

	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, t.TempDir(), cfg, nil, nil, nil)

	// Create a stale processing job (2 hours old)
	staleTime := time.Now().Add(-2 * time.Hour)
	staleJob := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/path/to/source.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"720p"},
		Status:            domain.EncodingStatusProcessing,
		Progress:          45,
		CreatedAt:         staleTime,
		UpdatedAt:         staleTime,
	}
	started := staleTime
	staleJob.StartedAt = &started
	repo.jobs[staleJob.ID] = staleJob

	// Run with a very short context so workers exit immediately
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = svc.Run(ctx, 1)

	// The stale job should have been reset by Run's startup recovery.
	// Note: workers may have also picked it up and failed (source file doesn't exist),
	// which would set it to "failed". Either "pending" or "failed" confirms recovery ran.
	job := repo.jobs[staleJob.ID]
	if job != nil {
		assert.NotEqual(t, domain.EncodingStatusProcessing, job.Status,
			"Stale job should no longer be stuck in processing after Run")
	}
	// If job was deleted, that also confirms it was processed (completed jobs get deleted)
}
