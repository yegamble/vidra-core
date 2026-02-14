package encoding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/ipfs"
	"athena/internal/storage"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessNext_GetNextJobError(t *testing.T) {
	repo := &errorEncodingRepository{getNextJobErr: fmt.Errorf("database connection lost")}
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := &service{repo: repo, videoRepo: videoRepo, uploadsDir: t.TempDir(), cfg: cfg}
	ctx := context.Background()

	processed, err := svc.ProcessNext(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database connection lost")
	assert.False(t, processed)
}

func TestProcessNext_UpdateJobError(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(repo, videoRepo, nil, t.TempDir(), cfg, nil, nil, nil)
	ctx := context.Background()

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    "",
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
}

func TestUploadVariantsToIPFS_EnabledClient_MasterPlaylistUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmTestCID123","Size":"100"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	masterPath := filepath.Join(tempDir, "master.m3u8")
	require.NoError(t, os.WriteFile(masterPath, []byte("#EXTM3U\n"), 0600))

	resDir := filepath.Join(tempDir, "720p")
	require.NoError(t, os.MkdirAll(resDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "stream.m3u8"), []byte("#EXTM3U\n"), 0600))

	job := &domain.EncodingJob{
		TargetResolutions: []string{"720p"},
	}

	cids, err := svc.uploadVariantsToIPFS(ctx, job, tempDir)

	assert.NoError(t, err)
	assert.Contains(t, cids, "master")
	assert.Contains(t, cids, "720p")
}

func TestUploadVariantsToIPFS_EnabledClient_NoMasterPlaylist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmTestCID","Size":"100"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	resDir := filepath.Join(tempDir, "480p")
	require.NoError(t, os.MkdirAll(resDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "stream.m3u8"), []byte("#EXTM3U\n"), 0600))

	job := &domain.EncodingJob{
		TargetResolutions: []string{"480p"},
	}

	cids, err := svc.uploadVariantsToIPFS(ctx, job, tempDir)

	assert.NoError(t, err)
	assert.NotContains(t, cids, "master")
	assert.Contains(t, cids, "480p")
}

func TestUploadVariantsToIPFS_EnabledClient_ResolutionDirMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmTestCID","Size":"100"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	job := &domain.EncodingJob{
		TargetResolutions: []string{"720p"},
	}

	cids, err := svc.uploadVariantsToIPFS(ctx, job, tempDir)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolution directory not found")
	_ = cids
}

func TestUploadVariantsToIPFS_EnabledClient_MasterUploadFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	masterPath := filepath.Join(tempDir, "master.m3u8")
	require.NoError(t, os.WriteFile(masterPath, []byte("#EXTM3U\n"), 0600))

	job := &domain.EncodingJob{
		TargetResolutions: []string{"720p"},
	}

	_, err := svc.uploadVariantsToIPFS(ctx, job, tempDir)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to upload master playlist")
}

func TestUploadVariantsToIPFS_EnabledClient_SkipsInvalidResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmTestCID","Size":"100"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	job := &domain.EncodingJob{
		TargetResolutions: []string{"unknown_res"},
	}

	cids, err := svc.uploadVariantsToIPFS(ctx, job, tempDir)

	assert.NoError(t, err)
	assert.Empty(t, cids)
}

func TestUploadVariantsToIPFS_DisabledClient(t *testing.T) {
	client := ipfs.NewClient("", "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	job := &domain.EncodingJob{
		TargetResolutions: []string{"720p"},
	}

	cids, err := svc.uploadVariantsToIPFS(ctx, job, t.TempDir())

	assert.NoError(t, err)
	assert.Empty(t, cids)
}

func TestUploadMediaToIPFS_EnabledClient_BothFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmMediaCID","Size":"50"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	thumbPath := filepath.Join(tempDir, "thumb.jpg")
	previewPath := filepath.Join(tempDir, "preview.webp")
	require.NoError(t, os.WriteFile(thumbPath, []byte("fake thumbnail"), 0600))
	require.NoError(t, os.WriteFile(previewPath, []byte("fake preview"), 0600))

	thumbCID, previewCID := svc.uploadMediaToIPFS(ctx, thumbPath, previewPath)

	assert.NotEmpty(t, thumbCID)
	assert.NotEmpty(t, previewCID)
}

func TestUploadMediaToIPFS_EnabledClient_EmptyPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmMediaCID","Size":"50"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()

	thumbCID, previewCID := svc.uploadMediaToIPFS(ctx, "", "")

	assert.Empty(t, thumbCID)
	assert.Empty(t, previewCID)
}

