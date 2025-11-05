package encoding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingService_ProcessMultipleResolutions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping resolution encoding tests in short mode")
	}

	// Check if ffmpeg is available
	if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); os.IsNotExist(err) {
		t.Skip("FFmpeg not available, skipping encoding tests")
	}

	encodingRepo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()

	tempDir := t.TempDir()
	cfg := &config.Config{
		FFMPEGPath:         "/opt/homebrew/bin/ffmpeg",
		HLSSegmentDuration: 4,
	}

	svc := NewService(encodingRepo, videoRepo, nil, tempDir, cfg, nil, nil, nil)

	for _, testVideo := range testutil.TestVideos {
		t.Run(testVideo.Name, func(t *testing.T) {
			// Generate test video on-demand
			videoPath, err := testutil.EnsureTestVideoExists(testVideo)
			require.NoError(t, err, "Failed to generate test video for %s", testVideo.Name)

			// Verify source video metadata
			metadata, err := testutil.GetVideoMetadata(videoPath)
			require.NoError(t, err, "Failed to get metadata for %s", testVideo.Name)

			assert.Equal(t, testVideo.Width, metadata.Width, "Width mismatch for %s", testVideo.Name)
			assert.Equal(t, testVideo.Height, metadata.Height, "Height mismatch for %s", testVideo.Name)
			assert.Equal(t, "h264", metadata.VideoCodec, "Expected H.264 codec for %s", testVideo.Name)

			// Test resolution detection
			detectedRes := domain.DetectResolutionFromHeight(metadata.Height)
			assert.Equal(t, testVideo.Resolution, detectedRes, "Resolution detection failed for %s", testVideo.Name)

			// Test target resolution generation
			targetResolutions := domain.GetTargetResolutions(testVideo.Resolution)
			assert.Equal(t, testVideo.ExpectedVars, targetResolutions, "Target resolutions mismatch for %s", testVideo.Name)

			// Create encoding job
			videoID := uuid.NewString()
			job := &domain.EncodingJob{
				ID:                uuid.NewString(),
				VideoID:           videoID,
				SourceFilePath:    videoPath,
				SourceResolution:  testVideo.Resolution,
				TargetResolutions: targetResolutions,
				Status:            domain.EncodingStatusPending,
				Progress:          0,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}

			// Process encoding job
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			err = svc.(*service).processJob(ctx, job)
			require.NoError(t, err, "Encoding failed for %s", testVideo.Name)

			// Verify outputs were created
			outputDir := filepath.Join(tempDir, "streaming-playlists", "hls", videoID)
			assert.DirExists(t, outputDir, "Output directory not created for %s", testVideo.Name)

			// Verify master playlist
			masterPlaylist := filepath.Join(outputDir, "master.m3u8")
			assert.FileExists(t, masterPlaylist, "Master playlist not created for %s", testVideo.Name)

			// Verify individual resolution outputs
			for _, targetRes := range targetResolutions {
				height, ok := domain.HeightForResolution(targetRes)
				require.True(t, ok, "Invalid resolution %s", targetRes)

				resDir := filepath.Join(outputDir, targetRes)
				assert.DirExists(t, resDir, "Resolution directory %s not created for %s", targetRes, testVideo.Name)

				playlist := filepath.Join(resDir, "stream.m3u8")
				assert.FileExists(t, playlist, "Playlist not created for %s/%s", testVideo.Name, targetRes)

				// Verify output video properties
				if len(targetResolutions) > 0 {
					// Check first segment to verify encoding worked
					segments, err := filepath.Glob(filepath.Join(resDir, "segment_*.ts"))
					require.NoError(t, err)

					if len(segments) > 0 {
						segMetadata, err := testutil.GetVideoMetadata(segments[0])
						require.NoError(t, err, "Failed to get segment metadata")

						assert.Equal(t, height, segMetadata.Height, "Encoded height mismatch for %s/%s", testVideo.Name, targetRes)
						assert.Equal(t, "h264", segMetadata.VideoCodec, "Expected H.264 codec in output")
					}
				}
			}

			// Verify thumbnail and preview were created
			thumbnailPath := filepath.Join(tempDir, "thumbnails", videoID+"_thumb.jpg")
			assert.FileExists(t, thumbnailPath, "Thumbnail not created for %s", testVideo.Name)

			previewPath := filepath.Join(tempDir, "previews", videoID+"_preview.webp")
			assert.FileExists(t, previewPath, "Preview not created for %s", testVideo.Name)
		})
	}
}

