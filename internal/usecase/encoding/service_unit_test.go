package encoding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- NewService tests ---

func TestNewService(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, "/tmp/uploads", cfg, nil, nil, nil)

	assert.NotNil(t, svc, "NewService should return a non-nil Service")

	// Verify it implements the Service interface
	var _ Service = svc
}

func TestNewService_WithNilDeps(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	// All optional dependencies nil
	svc := NewService(repo, videoRepo, nil, "/tmp", cfg, nil, nil, nil)
	assert.NotNil(t, svc)
}

func TestWithCaptionGenerator(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, "/tmp", cfg, nil, nil, nil)
	concrete := svc.(*service)

	assert.Nil(t, concrete.captionGen, "captionGen should be nil by default")

	mockGen := &mockCaptionGenerator{}
	concrete.WithCaptionGenerator(mockGen)

	assert.NotNil(t, concrete.captionGen, "captionGen should be set after WithCaptionGenerator")
}

func TestWithFederationEnqueuer(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, "/tmp", cfg, nil, nil, nil)
	concrete := svc.(*service)

	mockEnq := &mockJobEnqueuer{}
	concrete.WithFederationEnqueuer(mockEnq)

	assert.NotNil(t, concrete.fedEnq, "fedEnq should be set after WithFederationEnqueuer")
}

// --- validateJob tests ---

func TestValidateJob_MissingSourceFilePath(t *testing.T) {
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	job := &domain.EncodingJob{
		ID:             uuid.NewString(),
		SourceFilePath: "",
	}

	err := svc.validateJob(job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing source file path")
}

func TestValidateJob_SourceFileNotFound(t *testing.T) {
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	job := &domain.EncodingJob{
		ID:             uuid.NewString(),
		SourceFilePath: "/nonexistent/path/video.mp4",
	}

	err := svc.validateJob(job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "source file not found")
}

func TestValidateJob_ValidSourceFile(t *testing.T) {
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "test_video.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("fake video"), 0600))

	job := &domain.EncodingJob{
		ID:             uuid.NewString(),
		SourceFilePath: sourceFile,
	}

	err := svc.validateJob(job)

	assert.NoError(t, err)
}

// --- ProcessNext tests ---

func TestProcessNext_NoJobsAvailable(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, t.TempDir(), cfg, nil, nil, nil)
	ctx := context.Background()

	// No pending jobs
	processed, err := svc.ProcessNext(ctx)

	assert.NoError(t, err)
	assert.False(t, processed, "Should return false when no jobs are available")
}

func TestProcessNext_JobFailsValidation(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	tempDir := t.TempDir()
	svc := NewService(repo, videoRepo, nil, tempDir, cfg, nil, nil, nil)
	ctx := context.Background()

	// Create a pending job with non-existent source file
	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/nonexistent/video.mp4",
		SourceResolution:  "1080p",
		TargetResolutions: []string{"720p"},
		Status:            domain.EncodingStatusPending,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	require.NoError(t, repo.CreateJob(ctx, job))

	processed, err := svc.ProcessNext(ctx)

	assert.Error(t, err, "Should return error for invalid source file")
	assert.True(t, processed, "Should return true since a job was picked up")

	// Job should be marked as failed
	failedJob, getErr := repo.GetJob(ctx, job.ID)
	require.NoError(t, getErr)
	assert.Equal(t, domain.EncodingStatusFailed, failedJob.Status)
	assert.Contains(t, failedJob.ErrorMessage, "source file not found")
}

func TestProcessNext_EmptySourcePath(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	tempDir := t.TempDir()
	svc := NewService(repo, videoRepo, nil, tempDir, cfg, nil, nil, nil)
	ctx := context.Background()

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "", // Empty
		SourceResolution:  "720p",
		TargetResolutions: []string{"480p"},
		Status:            domain.EncodingStatusPending,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	require.NoError(t, repo.CreateJob(ctx, job))

	processed, err := svc.ProcessNext(ctx)

	assert.Error(t, err)
	assert.True(t, processed)

	failedJob, _ := repo.GetJob(ctx, job.ID)
	assert.Equal(t, domain.EncodingStatusFailed, failedJob.Status)
	assert.Contains(t, failedJob.ErrorMessage, "missing source file path")
}

