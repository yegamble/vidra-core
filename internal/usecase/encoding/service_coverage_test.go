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

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/ipfs"
	"vidra-core/internal/storage"

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

// testCIDv1 is a valid CIDv1 for use in mock IPFS server responses.
const testCIDv1 = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

// newMockIPFSServer creates a mock IPFS server that handles add and pin endpoints.
func newMockIPFSServer(t *testing.T, cid string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v0/add":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"Name":"file","Hash":"%s","Size":"100"}`, cid)
		case "/api/v0/pin/add":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"Pins":["%s"]}`, cid)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestUploadVariantsToIPFS_EnabledClient_MasterPlaylistUpload(t *testing.T) {
	server := newMockIPFSServer(t, testCIDv1)

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
	server := newMockIPFSServer(t, testCIDv1)

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
	server := newMockIPFSServer(t, testCIDv1)

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
	server := newMockIPFSServer(t, testCIDv1)

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
	server := newMockIPFSServer(t, testCIDv1)

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
	server := newMockIPFSServer(t, testCIDv1)

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
	server := newMockIPFSServer(t, testCIDv1)

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
	err := svc.transcodeHLS(ctx, "/input.mp4", 720, "/out.m3u8", "/seg_%05d.ts", 0, nil)

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
	err := svc.execFFmpegWithProgress(ctx, []string{"-i", "test.mp4"}, 60*time.Second, "720p", func(int) {})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ffmpeg binary path")
}

func TestExecFFmpegWithProgress_ZeroDurationSkipsProgress(t *testing.T) {
	svc := &service{
		cfg: &config.Config{
			FFMPEGPath:         "/path/ffmpeg;evil",
			HLSSegmentDuration: 4,
		},
	}

	ctx := context.Background()
	err := svc.execFFmpegWithProgress(ctx, []string{"-i", "test.mp4"}, 0, "720p", func(int) {})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ffmpeg binary path")
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

// --- WithActivityPubPublisher / WithStoryboardRepo tests ---

func TestWithActivityPubPublisher(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}

	svc := NewService(repo, videoRepo, nil, "/tmp", cfg, nil, nil, nil)
	concrete := svc.(*service)

	assert.Nil(t, concrete.activitypub)

	mockPub := &mockPublisher{}
	result := concrete.WithActivityPubPublisher(mockPub)

	assert.NotNil(t, result)
	assert.NotNil(t, concrete.activitypub)
}

func TestWithStoryboardRepo(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}

	svc := NewService(repo, videoRepo, nil, "/tmp", cfg, nil, nil, nil)
	concrete := svc.(*service)

	assert.Nil(t, concrete.storyboardRepo)

	mockSB := &mockStoryboardRepo{}
	result := concrete.WithStoryboardRepo(mockSB)

	assert.NotNil(t, result)
	assert.NotNil(t, concrete.storyboardRepo)
}

// --- cleanupOriginalFile tests ---

func TestCleanupOriginalFile_KeepOriginalTrue(t *testing.T) {
	svc := &service{
		cfg: &config.Config{KeepOriginalFile: true, FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
	}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "video.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("video data"), 0600))

	job := &domain.EncodingJob{VideoID: uuid.NewString(), SourceFilePath: sourceFile}
	svc.cleanupOriginalFile(context.Background(), job, false)

	assert.FileExists(t, sourceFile, "file should NOT be removed when KeepOriginalFile=true")
}

func TestCleanupOriginalFile_NilConfig(t *testing.T) {
	svc := &service{cfg: nil}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "video.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("video data"), 0600))

	job := &domain.EncodingJob{VideoID: uuid.NewString(), SourceFilePath: sourceFile}
	svc.cleanupOriginalFile(context.Background(), job, false)

	assert.FileExists(t, sourceFile, "file should NOT be removed when config is nil")
}

func TestCleanupOriginalFile_S3EnabledButMigrationFailed(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	svc := &service{
		cfg:       &config.Config{KeepOriginalFile: false, FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
		videoRepo: videoRepo,
		s3Backend: &mockStorageBackend{}, // S3 enabled
	}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "video.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("video data"), 0600))

	job := &domain.EncodingJob{VideoID: uuid.NewString(), SourceFilePath: sourceFile}
	svc.cleanupOriginalFile(context.Background(), job, false) // s3Migrated=false

	assert.FileExists(t, sourceFile, "file should NOT be removed when S3 is enabled but migration failed")
}

func TestCleanupOriginalFile_SuccessfulRemoval(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	videoID := uuid.NewString()
	require.NoError(t, videoRepo.Create(context.Background(), &domain.Video{ID: videoID}))

	svc := &service{
		cfg:       &config.Config{KeepOriginalFile: false, FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
		videoRepo: videoRepo,
	}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "video.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("video data"), 0600))

	job := &domain.EncodingJob{VideoID: videoID, SourceFilePath: sourceFile}
	svc.cleanupOriginalFile(context.Background(), job, false) // no S3 backend, s3Migrated irrelevant

	assert.NoFileExists(t, sourceFile, "file should be removed")
}

func TestCleanupOriginalFile_FileAlreadyGone(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	svc := &service{
		cfg:       &config.Config{KeepOriginalFile: false, FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
		videoRepo: videoRepo,
	}

	job := &domain.EncodingJob{VideoID: uuid.NewString(), SourceFilePath: "/nonexistent/video.mp4"}
	// Should not panic
	assert.NotPanics(t, func() {
		svc.cleanupOriginalFile(context.Background(), job, false)
	})
}

// --- finalizeVideoState tests ---

func TestFinalizeVideoState_PublishesProcessingVideosWithDisplayAssets(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	videoID := uuid.NewString()
	video := &domain.Video{
		ID:              videoID,
		Status:          domain.StatusProcessing,
		WaitTranscoding: true,
		ThumbnailPath:   "/static/thumbnails/" + videoID + "_thumb.jpg",
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo: videoRepo,
		cfg:       &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
	}

	svc.finalizeVideoState(context.Background(), videoID)

	updated, err := videoRepo.GetByID(context.Background(), videoID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, updated.Status, "should transition to completed")
}

func TestFinalizeVideoState_KeepsProcessingWithoutDisplayAssets(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	videoID := uuid.NewString()
	video := &domain.Video{
		ID:              videoID,
		Status:          domain.StatusProcessing,
		WaitTranscoding: false,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo: videoRepo,
		cfg:       &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
	}

	svc.finalizeVideoState(context.Background(), videoID)

	updated, err := videoRepo.GetByID(context.Background(), videoID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusProcessing, updated.Status, "should stay processing until a display asset exists")
}

func TestFinalizeVideoState_VideoNotFound(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	svc := &service{
		videoRepo: videoRepo,
		cfg:       &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
	}

	// Should not panic
	assert.NotPanics(t, func() {
		svc.finalizeVideoState(context.Background(), "nonexistent-id")
	})
}

func TestFinalizeVideoState_AlreadyCompleted(t *testing.T) {
	videoRepo := NewMockVideoRepository()
	videoID := uuid.NewString()
	video := &domain.Video{
		ID:              videoID,
		Status:          domain.StatusCompleted,
		WaitTranscoding: true,
	}
	require.NoError(t, videoRepo.Create(context.Background(), video))

	svc := &service{
		videoRepo: videoRepo,
		cfg:       &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
	}

	svc.finalizeVideoState(context.Background(), videoID)

	updated, err := videoRepo.GetByID(context.Background(), videoID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCompleted, updated.Status, "should remain completed")
}

// --- logPipelineCompletion tests ---

func TestLogPipelineCompletion_NoPanic(t *testing.T) {
	svc := &service{
		cfg: &config.Config{KeepOriginalFile: true, FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
	}

	job := &domain.EncodingJob{
		VideoID:           uuid.NewString(),
		TargetResolutions: []string{"720p", "1080p"},
		SourceResolution:  "1080p",
	}

	assert.NotPanics(t, func() {
		svc.logPipelineCompletion(job, map[string]string{"720p": "QmABC"}, true, true, true, true)
	})
}

func TestLogPipelineCompletion_NilConfig(t *testing.T) {
	svc := &service{cfg: nil}

	job := &domain.EncodingJob{
		VideoID:           uuid.NewString(),
		TargetResolutions: []string{},
		SourceResolution:  "720p",
	}

	assert.NotPanics(t, func() {
		svc.logPipelineCompletion(job, nil, false, false, false, false)
	})
}

// --- generateThumbnail / generatePreviewWebP tests ---

func TestGenerateThumbnail_InvalidBinary(t *testing.T) {
	svc := &service{
		cfg: &config.Config{FFMPEGPath: "nonexistent_ffmpeg_binary_xyz", HLSSegmentDuration: 4},
	}

	err := svc.generateThumbnail(context.Background(), "/input.mp4", "/output.jpg")
	assert.Error(t, err)
}

func TestGeneratePreviewWebP_InvalidBinary(t *testing.T) {
	svc := &service{
		cfg: &config.Config{FFMPEGPath: "nonexistent_ffmpeg_binary_xyz", HLSSegmentDuration: 4},
	}

	err := svc.generatePreviewWebP(context.Background(), "/input.mp4", "/output.webp")
	assert.Error(t, err)
}

// --- generateStoryboard tests ---

func TestGenerateStoryboard_NilStoryboardRepo(t *testing.T) {
	svc := &service{
		cfg:            &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4},
		storyboardRepo: nil,
	}

	job := &domain.EncodingJob{VideoID: uuid.NewString(), SourceFilePath: "/input.mp4"}

	// Should return early without panic when storyboardRepo is nil
	assert.NotPanics(t, func() {
		svc.generateStoryboard(context.Background(), job)
	})
}

// --- contentTypeForPath tests ---

func TestContentTypeForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"stream.m3u8", "application/vnd.apple.mpegurl"},
		{"segment_00001.ts", "video/MP2T"},
		{"video.mp4", "application/octet-stream"},
		{"file.M3U8", "application/vnd.apple.mpegurl"},
		{"segment.TS", "video/MP2T"},
		{"data.bin", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, contentTypeForPath(tt.path))
		})
	}
}

// --- encodeResolutions with valid resolution but ffmpeg failure ---

func TestEncodeResolutions_ValidResolutionFFmpegFails(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "nonexistent_ffmpeg_binary_xyz",
		HLSSegmentDuration: 4,
	}

	svc := &service{repo: repo, videoRepo: videoRepo, cfg: cfg}

	ctx := context.Background()
	tempDir := t.TempDir()

	// Create a fake source file so getVideoDuration has something to try
	sourceFile := filepath.Join(tempDir, "source.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("fake"), 0600))

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    sourceFile,
		TargetResolutions: []string{"720p"},
	}

	updateCalled := 0
	update := func() { updateCalled++ }

	err := svc.encodeResolutions(ctx, job, tempDir, update)

	// Should fail because FFmpeg binary doesn't exist
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "encode 720p")
}

// --- processJob exercising validation + output dir + failure at encoding ---

func TestProcessJob_ValidationPassesEncodingFails(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "nonexistent_ffmpeg_binary_xyz",
		HLSSegmentDuration: 4,
	}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("fake video data"), 0600))

	svc := &service{repo: repo, videoRepo: videoRepo, uploadsDir: tempDir, cfg: cfg}

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    sourceFile,
		SourceResolution:  "1080p",
		TargetResolutions: []string{"720p"},
		Status:            domain.EncodingStatusProcessing,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, repo.CreateJob(context.Background(), job))

	err := svc.processJob(context.Background(), job)

	assert.Error(t, err, "should fail because ffmpeg binary doesn't exist")
}

func TestProcessJob_EmptyResolutionsReachesMediaAssets(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{
		FFMPEGPath:         "nonexistent_ffmpeg_binary_xyz",
		HLSSegmentDuration: 4,
	}

	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("fake video data"), 0600))

	svc := &service{repo: repo, videoRepo: videoRepo, uploadsDir: tempDir, cfg: cfg}

	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           uuid.NewString(),
		SourceFilePath:    sourceFile,
		SourceResolution:  "1080p",
		TargetResolutions: []string{}, // empty — encodeResolutions succeeds
		Status:            domain.EncodingStatusProcessing,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	require.NoError(t, repo.CreateJob(context.Background(), job))

	err := svc.processJob(context.Background(), job)

	// Should get past encodeResolutions and generateMasterPlaylist,
	// then fail at generateMediaAssets (thumbnail generation needs ffmpeg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "thumbnail")
}

// --- uploadHLSToS3 tests ---

func TestUploadHLSToS3_NilBackend(t *testing.T) {
	svc := &service{s3Backend: nil, cfg: &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}}

	urls, err := svc.uploadHLSToS3(context.Background(), "vid-1", "/src.mp4", "/out", "/thumb.jpg", "/prev.webp", []string{"720p"})

	assert.NoError(t, err)
	assert.Empty(t, urls)
}

// --- WithS3Backend test ---

func TestWithS3Backend(t *testing.T) {
	repo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}

	svc := NewService(repo, videoRepo, nil, "/tmp", cfg, nil, nil, nil)
	concrete := svc.(*service)

	assert.Nil(t, concrete.s3Backend)

	mock := &mockStorageBackend{}
	result := concrete.WithS3Backend(mock)

	assert.NotNil(t, result)
	assert.NotNil(t, concrete.s3Backend)
}

// --- mock helpers ---

type mockStoryboardRepo struct{}

func (m *mockStoryboardRepo) Create(_ context.Context, _ *domain.VideoStoryboard) error { return nil }

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

func (r *errorEncodingRepository) ListJobsByStatus(_ context.Context, _ string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
