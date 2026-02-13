package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"athena/internal/domain"
)

// YtDlp wraps yt-dlp command-line tool for video downloading
type YtDlp struct {
	binaryPath string
	outputDir  string
}

// NewYtDlp creates a new YtDlp wrapper
func NewYtDlp(binaryPath, outputDir string) *YtDlp {
	if binaryPath == "" {
		binaryPath = "yt-dlp" // Use system PATH
	}
	return &YtDlp{
		binaryPath: binaryPath,
		outputDir:  outputDir,
	}
}

// ProgressCallback is called during download to report progress
type ProgressCallback func(progress int, downloadedBytes int64, totalBytes int64)

var (
	progressPercentRegex = regexp.MustCompile(`(\d+\.?\d*)%`)
	progressSizeRegex    = regexp.MustCompile(`([\d.]+)([KMG]iB) of ([\d.]+)([KMG]iB)`)
)

// ValidateURL checks if yt-dlp can handle the URL (dry run)
func (y *YtDlp) ValidateURL(ctx context.Context, url string) error {
	// Use --get-title to validate without downloading
	// #nosec G204 - URL is validated and sanitized by domain layer
	cmd := exec.CommandContext(ctx, y.binaryPath, "--get-title", "--no-warnings", "--", url)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// SECURITY FIX: Log detailed error server-side, return generic error to client
		fmt.Fprintf(os.Stderr, "ERROR: yt-dlp validation failed: %v, output: %s\n", err, string(output))
		return fmt.Errorf("video URL validation failed: unable to access or parse the video")
	}

	if len(output) == 0 {
		return domain.ErrImportUnsupportedURL
	}

	return nil
}

// ExtractMetadata extracts metadata from a URL without downloading
func (y *YtDlp) ExtractMetadata(ctx context.Context, url string) (*domain.ImportMetadata, error) {
	args := []string{
		"--dump-json",
		"--no-warnings",
		"--no-playlist", // Only get the single video, not playlist
		"--",            // Stop option parsing before user-controlled URL
		url,
	}

	// #nosec G204 - URL is validated and sanitized
	cmd := exec.CommandContext(ctx, y.binaryPath, args...)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	var rawMeta map[string]interface{}
	if err := json.Unmarshal(output, &rawMeta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	// Map yt-dlp output to our ImportMetadata structure
	metadata := &domain.ImportMetadata{
		Title:          getString(rawMeta, "title"),
		Description:    getString(rawMeta, "description"),
		Duration:       getInt(rawMeta, "duration"),
		Uploader:       getString(rawMeta, "uploader"),
		UploaderURL:    getString(rawMeta, "uploader_url"),
		ThumbnailURL:   getString(rawMeta, "thumbnail"),
		ViewCount:      getInt64(rawMeta, "view_count"),
		LikeCount:      getInt64(rawMeta, "like_count"),
		UploadDate:     getString(rawMeta, "upload_date"),
		ExtractorKey:   getString(rawMeta, "extractor_key"),
		Format:         getString(rawMeta, "format"),
		FormatID:       getString(rawMeta, "format_id"),
		Width:          getInt(rawMeta, "width"),
		Height:         getInt(rawMeta, "height"),
		FPS:            getFloat64(rawMeta, "fps"),
		VideoCodec:     getString(rawMeta, "vcodec"),
		AudioCodec:     getString(rawMeta, "acodec"),
		Filesize:       getInt64(rawMeta, "filesize"),
		FilesizeApprox: getInt64(rawMeta, "filesize_approx"),
	}

	// Extract tags
	if tagsRaw, ok := rawMeta["tags"].([]interface{}); ok {
		tags := make([]string, 0, len(tagsRaw))
		for _, tag := range tagsRaw {
			if tagStr, ok := tag.(string); ok {
				tags = append(tags, tagStr)
			}
		}
		metadata.Tags = tags
	}

	// Extract categories
	if categoriesRaw, ok := rawMeta["categories"].([]interface{}); ok {
		categories := make([]string, 0, len(categoriesRaw))
		for _, cat := range categoriesRaw {
			if catStr, ok := cat.(string); ok {
				categories = append(categories, catStr)
			}
		}
		metadata.Categories = categories
	}

	return metadata, nil
}

// Download downloads a video from the given URL with size protection
func (y *YtDlp) Download(ctx context.Context, url, importID string, progressCallback ProgressCallback) (string, error) {
	// SECURITY FIX: Add file size protection to prevent DoS attacks
	// Limit downloads to 5GB to prevent memory exhaustion from malicious large files
	const maxFileSize = int64(5 * 1024 * 1024 * 1024) // 5GB

	// Create output directory
	outputPath := filepath.Join(y.outputDir, importID)
	if err := os.MkdirAll(outputPath, 0750); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Output template: {importID}/video.%(ext)s
	outputTemplate := filepath.Join(outputPath, "video.%(ext)s")

	// yt-dlp arguments with size limit
	// --max-filesize limits the download size to prevent DoS
	args := []string{
		"--format", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"--no-playlist",
		"--no-warnings",
		"--newline",                                          // Output progress on new lines (easier to parse)
		"--max-filesize", strconv.FormatInt(maxFileSize, 10), // Add size limit
		"--output", outputTemplate,
		"--", // Stop option parsing before user-controlled URL
		url,
	}

	// #nosec G204 - URL is validated and sanitized
	cmd := exec.CommandContext(ctx, y.binaryPath, args...)

	// Capture stdout for progress parsing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Capture stderr for errors
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start yt-dlp: %w", err)
	}

	// Parse progress in goroutine
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if progressCallback != nil {
				parseProgress(line, progressCallback)
			}
		}
	}()

	// Collect stderr
	var stderrBuf strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			stderrBuf.WriteString(scanner.Text())
			stderrBuf.WriteString("\n")
		}
	}()

	// Wait for completion
	<-progressDone
	if err := cmd.Wait(); err != nil {
		// SECURITY FIX: Log detailed error server-side, return generic error to client
		fmt.Fprintf(os.Stderr, "ERROR: yt-dlp download failed: %v, stderr: %s\n", err, stderrBuf.String())
		return "", fmt.Errorf("video download failed: unable to complete the download")
	}

	// Find the downloaded file
	videoPath, err := findDownloadedFile(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to find downloaded file: %w", err)
	}

	return videoPath, nil
}