// --- validateBinaryPath tests ---

func TestValidateBinaryPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "simple binary name",
			path:    "ffmpeg",
			wantErr: false,
		},
		{
			name:    "absolute path",
			path:    "/usr/bin/ffmpeg",
			wantErr: false,
		},
		{
			name:    "homebrew path",
			path:    "/opt/homebrew/bin/ffmpeg",
			wantErr: false,
		},
		{
			name:    "path with relative dot-dot that remains after clean",
			path:    "/usr/bin/../../..",
			wantErr: false, // filepath.Clean resolves this to "/", no ".." remains
		},
		{
			name:    "path with semicolon injection",
			path:    "/usr/bin/ffmpeg;rm -rf /",
			wantErr: true,
			errMsg:  "suspicious characters",
		},
		{
			name:    "path with pipe injection",
			path:    "/usr/bin/ffmpeg|cat /etc/passwd",
			wantErr: true,
			errMsg:  "suspicious characters",
		},
		{
			name:    "path with ampersand",
			path:    "/usr/bin/ffmpeg&echo hacked",
			wantErr: true,
			errMsg:  "suspicious characters",
		},
		{
			name:    "path with dollar sign",
			path:    "/usr/bin/$HOME/ffmpeg",
			wantErr: true,
			errMsg:  "suspicious characters",
		},
		{
			name:    "path with backtick",
			path:    "/usr/bin/`whoami`/ffmpeg",
			wantErr: true,
			errMsg:  "suspicious characters",
		},
		{
			name:    "empty path is simple name",
			path:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBinaryPath(tt.path)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- generateMasterPlaylist tests ---

func TestGenerateMasterPlaylist_BasicResolutions(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	resolutions := []string{"720p", "1080p"}
	err := svc.generateMasterPlaylist(tempDir, resolutions)

	require.NoError(t, err)

	masterPath := filepath.Join(tempDir, "master.m3u8")
	assert.FileExists(t, masterPath)

	content, err := os.ReadFile(masterPath)
	require.NoError(t, err)

	playlist := string(content)
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:3")
	assert.Contains(t, playlist, "720p/stream.m3u8")
	assert.Contains(t, playlist, "1080p/stream.m3u8")
	assert.Contains(t, playlist, "BANDWIDTH=2800000") // 720p
	assert.Contains(t, playlist, "BANDWIDTH=5000000") // 1080p
	assert.Contains(t, playlist, "RESOLUTION=1280x720")
	assert.Contains(t, playlist, "RESOLUTION=1920x1080")
}

func TestGenerateMasterPlaylist_AllResolutions(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	resolutions := []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"}
	err := svc.generateMasterPlaylist(tempDir, resolutions)

	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(tempDir, "master.m3u8"))
	require.NoError(t, err)

	playlist := string(content)
	assert.Contains(t, playlist, "240p/stream.m3u8")
	assert.Contains(t, playlist, "4320p/stream.m3u8")
}

func TestGenerateMasterPlaylist_EmptyResolutions(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	err := svc.generateMasterPlaylist(tempDir, []string{})

	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(tempDir, "master.m3u8"))
	require.NoError(t, err)

	playlist := string(content)
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:3")
}

func TestGenerateMasterPlaylist_SkipsUnknownResolution(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	resolutions := []string{"720p", "unknown", "1080p"}
	err := svc.generateMasterPlaylist(tempDir, resolutions)

	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(tempDir, "master.m3u8"))
	require.NoError(t, err)

	playlist := string(content)
	assert.Contains(t, playlist, "720p/stream.m3u8")
	assert.Contains(t, playlist, "1080p/stream.m3u8")
	assert.NotContains(t, playlist, "unknown")
}

