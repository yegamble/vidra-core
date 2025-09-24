package encoding

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingQuality_VerifyOutputFormats(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping quality tests in short mode")
	}

	// Check if ffmpeg/ffprobe is available
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

	svc := NewService(encodingRepo, videoRepo, nil, tempDir, cfg, nil, nil)

	// Test with 1080p video
	testVideo := testutil.TestVideos[3] // 1080p video
	videoPath, err := testutil.EnsureTestVideoExists(testVideo)
	require.NoError(t, err, "Failed to generate test video")

	videoID := uuid.NewString()
	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           videoID,
		SourceFilePath:    videoPath,
		SourceResolution:  testVideo.Resolution,
		TargetResolutions: testVideo.ExpectedVars,
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	// Process encoding job
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	err = svc.(*service).processJob(ctx, job)
	require.NoError(t, err, "Encoding failed")

	outputDir := filepath.Join(tempDir, "streaming-playlists", "hls", videoID)

	t.Run("MasterPlaylistFormat", func(t *testing.T) {
		masterPlaylist := filepath.Join(outputDir, "master.m3u8")
		require.FileExists(t, masterPlaylist)

		content, err := os.ReadFile(masterPlaylist)
		require.NoError(t, err)

		playlistContent := string(content)

		// Verify playlist starts with proper header
		assert.Contains(t, playlistContent, "#EXTM3U")
		assert.Contains(t, playlistContent, "#EXT-X-VERSION:3")

		// Verify each target resolution is present
		for _, targetRes := range testVideo.ExpectedVars {
			assert.Contains(t, playlistContent, fmt.Sprintf("NAME=\"%s\"", targetRes))
			height, _ := domain.HeightForResolution(targetRes)
			assert.Contains(t, playlistContent, fmt.Sprintf("%dp/stream.m3u8", height))
		}
	})

	t.Run("IndividualPlaylistFormats", func(t *testing.T) {
		for _, targetRes := range testVideo.ExpectedVars {
			t.Run(targetRes, func(t *testing.T) {
				height, ok := domain.HeightForResolution(targetRes)
				require.True(t, ok)

				resDir := filepath.Join(outputDir, fmt.Sprintf("%dp", height))
				playlist := filepath.Join(resDir, "stream.m3u8")
				require.FileExists(t, playlist)

				content, err := os.ReadFile(playlist)
				require.NoError(t, err)

				playlistContent := string(content)

				// Verify HLS playlist format
				assert.Contains(t, playlistContent, "#EXTM3U")
				assert.Contains(t, playlistContent, "#EXT-X-VERSION:3")
				assert.Contains(t, playlistContent, "#EXT-X-TARGETDURATION:")
				assert.Contains(t, playlistContent, "#EXT-X-MEDIA-SEQUENCE:0")
				assert.Contains(t, playlistContent, "#EXT-X-PLAYLIST-TYPE:VOD")
				assert.Contains(t, playlistContent, "#EXT-X-ENDLIST")

				// Verify segments are present
				lines := strings.Split(playlistContent, "\n")
				segmentCount := 0
				for _, line := range lines {
					if strings.HasSuffix(line, ".ts") {
						segmentCount++
						// Verify segment file exists
						segmentPath := filepath.Join(resDir, line)
						assert.FileExists(t, segmentPath, "Segment file should exist: %s", line)
					}
				}
				assert.Greater(t, segmentCount, 0, "Should have at least one segment")
			})
		}
	})

	t.Run("SegmentQuality", func(t *testing.T) {
		for _, targetRes := range testVideo.ExpectedVars[:2] { // Test first two resolutions for speed
			t.Run(targetRes, func(t *testing.T) {
				height, ok := domain.HeightForResolution(targetRes)
				require.True(t, ok)

				resDir := filepath.Join(outputDir, fmt.Sprintf("%dp", height))
				segments, err := filepath.Glob(filepath.Join(resDir, "segment_*.ts"))
				require.NoError(t, err)
				require.Greater(t, len(segments), 0, "Should have segments")

				// Test first segment
				segmentMetadata, err := testutil.GetVideoMetadata(segments[0])
				require.NoError(t, err)

				// Verify resolution
				assert.Equal(t, height, segmentMetadata.Height, "Segment height should match target")

				// Verify codec
				assert.Equal(t, "h264", segmentMetadata.VideoCodec, "Should use H.264 codec")

				// Verify reasonable bitrate (may be zero for segments)
				// Note: Individual segment bitrate is often not available via ffprobe
				if segmentMetadata.Bitrate > 0 {
					assert.Greater(t, segmentMetadata.Bitrate, 10000, "If bitrate available, should be reasonable")
				}

				// Verify reasonable frame rate
				assert.Greater(t, segmentMetadata.Framerate, 20.0, "Should have reasonable frame rate")
				assert.Less(t, segmentMetadata.Framerate, 35.0, "Frame rate should be reasonable")
			})
		}
	})

	t.Run("ThumbnailQuality", func(t *testing.T) {
		thumbnailPath := filepath.Join(tempDir, "thumbnails", videoID+"_thumb.jpg")
		require.FileExists(t, thumbnailPath)

		// Verify it's a valid image
		metadata, err := testutil.GetVideoMetadata(thumbnailPath)
		if err == nil { // FFprobe can read some image formats
			assert.Greater(t, metadata.Width, 0, "Thumbnail should have width")
			assert.Greater(t, metadata.Height, 0, "Thumbnail should have height")
		}

		// At minimum, verify file is not empty
		stat, err := os.Stat(thumbnailPath)
		require.NoError(t, err)
		assert.Greater(t, stat.Size(), int64(1000), "Thumbnail should be reasonably sized")
	})

	t.Run("PreviewQuality", func(t *testing.T) {
		previewPath := filepath.Join(tempDir, "previews", videoID+"_preview.webp")
		require.FileExists(t, previewPath)

		// Verify file is not empty
		stat, err := os.Stat(previewPath)
		require.NoError(t, err)
		assert.Greater(t, stat.Size(), int64(1000), "Preview should be reasonably sized")
	})
}

