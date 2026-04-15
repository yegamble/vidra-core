package media

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBuildRepresentativeThumbnailArgs_UsesMidVideoSampling(t *testing.T) {
	args := BuildRepresentativeThumbnailArgs("/videos/input.mp4", "/videos/output.jpg", 2*time.Minute)

	assert.Equal(t, []string{
		"-y",
		"-ss", "00:00:57.500",
		"-i", "/videos/input.mp4",
		"-t", "00:00:05.000",
		"-vf", "thumbnail=50",
		"-frames:v", "1",
		"-update", "1",
		"-q:v", "2",
		"/videos/output.jpg",
	}, args)
}

func TestBuildRepresentativeThumbnailArgs_UsesWholeClipForShortVideos(t *testing.T) {
	args := BuildRepresentativeThumbnailArgs("/videos/input.mp4", "/videos/output.jpg", 2*time.Second)

	assert.Equal(t, []string{
		"-y",
		"-ss", "00:00:00.000",
		"-i", "/videos/input.mp4",
		"-t", "00:00:02.000",
		"-vf", "thumbnail=12",
		"-frames:v", "1",
		"-update", "1",
		"-q:v", "2",
		"/videos/output.jpg",
	}, args)
}

func TestBuildRepresentativeThumbnailArgs_FallsBackWhenDurationUnknown(t *testing.T) {
	args := BuildRepresentativeThumbnailArgs("/videos/input.mp4", "/videos/output.jpg", 0)

	assert.Equal(t, []string{
		"-y",
		"-ss", "00:00:01.000",
		"-i", "/videos/input.mp4",
		"-frames:v", "1",
		"-update", "1",
		"-q:v", "2",
		"/videos/output.jpg",
	}, args)
}

func TestBuildRepresentativePreviewArgs_UsesMidVideoSampling(t *testing.T) {
	args := BuildRepresentativePreviewArgs("/videos/input.mp4", "/videos/output.webp", 2*time.Minute)

	assert.Equal(t, []string{
		"-y",
		"-ss", "00:00:58.500",
		"-t", "00:00:03.000",
		"-i", "/videos/input.mp4",
		"-vf", "fps=10,scale=320:-2",
		"-loop", "0",
		"-an",
		"-vsync", "0",
		"-c:v", "libwebp",
		"-quality", "80",
		"/videos/output.webp",
	}, args)
}

func TestBuildRepresentativePreviewArgs_FallsBackWhenDurationUnknown(t *testing.T) {
	args := BuildRepresentativePreviewArgs("/videos/input.mp4", "/videos/output.webp", 0)

	assert.Equal(t, []string{
		"-y",
		"-ss", "00:00:01.000",
		"-t", "00:00:03.000",
		"-i", "/videos/input.mp4",
		"-vf", "fps=10,scale=320:-2",
		"-loop", "0",
		"-an",
		"-vsync", "0",
		"-c:v", "libwebp",
		"-quality", "80",
		"/videos/output.webp",
	}, args)
}
