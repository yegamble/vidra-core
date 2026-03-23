package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"athena/internal/domain"
	"athena/internal/security"
)

type YtDlp struct {
	binaryPath string
	outputDir  string
}

func NewYtDlp(binaryPath, outputDir string) *YtDlp {
	if binaryPath == "" {
		binaryPath = "yt-dlp"
	}
	return &YtDlp{
		binaryPath: binaryPath,
		outputDir:  outputDir,
	}
}

type ProgressCallback func(progress int, downloadedBytes int64, totalBytes int64)

var (
	progressPercentRegex = regexp.MustCompile(`(\d+\.?\d*)%`)
	progressSizeRegex    = regexp.MustCompile(`([\d.]+)([KMG]iB) of ([\d.]+)([KMG]iB)`)
)

// validateImportURL checks that the URL uses http/https and doesn't target private IPs.
func validateImportURL(rawURL string) error {
	return security.IsSSRFSafeURL(rawURL)
}

func (y *YtDlp) ValidateURL(ctx context.Context, url string) error {
	if err := validateImportURL(url); err != nil {
		return fmt.Errorf("video URL validation failed: %w", err)
	}

	cmd := exec.CommandContext(ctx, y.binaryPath, "--get-title", "--no-warnings", "--", url)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("yt-dlp validation failed", "error", err, "output", string(output))
		return fmt.Errorf("video URL validation failed: unable to access or parse the video")
	}

	if len(output) == 0 {
		return domain.ErrImportUnsupportedURL
	}

	return nil
}

func (y *YtDlp) ExtractMetadata(ctx context.Context, url string) (*domain.ImportMetadata, error) {
	if err := validateImportURL(url); err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	args := []string{
		"--dump-json",
		"--no-warnings",
		"--no-playlist",
		"--",
		url,
	}

	cmd := exec.CommandContext(ctx, y.binaryPath, args...)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	var rawMeta map[string]interface{}
	if err := json.Unmarshal(output, &rawMeta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

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

	if tagsRaw, ok := rawMeta["tags"].([]interface{}); ok {
		tags := make([]string, 0, len(tagsRaw))
		for _, tag := range tagsRaw {
			if tagStr, ok := tag.(string); ok {
				tags = append(tags, tagStr)
			}
		}
		metadata.Tags = tags
	}

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

func (y *YtDlp) Download(ctx context.Context, url string, importID string, progressCallback func(progress int, downloadedBytes, totalBytes int64)) (string, error) {
	if err := validateImportURL(url); err != nil {
		return "", fmt.Errorf("video download failed: %w", err)
	}

	const maxFileSize = int64(5 * 1024 * 1024 * 1024)

	outputPath := filepath.Join(y.outputDir, importID)
	if err := os.MkdirAll(outputPath, 0750); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	outputTemplate := filepath.Join(outputPath, "video.%(ext)s")

	args := []string{
		"--format", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"--no-playlist",
		"--no-warnings",
		"--newline",
		"--max-filesize", strconv.FormatInt(maxFileSize, 10),
		"--output", outputTemplate,
		"--",
		url,
	}

	cmd := exec.CommandContext(ctx, y.binaryPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start yt-dlp: %w", err)
	}

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

	var stderrBuf strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			stderrBuf.WriteString(scanner.Text())
			stderrBuf.WriteString("\n")
		}
	}()

	<-progressDone
	if err := cmd.Wait(); err != nil {
		slog.Error("yt-dlp download failed", "error", err, "stderr", stderrBuf.String())
		return "", fmt.Errorf("video download failed: unable to complete the download")
	}

	videoPath, err := findDownloadedFile(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to find downloaded file: %w", err)
	}

	return videoPath, nil
}

func parseProgress(line string, callback ProgressCallback) {
	if !strings.Contains(line, "[download]") {
		return
	}

	matches := progressPercentRegex.FindStringSubmatch(line)
	if len(matches) < 2 {
		return
	}

	percentFloat, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return
	}

	progress := int(percentFloat)

	var downloadedBytes, totalBytes int64

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

func findDownloadedFile(outputPath string) (string, error) {
	entries, err := os.ReadDir(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to read output directory: %w", err)
	}

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

func (y *YtDlp) DownloadThumbnail(ctx context.Context, url, outputPath string) error {
	if err := validateImportURL(url); err != nil {
		return fmt.Errorf("failed to download thumbnail: %w", err)
	}

	args := []string{
		"--write-thumbnail",
		"--skip-download",
		"--convert-thumbnails", "jpg",
		"--output", outputPath,
		"--",
		url,
	}

	cmd := exec.CommandContext(ctx, y.binaryPath, args...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download thumbnail: %w", err)
	}

	return nil
}

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