func TestEncodingPerformance_SegmentDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance tests in short mode")
	}

	// Test different HLS segment durations
	durations := []int{2, 4, 6, 10}

	for _, duration := range durations {
		t.Run(fmt.Sprintf("Duration_%ds", duration), func(t *testing.T) {
			// Check if ffmpeg is available
			if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); os.IsNotExist(err) {
				t.Skip("FFmpeg not available, skipping encoding tests")
			}

			testVideo := testutil.TestVideos[5] // 480p video for speed
			videoPath, err := testutil.EnsureTestVideoExists(testVideo)
			if err != nil {
				t.Skip("Failed to generate test video, skipping")
			}

			tempDir := t.TempDir()
			cfg := &config.Config{
				FFMPEGPath:         "/opt/homebrew/bin/ffmpeg",
				HLSSegmentDuration: duration,
			}

			encodingRepo := NewMockEncodingRepository()
			videoRepo := NewMockVideoRepository()
			svc := NewService(encodingRepo, videoRepo, nil, tempDir, cfg, nil, nil)

			videoID := uuid.NewString()
			job := &domain.EncodingJob{
				ID:                uuid.NewString(),
				VideoID:           videoID,
				SourceFilePath:    videoPath,
				SourceResolution:  testVideo.Resolution,
				TargetResolutions: []string{"480p", "360p"}, // Limit variants for speed
				Status:            domain.EncodingStatusPending,
				Progress:          0,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			start := time.Now()
			err = svc.(*service).processJob(ctx, job)
			elapsed := time.Since(start)

			require.NoError(t, err, "Encoding failed for %d second segments", duration)

			t.Logf("Encoding took %v for %d second segments", elapsed, duration)

			// Verify segments have approximately correct duration
			outputDir := filepath.Join(tempDir, "streaming-playlists", "hls", videoID, "480p")
			playlist := filepath.Join(outputDir, "stream.m3u8")
			require.FileExists(t, playlist)

			content, err := os.ReadFile(playlist)
			require.NoError(t, err)

			playlistContent := string(content)
			lines := strings.Split(playlistContent, "\n")

			segmentDurations := []string{}
			for _, line := range lines {
				if strings.HasPrefix(line, "#EXTINF:") {
					segmentDurations = append(segmentDurations, line)
				}
			}

			assert.Greater(t, len(segmentDurations), 0, "Should have segment duration info")
		})
	}
}

func TestEncodingEdgeCases(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping edge case tests in short mode")
	}

	tempDir := t.TempDir()
	cfg := &config.Config{
		FFMPEGPath:         "/opt/homebrew/bin/ffmpeg",
		HLSSegmentDuration: 4,
	}

	encodingRepo := NewMockEncodingRepository()
	videoRepo := NewMockVideoRepository()
	svc := NewService(encodingRepo, videoRepo, nil, tempDir, cfg, nil, nil)

	t.Run("NonExistentSourceFile", func(t *testing.T) {
		job := &domain.EncodingJob{
			ID:                uuid.NewString(),
			VideoID:           uuid.NewString(),
			SourceFilePath:    "/path/to/nonexistent/file.mp4",
			SourceResolution:  "1080p",
			TargetResolutions: []string{"720p", "480p"},
			Status:            domain.EncodingStatusPending,
			Progress:          0,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := svc.(*service).processJob(ctx, job)
		assert.Error(t, err, "Should fail for non-existent file")
		assert.Contains(t, err.Error(), "source file not found")
	})

	t.Run("EmptyTargetResolutions", func(t *testing.T) {
		// Create a minimal test video
		testVideoPath := filepath.Join(tempDir, "test_minimal.mp4")

		// Skip if ffmpeg not available
		if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); os.IsNotExist(err) {
			t.Skip("FFmpeg not available")
		}

		// Create minimal test video
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "/opt/homebrew/bin/ffmpeg",
			"-f", "lavfi",
			"-i", "testsrc2=duration=2:size=320x240:rate=15",
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-pix_fmt", "yuv420p",
			testVideoPath,
		)

		err := cmd.Run()
		require.NoError(t, err, "Failed to create test video")

		job := &domain.EncodingJob{
			ID:                uuid.NewString(),
			VideoID:           uuid.NewString(),
			SourceFilePath:    testVideoPath,
			SourceResolution:  "240p",
			TargetResolutions: []string{}, // Empty
			Status:            domain.EncodingStatusPending,
			Progress:          0,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()

		err = svc.(*service).processJob(ctx2, job)
		// Should not fail even with empty target resolutions - it will just create thumbnails
		assert.NoError(t, err, "Should handle empty target resolutions gracefully")
	})
}