func TestUploadMediaToIPFS_EnabledClient_FilesNotExist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Name":"file","Hash":"QmMediaCID","Size":"50"}`)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()

	thumbCID, previewCID := svc.uploadMediaToIPFS(ctx, "/nonexistent/thumb.jpg", "/nonexistent/preview.webp")

	assert.Empty(t, thumbCID, "Should return empty CID for non-existent thumbnail")
	assert.Empty(t, previewCID, "Should return empty CID for non-existent preview")
}

func TestUploadMediaToIPFS_EnabledClient_UploadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	tempDir := t.TempDir()

	thumbPath := filepath.Join(tempDir, "thumb.jpg")
	previewPath := filepath.Join(tempDir, "preview.webp")
	require.NoError(t, os.WriteFile(thumbPath, []byte("fake"), 0600))
	require.NoError(t, os.WriteFile(previewPath, []byte("fake"), 0600))

	thumbCID, previewCID := svc.uploadMediaToIPFS(ctx, thumbPath, previewPath)

	assert.Empty(t, thumbCID, "Should return empty CID on upload error")
	assert.Empty(t, previewCID, "Should return empty CID on upload error")
}

func TestUploadMediaToIPFS_DisabledClient(t *testing.T) {
	client := ipfs.NewClient("", "", 5*time.Second)
	svc := &service{
		ipfsClient: client,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()

	thumbCID, previewCID := svc.uploadMediaToIPFS(ctx, "/some/thumb.jpg", "/some/preview.webp")

	assert.Empty(t, thumbCID)
	assert.Empty(t, previewCID)
}

func TestExecFFmpeg_InvalidBinaryPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/usr/bin/ffmpeg;rm -rf /",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	err := svc.execFFmpeg(ctx, []string{"-version"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ffmpeg binary path")
}

func TestExecFFmpeg_NonexistentBinary(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "nonexistent_binary_that_does_not_exist_anywhere",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	err := svc.execFFmpeg(ctx, []string{"-version"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ffmpeg failed")
}

func TestExecFFmpeg_EmptyPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()

	_ = svc.execFFmpeg(ctx, []string{"-version"})
}

func TestExecFFmpeg_ContextCancellation(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "",
			HLSSegmentDuration: 4,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.execFFmpeg(ctx, []string{"-version"})

	assert.Error(t, err)
}

func TestGenerateMediaAssets_ThumbnailDirCreationFail(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()

	sp := storage.NewPaths("/dev/null/impossible")

	job := &domain.EncodingJob{
		VideoID: uuid.NewString(),
	}

	updateCalled := 0
	update := func() { updateCalled++ }

	_, _, err := svc.generateMediaAssets(ctx, job, &sp, update)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create thumbnail dir")
	assert.Equal(t, 0, updateCalled)
}

func TestProcessJob_WithATProtoPublish(t *testing.T) {

	videoRepo := NewMockVideoRepository()
	mockPub := &mockPublisher{}

	videoID := uuid.NewString()
	video := &domain.Video{
		ID:     videoID,
		UserID: uuid.NewString(),
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo: videoRepo,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
		atproto: mockPub,
	}

	ctx := context.Background()
	if svc.atproto != nil {
		if v, err := svc.videoRepo.GetByID(ctx, videoID); err == nil && v != nil {
			_ = svc.atproto.PublishVideo(ctx, v)
		}
	}

	assert.True(t, mockPub.publishCalled)
}

func TestProcessJob_ATProtoPublishFailsEnqueuesRetry(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	mockPub := &mockPublisher{shouldFail: true}
	mockEnq := &mockJobEnqueuer{}

	videoID := uuid.NewString()
	video := &domain.Video{
		ID:     videoID,
		UserID: uuid.NewString(),
		Status: domain.StatusCompleted,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo: videoRepo,
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
		atproto: mockPub,
		fedEnq:  mockEnq,
	}

	ctx := context.Background()
	if svc.atproto != nil {
		if v, err := svc.videoRepo.GetByID(ctx, videoID); err == nil && v != nil {
			if err := svc.atproto.PublishVideo(ctx, v); err != nil && svc.fedEnq != nil {
				_ = svc.enqueuePublishRetry(ctx, v.ID, 30*time.Second)
			}
		}
	}

	assert.True(t, mockPub.publishCalled)
	assert.True(t, mockEnq.enqueueJobCalled)
	assert.Equal(t, "publish_post", mockEnq.lastJobType)
}

func TestTranscodeHLS_InvalidBinaryPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/usr/bin/ffmpeg|evil",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	err := svc.transcodeHLS(ctx, "/input.mp4", 720, "/out.m3u8", "/seg_%05d.ts", "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ffmpeg binary path")
}

