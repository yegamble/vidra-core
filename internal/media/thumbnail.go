package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vidra-core/internal/domain"
)

const (
	defaultThumbnailSeek = 1 * time.Second
	longThumbnailWindow  = 5 * time.Second
	midThumbnailWindow   = 3 * time.Second
	previewWindow        = 3 * time.Second
)

// ProbeDuration returns the media duration using ffprobe. Callers can fall back
// to a default strategy when probing fails.
func ProbeDuration(ctx context.Context, ffmpegPath string, input string) (time.Duration, error) {
	bin := ffprobeBinary(ffmpegPath)

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

	return parseProbeDuration(result.Format.Duration)
}

func ProbeVideoMetadata(ctx context.Context, ffmpegPath string, input string) (*domain.VideoMetadata, time.Duration, error) {
	bin := ffprobeBinary(ffmpegPath)

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration:stream=codec_type,width,height,bit_rate,r_frame_rate,codec_name",
		"-of", "json",
		input,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			BitRate   string `json:"bit_rate"`
			FrameRate string `json:"r_frame_rate"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return nil, 0, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	duration, err := parseProbeDuration(result.Format.Duration)
	if err != nil {
		return nil, 0, err
	}

	for _, stream := range result.Streams {
		if stream.CodecType != "video" {
			continue
		}

		bitrate := 0
		if stream.BitRate != "" {
			if parsed, parseErr := strconv.Atoi(stream.BitRate); parseErr == nil {
				bitrate = parsed
			}
		}

		metadata := &domain.VideoMetadata{
			Width:      stream.Width,
			Height:     stream.Height,
			Framerate:  parseProbeFrameRate(stream.FrameRate),
			Bitrate:    bitrate,
			VideoCodec: stream.CodecName,
		}
		if stream.Width > 0 && stream.Height > 0 {
			metadata.AspectRatio = fmt.Sprintf("%d:%d", stream.Width, stream.Height)
		}

		return metadata, duration, nil
	}

	return nil, 0, fmt.Errorf("no video stream found")
}

func ffprobeBinary(ffmpegPath string) string {
	if ffmpegPath != "" {
		return filepath.Join(filepath.Dir(ffmpegPath), "ffprobe")
	}
	return "ffprobe"
}

func parseProbeDuration(raw string) (time.Duration, error) {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration: %w", err)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

func parseProbeFrameRate(rateStr string) float64 {
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
			"-update", "1",
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
		"-update", "1",
		"-q:v", "2",
		output,
	}
}

func BuildRepresentativePreviewArgs(input string, output string, duration time.Duration) []string {
	if duration <= 0 {
		return []string{
			"-y",
			"-ss", formatFFmpegTimestamp(defaultThumbnailSeek),
			"-t", formatFFmpegTimestamp(previewWindow),
			"-i", input,
			"-vf", "fps=10,scale=320:-2",
			"-loop", "0",
			"-an",
			"-vsync", "0",
			"-c:v", "libwebp",
			"-quality", "80",
			output,
		}
	}

	window := previewDuration(duration)
	start := representativeThumbnailStart(duration, window)

	return []string{
		"-y",
		"-ss", formatFFmpegTimestamp(start),
		"-t", formatFFmpegTimestamp(window),
		"-i", input,
		"-vf", "fps=10,scale=320:-2",
		"-loop", "0",
		"-an",
		"-vsync", "0",
		"-c:v", "libwebp",
		"-quality", "80",
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

func previewDuration(duration time.Duration) time.Duration {
	switch {
	case duration <= 0:
		return previewWindow
	case duration < previewWindow:
		return duration
	default:
		return previewWindow
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