// --- createProgressUpdater tests ---

func TestCreateProgressUpdater(t *testing.T) {
	repo := NewMockEncodingRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{repo: repo, cfg: cfg}

	ctx := context.Background()
	jobID := uuid.NewString()
	totalTasks := 4

	// Create the job in the repo so progress updates work
	job := &domain.EncodingJob{
		ID:        jobID,
		VideoID:   uuid.NewString(),
		Status:    domain.EncodingStatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateJob(ctx, job))

	update := svc.createProgressUpdater(ctx, jobID, totalTasks)

	// Call update 4 times, checking progress increments
	update()
	updatedJob, _ := repo.GetJob(ctx, jobID)
	assert.Equal(t, 25, updatedJob.Progress)

	update()
	updatedJob, _ = repo.GetJob(ctx, jobID)
	assert.Equal(t, 50, updatedJob.Progress)

	update()
	updatedJob, _ = repo.GetJob(ctx, jobID)
	assert.Equal(t, 75, updatedJob.Progress)

	update()
	updatedJob, _ = repo.GetJob(ctx, jobID)
	assert.Equal(t, 100, updatedJob.Progress)
}

// --- triggerCaptionGeneration tests ---

func TestTriggerCaptionGeneration_NilCaptionGen(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:              "ffmpeg",
		HLSSegmentDuration:      4,
		EnableCaptionGeneration: true,
	}

	svc := &service{
		repo:       repo,
		videoRepo:  videoRepo,
		cfg:        cfg,
		captionGen: nil,
	}

	ctx := context.Background()

	// Should not panic when captionGen is nil
	svc.triggerCaptionGeneration(ctx, uuid.NewString())
}

func TestTriggerCaptionGeneration_Disabled(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:              "ffmpeg",
		HLSSegmentDuration:      4,
		EnableCaptionGeneration: false, // Disabled
	}

	mockGen := &mockCaptionGenerator{}
	svc := &service{
		repo:       repo,
		videoRepo:  videoRepo,
		cfg:        cfg,
		captionGen: mockGen,
	}

	ctx := context.Background()

	// Should not call caption gen when disabled
	svc.triggerCaptionGeneration(ctx, uuid.NewString())

	assert.False(t, mockGen.createJobCalled, "CreateJob should not be called when caption gen is disabled")
}

func TestTriggerCaptionGeneration_VideoNotFound(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:              "ffmpeg",
		HLSSegmentDuration:      4,
		EnableCaptionGeneration: true,
	}

	mockGen := &mockCaptionGenerator{}
	svc := &service{
		repo:       repo,
		videoRepo:  videoRepo,
		cfg:        cfg,
		captionGen: mockGen,
	}

	ctx := context.Background()

	// Video not in repo
	svc.triggerCaptionGeneration(ctx, "nonexistent-video-id")

	assert.False(t, mockGen.createJobCalled, "CreateJob should not be called when video is not found")
}

func TestTriggerCaptionGeneration_Success(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:              "ffmpeg",
		HLSSegmentDuration:      4,
		EnableCaptionGeneration: true,
	}

	videoID := uuid.New()
	userID := uuid.New()
	video := &domain.Video{
		ID:     videoID.String(),
		UserID: userID.String(),
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	mockGen := &mockCaptionGenerator{}
	svc := &service{
		repo:       repo,
		videoRepo:  videoRepo,
		cfg:        cfg,
		captionGen: mockGen,
	}

	ctx := context.Background()

	svc.triggerCaptionGeneration(ctx, videoID.String())

	assert.True(t, mockGen.createJobCalled, "CreateJob should be called for valid video")
}

// --- triggerNotifications tests ---

func TestTriggerNotifications_NilNotificationService(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := &service{
		repo:            repo,
		videoRepo:       videoRepo,
		cfg:             cfg,
		notificationSvc: nil,
	}

	ctx := context.Background()

	// Should not panic when notification service is nil
	svc.triggerNotifications(ctx, uuid.NewString())
}

