package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	defaultThumbnailSeek = 1 * time.Second
	longThumbnailWindow  = 5 * time.Second
	midThumbnailWindow   = 3 * time.Second
)

// ProbeDuration returns the media duration using ffprobe. Callers can fall back
// to a default strategy when probing fails.
func ProbeDuration(ctx context.Context, ffmpegPath string, input string) (time.Duration, error) {
	bin := "ffprobe"
	if ffmpegPath != "" {
		bin = filepath.Join(filepath.Dir(ffmpegPath), "ffprobe")
	}

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		input,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return 0, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	seconds, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration: %w", err)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

// BuildRepresentativeThumbnailArgs approximates PeerTube's "analyze frames in
// the middle of the video" behavior by sampling a short mid-video window and
// letting ffmpeg's thumbnail filter choose the most representative frame.
func BuildRepresentativeThumbnailArgs(input string, output string, duration time.Duration) []string {
	if duration <= 0 {
		return []string{
			"-y",
			"-ss", formatFFmpegTimestamp(defaultThumbnailSeek),
			"-i", input,
			"-frames:v", "1",
			"-q:v", "2",
			output,
		}
	}

	window := representativeThumbnailWindow(duration)
	start := representativeThumbnailStart(duration, window)
	frames := representativeThumbnailFrames(window)

	return []string{
		"-y",
		"-ss", formatFFmpegTimestamp(start),
		"-i", input,
		"-t", formatFFmpegTimestamp(window),
		"-vf", fmt.Sprintf("thumbnail=%d", frames),
		"-frames:v", "1",
		"-q:v", "2",
		output,
	}
}

func representativeThumbnailWindow(duration time.Duration) time.Duration {
	switch {
	case duration <= 0:
		return 0
	case duration <= midThumbnailWindow:
		return duration
	case duration < 12*time.Second:
		return midThumbnailWindow
	default:
		return longThumbnailWindow
	}
}

func representativeThumbnailStart(duration time.Duration, window time.Duration) time.Duration {
	if duration <= 0 || window <= 0 {
		return defaultThumbnailSeek
	}

	start := (duration - window) / 2
	if start < 0 {
		return 0
	}

	return start
}

func representativeThumbnailFrames(window time.Duration) int {
	switch {
	case window >= longThumbnailWindow:
		return 50
	case window >= midThumbnailWindow:
		return 24
	default:
		return 12
	}
}

func formatFFmpegTimestamp(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}

	totalMillis := duration.Milliseconds()
	hours := totalMillis / (60 * 60 * 1000)
	minutes := (totalMillis % (60 * 60 * 1000)) / (60 * 1000)
	secondsMillis := totalMillis % (60 * 1000)

	return fmt.Sprintf("%02d:%02d:%06.3f", hours, minutes, float64(secondsMillis)/1000)
}