// parseProgress parses yt-dlp progress output
func parseProgress(line string, callback ProgressCallback) {
	// yt-dlp progress format: [download] 45.2% of 123.45MiB at 1.23MiB/s ETA 00:15
	// or: [download] Downloading video 1 of 1

	if !strings.Contains(line, "[download]") {
		return
	}

	// Extract percentage
	matches := progressPercentRegex.FindStringSubmatch(line)
	if len(matches) < 2 {
		return
	}

	percentFloat, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return
	}

	progress := int(percentFloat)

	// Extract downloaded and total bytes
	var downloadedBytes, totalBytes int64

	// Pattern: "45.2% of 123.45MiB"
	sizeMatches := progressSizeRegex.FindStringSubmatch(line)
	if len(sizeMatches) >= 5 {
		downloaded, _ := strconv.ParseFloat(sizeMatches[1], 64)
		downloadedUnit := sizeMatches[2]
		total, _ := strconv.ParseFloat(sizeMatches[3], 64)
		totalUnit := sizeMatches[4]

		downloadedBytes = int64(downloaded * unitMultiplier(downloadedUnit))
		totalBytes = int64(total * unitMultiplier(totalUnit))
	}

	callback(progress, downloadedBytes, totalBytes)
}

// unitMultiplier returns the byte multiplier for size units
func unitMultiplier(unit string) float64 {
	switch unit {
	case "KiB":
		return 1024
	case "MiB":
		return 1024 * 1024
	case "GiB":
		return 1024 * 1024 * 1024
	default:
		return 1
	}
}

// findDownloadedFile finds the downloaded video file in the output directory
func findDownloadedFile(outputPath string) (string, error) {
	entries, err := os.ReadDir(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to read output directory: %w", err)
	}

	// Look for video.mp4 or video.{ext}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "video.") {
			return filepath.Join(outputPath, name), nil
		}
	}

	return "", fmt.Errorf("no video file found in %s", outputPath)
}

// Helper functions to extract values from JSON map

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case int64:
			return int(v)
		}
	}
	return 0
}

func getInt64(m map[string]interface{}, key string) int64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int:
			return int64(v)
		case int64:
			return v
		}
	}
	return 0
}

func getFloat64(m map[string]interface{}, key string) float64 {
	if val, ok := m[key]; ok {
		if f, ok := val.(float64); ok {
			return f
		}
	}
	return 0
}

// DownloadThumbnail downloads the video thumbnail
func (y *YtDlp) DownloadThumbnail(ctx context.Context, url, outputPath string) error {
	args := []string{
		"--write-thumbnail",
		"--skip-download",
		"--convert-thumbnails", "jpg",
		"--output", outputPath,
		"--", // Stop option parsing before user-controlled URL
		url,
	}

	// #nosec G204 - URL is validated
	cmd := exec.CommandContext(ctx, y.binaryPath, args...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download thumbnail: %w", err)
	}

	return nil
}

// CheckAvailability checks if yt-dlp is installed and accessible
func CheckAvailability(binaryPath string) error {
	if binaryPath == "" {
		binaryPath = "yt-dlp"
	}

	cmd := exec.Command(binaryPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("yt-dlp not found or not executable: %w (ensure yt-dlp is installed)", err)
	}

	version := strings.TrimSpace(string(output))
	if version == "" {
		return fmt.Errorf("yt-dlp version check failed")
	}

	return nil
}