// --- enqueuePublishRetry tests ---

func TestEnqueuePublishRetry_NilEnqueuer(t *testing.T) {
	svc := &service{fedEnq: nil}
	ctx := context.Background()

	err := svc.enqueuePublishRetry(ctx, uuid.NewString(), 30*time.Second)

	assert.NoError(t, err, "Should return nil when fedEnq is nil")
}

func TestEnqueuePublishRetry_WithEnqueuer(t *testing.T) {
	mockEnq := &mockJobEnqueuer{}
	svc := &service{fedEnq: mockEnq}
	ctx := context.Background()

	err := svc.enqueuePublishRetry(ctx, uuid.NewString(), 30*time.Second)

	assert.NoError(t, err)
	assert.True(t, mockEnq.enqueueJobCalled, "EnqueueJob should be called")
	assert.Equal(t, "publish_post", mockEnq.lastJobType)
}

// --- uploadMediaToIPFS tests ---

func TestUploadMediaToIPFS_NilClient(t *testing.T) {
	svc := &service{ipfsClient: nil}
	ctx := context.Background()

	thumbCID, previewCID := svc.uploadMediaToIPFS(ctx, "/some/thumb.jpg", "/some/preview.webp")

	assert.Empty(t, thumbCID)
	assert.Empty(t, previewCID)
}

// --- uploadVariantsToIPFS tests ---

func TestUploadVariantsToIPFS_NilClient(t *testing.T) {
	svc := &service{ipfsClient: nil}
	ctx := context.Background()

	job := &domain.EncodingJob{
		TargetResolutions: []string{"720p", "1080p"},
	}

	cids, err := svc.uploadVariantsToIPFS(ctx, job, "/tmp/output")

	assert.NoError(t, err)
	assert.Empty(t, cids)
}

// --- updateVideoInfo tests ---

func TestUpdateVideoInfo(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	videoID := uuid.NewString()
	// Video with StatusProcessing (waitTranscoding=true case) — updateVideoInfo
	// should preserve the existing status, not force StatusCompleted.
	video := &domain.Video{
		ID:          videoID,
		Status:      domain.StatusProcessing,
		OutputPaths: map[string]string{"source": "/uploads/original.mp4"},
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo: videoRepo,
		cfg:       cfg,
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           videoID,
		TargetResolutions: []string{"720p", "1080p"},
	}

	processedCIDs := map[string]string{"720p": "QmABC", "1080p": "QmDEF"}

	err := svc.updateVideoInfo(ctx, job, tempDir, "/thumb.jpg", "/preview.webp", processedCIDs, "QmThumb", "QmPreview")

	assert.NoError(t, err)

	// Verify the video was updated but status preserved (not forced to Completed)
	updatedVideo, _ := videoRepo.GetByID(ctx, videoID)
	assert.Equal(t, domain.StatusProcessing, updatedVideo.Status)
	assert.NotEmpty(t, updatedVideo.OutputPaths)
	assert.Equal(t, "/uploads/original.mp4", updatedVideo.OutputPaths["source"]) // source key preserved
	assert.Equal(t, "QmABC", updatedVideo.ProcessedCIDs["720p"])
	assert.Equal(t, "QmThumb", updatedVideo.ThumbnailCID)
}

func TestUpdateVideoInfo_BackfillsSourceFromJobPath(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	videoID := uuid.NewString()
	video := &domain.Video{
		ID:          videoID,
		Status:      domain.StatusCompleted,
		OutputPaths: map[string]string{},
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo:  videoRepo,
		cfg:        cfg,
		uploadsDir: t.TempDir(),
	}

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           videoID,
		SourceFilePath:    filepath.Join("/tmp", videoID+".mov"),
		TargetResolutions: []string{"720p"},
	}

	err := svc.updateVideoInfo(context.Background(), job, t.TempDir(), "/thumb.jpg", "/preview.webp", nil, "", "")

	assert.NoError(t, err)

	updatedVideo, _ := videoRepo.GetByID(context.Background(), videoID)
	assert.Equal(t, "/static/web-videos/"+videoID+".mov", updatedVideo.OutputPaths["source"])
	assert.Equal(t, "/static/streaming-playlists/hls/"+videoID+"/master.m3u8", updatedVideo.OutputPaths["master"])
}