func TestResolutionDetection(t *testing.T) {
	tests := []struct {
		height   int
		expected string
	}{
		{240, "240p"},
		{360, "360p"},
		{480, "480p"},
		{720, "720p"},
		{1080, "1080p"},
		{1440, "1440p"},
		{2160, "2160p"},
		{4320, "4320p"},
		// Edge cases
		{250, "240p"},   // Closer to 240p
		{300, "240p"},   // Closer to 240p
		{400, "360p"},   // Closer to 360p
		{1000, "1080p"}, // Closer to 1080p
		{1200, "1080p"}, // Closer to 1080p
		{1300, "1440p"}, // Closer to 1440p
		{3000, "2160p"}, // Closer to 2160p
		{5000, "4320p"}, // Closer to 4320p
		{100, "240p"},   // Very low, should default to 240p
		{8000, "4320p"}, // Very high, should default to 4320p
	}

	for _, tt := range tests {
		t.Run(tt.expected+"_"+string(rune(tt.height)), func(t *testing.T) {
			result := domain.DetectResolutionFromHeight(tt.height)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTargetResolutionGeneration(t *testing.T) {
	tests := []struct {
		sourceRes string
		expected  []string
	}{
		{
			sourceRes: "4320p",
			expected:  []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"},
		},
		{
			sourceRes: "2160p",
			expected:  []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p"},
		},
		{
			sourceRes: "1440p",
			expected:  []string{"240p", "360p", "480p", "720p", "1080p", "1440p"},
		},
		{
			sourceRes: "1080p",
			expected:  []string{"240p", "360p", "480p", "720p", "1080p"},
		},
		{
			sourceRes: "720p",
			expected:  []string{"240p", "360p", "480p", "720p"},
		},
		{
			sourceRes: "480p",
			expected:  []string{"240p", "360p", "480p"},
		},
		{
			sourceRes: "360p",
			expected:  []string{"240p", "360p"},
		},
		{
			sourceRes: "240p",
			expected:  []string{"240p"},
		},
		{
			sourceRes: "unknown",
			expected:  []string{"720p", "480p", "360p", "240p"}, // Default fallback
		},
	}

	for _, tt := range tests {
		t.Run(tt.sourceRes, func(t *testing.T) {
			result := domain.GetTargetResolutions(tt.sourceRes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Mock implementations for testing
type mockEncodingRepository struct {
	jobs map[string]*domain.EncodingJob
}

func NewMockEncodingRepository() *mockEncodingRepository {
	return &mockEncodingRepository{
		jobs: make(map[string]*domain.EncodingJob),
	}
}

func (r *mockEncodingRepository) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	r.jobs[job.ID] = job
	return nil
}

func (r *mockEncodingRepository) GetJob(ctx context.Context, id string) (*domain.EncodingJob, error) {
	job, exists := r.jobs[id]
	if !exists {
		return nil, fmt.Errorf("JOB_NOT_FOUND")
	}
	return job, nil
}

func (r *mockEncodingRepository) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	for _, job := range r.jobs {
		if job.VideoID == videoID {
			return job, nil
		}
	}
	return nil, fmt.Errorf("JOB_NOT_FOUND")
}

func (r *mockEncodingRepository) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	r.jobs[job.ID] = job
	return nil
}

func (r *mockEncodingRepository) UpdateJobStatus(ctx context.Context, id string, status domain.EncodingStatus) error {
	if job, exists := r.jobs[id]; exists {
		job.Status = status
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockEncodingRepository) UpdateJobProgress(ctx context.Context, id string, progress int) error {
	if job, exists := r.jobs[id]; exists {
		job.Progress = progress
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockEncodingRepository) SetJobError(ctx context.Context, id string, errorMsg string) error {
	if job, exists := r.jobs[id]; exists {
		job.Status = domain.EncodingStatusFailed
		job.ErrorMessage = errorMsg
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockEncodingRepository) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	var pending []*domain.EncodingJob
	for _, job := range r.jobs {
		if job.Status == domain.EncodingStatusPending && len(pending) < limit {
			pending = append(pending, job)
		}
	}
	return pending, nil
}

func (r *mockEncodingRepository) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	for _, job := range r.jobs {
		if job.Status == domain.EncodingStatusPending {
			job.Status = domain.EncodingStatusProcessing
			job.UpdatedAt = time.Now()
			now := time.Now()
			job.StartedAt = &now
			return job, nil
		}
	}
	return nil, nil
}

func (r *mockEncodingRepository) DeleteJob(ctx context.Context, id string) error {
	delete(r.jobs, id)
	return nil
}

func (r *mockEncodingRepository) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	counts := make(map[string]int64)
	for _, job := range r.jobs {
		counts[string(job.Status)]++
	}
	return counts, nil
}

type mockVideoRepository struct {
	videos map[string]*domain.Video
}

func NewMockVideoRepository() *mockVideoRepository {
	return &mockVideoRepository{
		videos: make(map[string]*domain.Video),
	}
}

func (r *mockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	r.videos[video.ID] = video
	return nil
}

func (r *mockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	video, exists := r.videos[id]
	if !exists {
		return nil, fmt.Errorf("VIDEO_NOT_FOUND")
	}
	return video, nil
}

func (r *mockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	r.videos[video.ID] = video
	return nil
}

func (r *mockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	delete(r.videos, id)
	return nil
}

func (r *mockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	if video, exists := r.videos[videoID]; exists {
		video.Status = status
		video.OutputPaths = outputPaths
		video.ThumbnailPath = thumbnailPath
		video.PreviewPath = previewPath
		video.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	if video, exists := r.videos[videoID]; exists {
		video.Status = status
		video.OutputPaths = outputPaths
		video.ThumbnailPath = thumbnailPath
		video.PreviewPath = previewPath
		video.ProcessedCIDs = processedCIDs
		video.ThumbnailCID = thumbnailCID
		video.UpdatedAt = time.Now()
	}
	return nil
}
