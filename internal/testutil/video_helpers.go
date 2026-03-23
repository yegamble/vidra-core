package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vidra-core/internal/domain"
)

type VideoMetadata struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Duration    string `json:"duration"`
	Bitrate     string `json:"bit_rate"`
	FrameRate   string `json:"r_frame_rate"`
	CodecName   string `json:"codec_name"`
	PixelFormat string `json:"pix_fmt"`
}

type FFProbeOutput struct {
	Streams []VideoMetadata `json:"streams"`
}

// GetVideoMetadata uses ffprobe to extract video metadata
func GetVideoMetadata(videoPath string) (*domain.VideoMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-select_streams", "v:0",
		videoPath,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var probe FFProbeOutput
	if err := json.Unmarshal(output, &probe); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	if len(probe.Streams) == 0 {
		return nil, fmt.Errorf("no video streams found")
	}

	stream := probe.Streams[0]

	// Parse frame rate (format: "30/1" or "30000/1001")
	frameRate := parseFrameRate(stream.FrameRate)

	// Parse bitrate
	bitrate := 0
	if stream.Bitrate != "" {
		if parsed, err := strconv.Atoi(stream.Bitrate); err == nil {
			bitrate = parsed
		}
	}

	return &domain.VideoMetadata{
		Width:       stream.Width,
		Height:      stream.Height,
		Framerate:   frameRate,
		Bitrate:     bitrate,
		VideoCodec:  stream.CodecName,
		AspectRatio: fmt.Sprintf("%d:%d", stream.Width, stream.Height),
	}, nil
}

func parseFrameRate(rateStr string) float64 {
	if rateStr == "" {
		return 0
	}

	parts := strings.Split(rateStr, "/")
	if len(parts) != 2 {
		return 0
	}

	numerator, err1 := strconv.ParseFloat(parts[0], 64)
	denominator, err2 := strconv.ParseFloat(parts[1], 64)

	if err1 != nil || err2 != nil || denominator == 0 {
		return 0
	}

	return numerator / denominator
}

// TestVideoSpecs defines expected properties for test videos
type TestVideoSpec struct {
	Name         string
	Width        int
	Height       int
	Resolution   string
	ExpectedVars []string // Expected encoding variants
}

var TestVideos = []TestVideoSpec{
	{
		Name:         "8K",
		Width:        7680,
		Height:       4320,
		Resolution:   "4320p",
		ExpectedVars: []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"},
	},
	{
		Name:         "4K",
		Width:        3840,
		Height:       2160,
		Resolution:   "2160p",
		ExpectedVars: []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p"},
	},
	{
		Name:         "1440p",
		Width:        2560,
		Height:       1440,
		Resolution:   "1440p",
		ExpectedVars: []string{"240p", "360p", "480p", "720p", "1080p", "1440p"},
	},
	{
		Name:         "1080p",
		Width:        1920,
		Height:       1080,
		Resolution:   "1080p",
		ExpectedVars: []string{"240p", "360p", "480p", "720p", "1080p"},
	},
	{
		Name:         "720p",
		Width:        1280,
		Height:       720,
		Resolution:   "720p",
		ExpectedVars: []string{"240p", "360p", "480p", "720p"},
	},
	{
		Name:         "480p",
		Width:        854,
		Height:       480,
		Resolution:   "480p",
		ExpectedVars: []string{"240p", "360p", "480p"},
	},
	{
		Name:         "360p",
		Width:        640,
		Height:       360,
		Resolution:   "360p",
		ExpectedVars: []string{"240p", "360p"},
	},
}

// GenerateTestVideo creates a short test video with the specified resolution
// Returns the path to the generated video file
func GenerateTestVideo(spec TestVideoSpec, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	filename := fmt.Sprintf("test_%s_%dp.mp4", strings.ToLower(spec.Name), spec.Height)
	outputPath := filepath.Join(outputDir, filename)

	// Check if file already exists
	if _, err := os.Stat(outputPath); err == nil {
		return outputPath, nil
	}

	// Generate a short (5 second) test video with colorful pattern
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=30:duration=5", spec.Width, spec.Height),
		"-f", "lavfi",
		"-i", "sine=frequency=1000:duration=5",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-shortest",
		"-y", // Overwrite output file
		outputPath,
	}

	// #nosec G204 - arguments are constructed from controlled test input
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg failed to generate test video: %w", err)
	}

	return outputPath, nil
}

// EnsureTestVideoExists generates a test video if it doesn't exist and returns its path
func EnsureTestVideoExists(spec TestVideoSpec) (string, error) {
	tempDir := filepath.Join(os.TempDir(), "vidra_test_videos")
	return GenerateTestVideo(spec, tempDir)
}