// --- Run tests ---

func TestRun_ContextCancellation(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, t.TempDir(), cfg, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx, 1)

	// Should exit cleanly on context cancellation
	assert.NoError(t, err)
}

func TestRun_DefaultWorkerCount(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, t.TempDir(), cfg, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// workers=0 should use default (NumCPU, min 2)
	err := svc.Run(ctx, 0)
	assert.NoError(t, err)
}

func TestRun_NegativeWorkerCount(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, t.TempDir(), cfg, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx, -5)
	assert.NoError(t, err)
}

func TestRun_ProcessesJobThenExits(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	tempDir := t.TempDir()
	svc := NewService(repo, videoRepo, nil, tempDir, cfg, nil, nil, nil)
	ctx := context.Background()

	// Create a job with missing source file - it will be picked up and fail
	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "/nonexistent/file.mp4",
		SourceResolution:  "720p",
		TargetResolutions: []string{"480p"},
		Status:            domain.EncodingStatusPending,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, repo.CreateJob(ctx, job))

	runCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = svc.Run(runCtx, 1)

	// Job should have been processed (and failed)
	failedJob, err := repo.GetJob(ctx, job.ID)
	if err == nil && failedJob != nil {
		assert.Equal(t, domain.EncodingStatusFailed, failedJob.Status)
	}
	// If job was deleted, that's fine too (completed jobs get deleted)
}

// --- triggerNotifications with video ---