func TestGetVideoDuration_InvalidInput(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	_, err := svc.getVideoDuration(ctx, "/nonexistent/video.mp4")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ffprobe failed")
}

func TestGetVideoDuration_CustomFFMPEGPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/nonexistent/path/ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	_, err := svc.getVideoDuration(ctx, "/nonexistent/video.mp4")

	assert.Error(t, err)
}

func TestExecFFmpegWithProgress_InvalidBinaryPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/path/ffmpeg;evil",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	err := svc.execFFmpegWithProgress(ctx, []string{"-i", "test.mp4"}, "job1", "720p")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ffmpeg binary path")
}

func TestExecFFmpegWithProgress_NoInputFile(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "ffmpeg",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	err := svc.execFFmpegWithProgress(ctx, []string{"-version"}, "job1", "720p")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no input file found")
}

func TestValidateBinaryPath_DirectoryTraversal(t *testing.T) {
	err := validateBinaryPath("/usr/local/../../../etc/passwd")
	assert.NoError(t, err)

	err = validateBinaryPath("../../../bin/sh")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "directory traversal")
}

func TestH264Encoder_Encode_InvalidBinaryPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/bin/ffmpeg$evil",
			HLSSegmentDuration: 4,
		},
	}

	encoder := NewH264Encoder(svc)
	ctx := context.Background()

	err := encoder.Encode(ctx, "/input.mp4", 720, "/out.m3u8", "/seg_%05d.ts")

	assert.Error(t, err)
}

func TestVP9Encoder_Encode_InvalidBinaryPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/bin/ffmpeg$evil",
			HLSSegmentDuration: 4,
			VP9Quality:         31,
			VP9Speed:           2,
		},
	}

	encoder := NewVP9Encoder(svc)
	ctx := context.Background()

	err := encoder.Encode(ctx, "/input.mp4", 720, "/out.m3u8", "/seg_%05d.ts")

	assert.Error(t, err)
}

func TestAV1Encoder_Encode_InvalidBinaryPath(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/bin/ffmpeg$evil",
			HLSSegmentDuration: 4,
		},
	}

	encoder := NewAV1Encoder(svc)
	ctx := context.Background()

	err := encoder.Encode(ctx, "/input.mp4", 720, "/out.m3u8", "/seg_%05d.ts")

	assert.Error(t, err)
}

type mockPublisher struct {
	publishCalled bool
	shouldFail    bool
}

func (m *mockPublisher) PublishVideo(_ context.Context, _ *domain.Video) error {
	m.publishCalled = true
	if m.shouldFail {
		return fmt.Errorf("publish failed")
	}
	return nil
}

type errorEncodingRepository struct {
	getNextJobErr error
}

func (r *errorEncodingRepository) CreateJob(_ context.Context, _ *domain.EncodingJob) error {
	return nil
}
func (r *errorEncodingRepository) GetJob(_ context.Context, _ string) (*domain.EncodingJob, error) {
	return nil, fmt.Errorf("not implemented")
}
func (r *errorEncodingRepository) GetJobByVideoID(_ context.Context, _ string) (*domain.EncodingJob, error) {
	return nil, fmt.Errorf("not implemented")
}
func (r *errorEncodingRepository) UpdateJob(_ context.Context, _ *domain.EncodingJob) error {
	return nil
}
func (r *errorEncodingRepository) UpdateJobStatus(_ context.Context, _ string, _ domain.EncodingStatus) error {
	return nil
}
func (r *errorEncodingRepository) UpdateJobProgress(_ context.Context, _ string, _ int) error {
	return nil
}
func (r *errorEncodingRepository) SetJobError(_ context.Context, _ string, _ string) error {
	return nil
}
func (r *errorEncodingRepository) GetPendingJobs(_ context.Context, _ int) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (r *errorEncodingRepository) GetNextJob(_ context.Context) (*domain.EncodingJob, error) {
	return nil, r.getNextJobErr
}
func (r *errorEncodingRepository) DeleteJob(_ context.Context, _ string) error {
	return nil
}
func (r *errorEncodingRepository) GetJobCounts(_ context.Context) (map[string]int64, error) {
	return nil, nil
}
func (r *errorEncodingRepository) ResetStaleJobs(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (r *errorEncodingRepository) GetJobsByVideoID(_ context.Context, _ string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (r *errorEncodingRepository) GetActiveJobsByVideoID(_ context.Context, _ string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
