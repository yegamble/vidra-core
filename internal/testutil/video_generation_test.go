package testutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTestVideo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping video generation test in short mode")
	}

	// Check if ffmpeg is available
	if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); os.IsNotExist(err) {
		t.Skip("FFmpeg not available, skipping video generation test")
	}

	tempDir := t.TempDir()

	spec := TestVideoSpec{
		Name:       "360p",
		Width:      640,
		Height:     360,
		Resolution: "360p",
	}

	videoPath, err := GenerateTestVideo(spec, tempDir)
	require.NoError(t, err, "Failed to generate test video")

	// Verify the file exists
	assert.FileExists(t, videoPath, "Generated video file should exist")

	// Verify the file is not empty
	stat, err := os.Stat(videoPath)
	require.NoError(t, err)
	assert.Greater(t, stat.Size(), int64(1000), "Video file should be larger than 1KB")

	// Verify video metadata
	metadata, err := GetVideoMetadata(videoPath)
	require.NoError(t, err, "Should be able to read metadata")

	assert.Equal(t, spec.Width, metadata.Width, "Video width should match")
	assert.Equal(t, spec.Height, metadata.Height, "Video height should match")
	assert.Equal(t, "h264", metadata.VideoCodec, "Video codec should be H.264")
}

func TestEnsureTestVideoExists(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping video generation test in short mode")
	}

	// Check if ffmpeg is available
	if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); os.IsNotExist(err) {
		t.Skip("FFmpeg not available, skipping video generation test")
	}

	spec := TestVideoSpec{
		Name:       "720p",
		Width:      1280,
		Height:     720,
		Resolution: "720p",
	}

	// First call should generate the video
	videoPath1, err := EnsureTestVideoExists(spec)
	require.NoError(t, err, "Failed to generate test video")

	// Verify the file exists
	assert.FileExists(t, videoPath1, "Generated video file should exist")

	// Second call should reuse the existing video
	videoPath2, err := EnsureTestVideoExists(spec)
	require.NoError(t, err, "Failed to get existing test video")

	assert.Equal(t, videoPath1, videoPath2, "Should return same path for existing video")

	// Cleanup
	os.Remove(videoPath1)
}

func TestGenerateMultipleResolutions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping video generation test in short mode")
	}

	// Check if ffmpeg is available
	if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); os.IsNotExist(err) {
		t.Skip("FFmpeg not available, skipping video generation test")
	}

	tempDir := t.TempDir()

	resolutions := []TestVideoSpec{
		{Name: "240p", Width: 426, Height: 240, Resolution: "240p"},
		{Name: "480p", Width: 854, Height: 480, Resolution: "480p"},
		{Name: "1080p", Width: 1920, Height: 1080, Resolution: "1080p"},
	}

	for _, spec := range resolutions {
		t.Run(spec.Resolution, func(t *testing.T) {
			videoPath, err := GenerateTestVideo(spec, tempDir)
			require.NoError(t, err, "Failed to generate %s video", spec.Resolution)

			assert.FileExists(t, videoPath, "Video file should exist")

			// Verify metadata
			metadata, err := GetVideoMetadata(videoPath)
			require.NoError(t, err, "Should be able to read metadata")

			assert.Equal(t, spec.Width, metadata.Width, "Width mismatch for %s", spec.Resolution)
			assert.Equal(t, spec.Height, metadata.Height, "Height mismatch for %s", spec.Resolution)

			// Verify file size is reasonable (should be small for 5-second video)
			stat, err := os.Stat(videoPath)
			require.NoError(t, err)
			assert.Less(t, stat.Size(), int64(10*1024*1024), "Video should be less than 10MB")
		})
	}
}