func TestTriggerNotifications_WithVideoAndService(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	videoID := uuid.NewString()
	video := &domain.Video{
		ID:     videoID,
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	mockNotif := &mockNotificationService{}
	svc := &service{
		videoRepo:       videoRepo,
		cfg:             cfg,
		notificationSvc: mockNotif,
	}

	ctx := context.Background()
	svc.triggerNotifications(ctx, videoID)

	assert.True(t, mockNotif.createVideoCalled, "CreateVideoNotificationForSubscribers should be called")
}

func TestTriggerNotifications_VideoNotFound(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	mockNotif := &mockNotificationService{}
	svc := &service{
		videoRepo:       videoRepo,
		cfg:             cfg,
		notificationSvc: mockNotif,
	}

	ctx := context.Background()
	// Should not panic or call notification service when video not found
	svc.triggerNotifications(ctx, "nonexistent-video-id")

	assert.False(t, mockNotif.createVideoCalled, "Should not create notification for missing video")
}

// --- triggerCaptionGeneration with invalid UUID ---

func TestTriggerCaptionGeneration_InvalidVideoUUID(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:              "ffmpeg",
		HLSSegmentDuration:      4,
		EnableCaptionGeneration: true,
	}

	// Video with non-UUID ID
	video := &domain.Video{
		ID:     "not-a-valid-uuid",
		UserID: uuid.NewString(),
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	mockGen := &mockCaptionGenerator{}
	svc := &service{
		videoRepo:  videoRepo,
		cfg:        cfg,
		captionGen: mockGen,
	}

	ctx := context.Background()
	svc.triggerCaptionGeneration(ctx, "not-a-valid-uuid")

	assert.False(t, mockGen.createJobCalled, "Should not create caption job for invalid video UUID")
}

func TestTriggerCaptionGeneration_InvalidUserUUID(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:              "ffmpeg",
		HLSSegmentDuration:      4,
		EnableCaptionGeneration: true,
	}

	videoID := uuid.New()
	// Video with non-UUID user ID
	video := &domain.Video{
		ID:     videoID.String(),
		UserID: "not-a-valid-user-uuid",
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	mockGen := &mockCaptionGenerator{}
	svc := &service{
		videoRepo:  videoRepo,
		cfg:        cfg,
		captionGen: mockGen,
	}

	ctx := context.Background()
	svc.triggerCaptionGeneration(ctx, videoID.String())

	assert.False(t, mockGen.createJobCalled, "Should not create caption job for invalid user UUID")
}

func TestTriggerCaptionGeneration_NilConfig(t *testing.T) {
	videoRepo := NewMockVideoRepository()

	videoID := uuid.New()
	video := &domain.Video{
		ID:     videoID.String(),
		UserID: uuid.NewString(),
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	mockGen := &mockCaptionGenerator{}
	svc := &service{
		videoRepo:  videoRepo,
		cfg:        nil, // nil config means generation is enabled (no config check blocks it)
		captionGen: mockGen,
	}

	ctx := context.Background()
	svc.triggerCaptionGeneration(ctx, videoID.String())

	assert.True(t, mockGen.createJobCalled, "Should create caption job when config is nil (no disabled check)")
}

// --- encodeResolutions with empty target resolutions ---

func TestEncodeResolutions_EmptyResolutions(t *testing.T) {
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	ctx := context.Background()
	tempDir := t.TempDir()

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		TargetResolutions: []string{}, // Empty
	}

	updateCalled := 0
	update := func() { updateCalled++ }

	err := svc.encodeResolutions(ctx, job, tempDir, update)

	assert.NoError(t, err)
	assert.Equal(t, 0, updateCalled, "No resolutions means no updates")
}

func TestEncodeResolutions_InvalidResolution(t *testing.T) {
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}
	svc := &service{cfg: cfg}

	ctx := context.Background()
	tempDir := t.TempDir()

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		TargetResolutions: []string{"invalid_resolution"},
	}

	updateCalled := 0
	update := func() { updateCalled++ }

	err := svc.encodeResolutions(ctx, job, tempDir, update)

	// Invalid resolution should be skipped (HeightForResolution returns false)
	assert.NoError(t, err)
	assert.Equal(t, 0, updateCalled, "Invalid resolutions should be skipped")
}

// --- Mock helpers for encoding unit tests ---

type mockCaptionGenerator struct {
	createJobCalled bool
}

func (m *mockCaptionGenerator) CreateJob(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error) {
	m.createJobCalled = true
	return &domain.CaptionGenerationJob{ID: uuid.New()}, nil
}

type mockJobEnqueuer struct {
	enqueueJobCalled bool
	lastJobType      string
}

func (m *mockJobEnqueuer) EnqueueJob(_ context.Context, jobType string, _ any, _ time.Time) (string, error) {
	m.enqueueJobCalled = true
	m.lastJobType = jobType
	return uuid.NewString(), nil
}

type mockNotificationService struct {
	createVideoCalled bool
}

func (m *mockNotificationService) CreateVideoNotificationForSubscribers(_ context.Context, _ *domain.Video, _ string) error {
	m.createVideoCalled = true
	return nil
}

func (m *mockNotificationService) CreateMessageNotification(_ context.Context, _ *domain.Message, _ string) error {
	return nil
}

func (m *mockNotificationService) CreateMessageReadNotification(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) error {
	return nil
}

func (m *mockNotificationService) GetUserNotifications(_ context.Context, _ uuid.UUID, _ domain.NotificationFilter) ([]domain.Notification, error) {
	return nil, nil
}

func (m *mockNotificationService) MarkAsRead(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

func (m *mockNotificationService) MarkAllAsRead(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockNotificationService) DeleteNotification(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

func (m *mockNotificationService) GetUnreadCount(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}

func (m *mockNotificationService) GetStats(_ context.Context, _ uuid.UUID) (*domain.NotificationStats, error) {
	return nil, nil
}
