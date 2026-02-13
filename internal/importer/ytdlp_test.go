package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"athena/internal/domain"
)

func writeMockExecutable(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "yt-dlp-mock.sh")
	content := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}

func TestNewYtDlp(t *testing.T) {
	outputDir := t.TempDir()

	defaultBinary := NewYtDlp("", outputDir)
	if defaultBinary.binaryPath != "yt-dlp" {
		t.Fatalf("default binaryPath = %q, want %q", defaultBinary.binaryPath, "yt-dlp")
	}
	if defaultBinary.outputDir != outputDir {
		t.Fatalf("outputDir = %q, want %q", defaultBinary.outputDir, outputDir)
	}

	customBinary := NewYtDlp("/usr/local/bin/custom-yt-dlp", outputDir)
	if customBinary.binaryPath != "/usr/local/bin/custom-yt-dlp" {
		t.Fatalf("custom binaryPath = %q", customBinary.binaryPath)
	}
}

func TestYtDlp_ValidateURL(t *testing.T) {
	outputDir := t.TempDir()

	t.Run("success", func(t *testing.T) {
		binary := writeMockExecutable(t, `
has_delimiter=0
for arg in "$@"; do
  if [ "$arg" = "--" ]; then
    has_delimiter=1
  fi
done
if [ "$has_delimiter" -ne 1 ]; then
  echo "missing -- delimiter" 1>&2
  exit 1
fi
echo "Sample Title"`)
		y := NewYtDlp(binary, outputDir)

		if err := y.ValidateURL(context.Background(), "https://example.com/video"); err != nil {
			t.Fatalf("ValidateURL() error = %v", err)
		}
	})

	t.Run("empty output unsupported", func(t *testing.T) {
		binary := writeMockExecutable(t, `exit 0`)
		y := NewYtDlp(binary, outputDir)

		err := y.ValidateURL(context.Background(), "https://example.com/video")
		if !errors.Is(err, domain.ErrImportUnsupportedURL) {
			t.Fatalf("ValidateURL() error = %v, want %v", err, domain.ErrImportUnsupportedURL)
		}
	})

	t.Run("command failure redacts details", func(t *testing.T) {
		binary := writeMockExecutable(t, `echo "internal parser failure" 1>&2; exit 1`)
		y := NewYtDlp(binary, outputDir)

		err := y.ValidateURL(context.Background(), "https://example.com/video")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "video URL validation failed") {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(err.Error(), "internal parser failure") {
			t.Fatalf("error leaked internal details: %v", err)
		}
	})
}

func TestYtDlp_ExtractMetadata(t *testing.T) {
	outputDir := t.TempDir()

	t.Run("success", func(t *testing.T) {
		binary := writeMockExecutable(t, `
has_delimiter=0
for arg in "$@"; do
  if [ "$arg" = "--" ]; then
    has_delimiter=1
  fi
done
if [ "$has_delimiter" -ne 1 ]; then
  echo "missing -- delimiter" 1>&2
  exit 1
fi
cat <<'EOF'
{
  "title": "Test Video",
  "description": "Description",
  "duration": 42,
  "uploader": "Uploader",
  "uploader_url": "https://example.com/uploader",
  "thumbnail": "https://example.com/thumb.jpg",
  "view_count": 1234,
  "like_count": 55,
  "upload_date": "20260101",
  "extractor_key": "YouTube",
  "format": "1080p",
  "format_id": "137",
  "width": 1920,
  "height": 1080,
  "fps": 30,
  "vcodec": "avc1",
  "acodec": "mp4a",
  "filesize": 1000,
  "filesize_approx": 1100,
  "tags": ["tag1", "tag2"],
  "categories": ["cat1", "cat2"]
}
EOF`)
		y := NewYtDlp(binary, outputDir)

		meta, err := y.ExtractMetadata(context.Background(), "https://example.com/video")
		if err != nil {
			t.Fatalf("ExtractMetadata() error = %v", err)
		}

		if meta.Title != "Test Video" {
			t.Fatalf("meta.Title = %q", meta.Title)
		}
		if meta.Duration != 42 {
			t.Fatalf("meta.Duration = %d", meta.Duration)
		}
		if meta.ViewCount != 1234 {
			t.Fatalf("meta.ViewCount = %d", meta.ViewCount)
		}
		if meta.FPS != 30 {
			t.Fatalf("meta.FPS = %v", meta.FPS)
		}
		if len(meta.Tags) != 2 || meta.Tags[0] != "tag1" {
			t.Fatalf("meta.Tags = %#v", meta.Tags)
		}
		if len(meta.Categories) != 2 || meta.Categories[1] != "cat2" {
			t.Fatalf("meta.Categories = %#v", meta.Categories)
		}
	})

	t.Run("command failure", func(t *testing.T) {
		binary := writeMockExecutable(t, `echo "metadata fetch failed" 1>&2; exit 1`)
		y := NewYtDlp(binary, outputDir)

		_, err := y.ExtractMetadata(context.Background(), "https://example.com/video")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to extract metadata") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		binary := writeMockExecutable(t, `echo "{invalid-json}"`)
		y := NewYtDlp(binary, outputDir)

		_, err := y.ExtractMetadata(context.Background(), "https://example.com/video")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to parse metadata JSON") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestYtDlp_Download(t *testing.T) {
	t.Run("success with progress callback", func(t *testing.T) {
		outputDir := t.TempDir()
		binary := writeMockExecutable(t, `
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output" ]; then
    out="$arg"
  fi
  if [ "$arg" = "https://example.com/video" ] && [ "$prev" != "--" ]; then
    echo "missing -- delimiter before URL" 1>&2
    exit 1
  fi
  prev="$arg"
done
if [ -z "$out" ]; then
  echo "missing output template" 1>&2
  exit 1
fi
out_file=$(echo "$out" | sed 's/%(ext)s/mp4/')
mkdir -p "$(dirname "$out_file")"
echo "video-data" > "$out_file"
echo "[download] 50.0% 10.0MiB of 20.0MiB"
echo "[download] 100.0% 20.0MiB of 20.0MiB"
`)
		y := NewYtDlp(binary, outputDir)

		var callbackCalls int
		var lastProgress int
		var lastDownloaded int64
		var lastTotal int64

		videoPath, err := y.Download(context.Background(), "https://example.com/video", "import-success", func(progress int, downloadedBytes int64, totalBytes int64) {
			callbackCalls++
			lastProgress = progress
			lastDownloaded = downloadedBytes
			lastTotal = totalBytes
		})
		if err != nil {
			t.Fatalf("Download() error = %v", err)
		}

		if callbackCalls == 0 {
			t.Fatal("expected progress callback to be invoked")
		}
		if lastProgress != 100 {
			t.Fatalf("last progress = %d, want 100", lastProgress)
		}
		if lastDownloaded != 20*1024*1024 {
			t.Fatalf("last downloaded = %d", lastDownloaded)
		}
		if lastTotal != 20*1024*1024 {
			t.Fatalf("last total = %d", lastTotal)
		}
		if !strings.HasSuffix(videoPath, filepath.Join("import-success", "video.mp4")) {
			t.Fatalf("unexpected video path: %s", videoPath)
		}
		if _, err := os.Stat(videoPath); err != nil {
			t.Fatalf("downloaded file missing: %v", err)
		}
	})

	t.Run("start failure", func(t *testing.T) {
		y := NewYtDlp(filepath.Join(t.TempDir(), "missing-binary"), t.TempDir())

		_, err := y.Download(context.Background(), "https://example.com/video", "import-start-failure", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to start yt-dlp") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("command failure redacts details", func(t *testing.T) {
		binary := writeMockExecutable(t, `echo "sensitive internal stderr" 1>&2; exit 1`)
		y := NewYtDlp(binary, t.TempDir())

		_, err := y.Download(context.Background(), "https://example.com/video", "import-command-failure", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "video download failed") {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(err.Error(), "sensitive internal stderr") {
			t.Fatalf("error leaked stderr details: %v", err)
		}
	})

	t.Run("no downloaded file", func(t *testing.T) {
		binary := writeMockExecutable(t, `echo "[download] 100.0% 1.0MiB of 1.0MiB"; exit 0`)
		y := NewYtDlp(binary, t.TempDir())

		_, err := y.Download(context.Background(), "https://example.com/video", "import-missing-file", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to find downloaded file") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestParseProgress(t *testing.T) {
	var calls int
	var progress int
	var downloaded int64
	var total int64

	callback := func(p int, d int64, t int64) {
		calls++
		progress = p
		downloaded = d
		total = t
	}

	parseProgress("not a download line", callback)
	if calls != 0 {
		t.Fatalf("callback calls = %d, want 0", calls)
	}

	parseProgress("[download] no percent available", callback)
	if calls != 0 {
		t.Fatalf("callback calls = %d, want 0", calls)
	}

	parseProgress("[download] 12.5% 1.5MiB of 3.0MiB", callback)
	if calls != 1 {
		t.Fatalf("callback calls = %d, want 1", calls)
	}
	if progress != 12 {
		t.Fatalf("progress = %d, want 12", progress)
	}
	if downloaded != int64(1.5*1024*1024) {
		t.Fatalf("downloaded = %d", downloaded)
	}
	if total != int64(3*1024*1024) {
		t.Fatalf("total = %d", total)
	}
}

func TestUnitMultiplier(t *testing.T) {
	tests := []struct {
		unit string
		want float64
	}{
		{unit: "KiB", want: 1024},
		{unit: "MiB", want: 1024 * 1024},
		{unit: "GiB", want: 1024 * 1024 * 1024},
		{unit: "B", want: 1},
	}

	for _, tc := range tests {
		got := unitMultiplier(tc.unit)
		if got != tc.want {
			t.Fatalf("unitMultiplier(%q) = %v, want %v", tc.unit, got, tc.want)
		}
	}
}

func TestFindDownloadedFile(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "video.webm")
		if err := os.WriteFile(target, []byte("data"), 0600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		got, err := findDownloadedFile(dir)
		if err != nil {
			t.Fatalf("findDownloadedFile() error = %v", err)
		}
		if got != target {
			t.Fatalf("findDownloadedFile() = %q, want %q", got, target)
		}
	})

	t.Run("not found", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		_, err := findDownloadedFile(dir)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no video file found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestJSONHelperExtractors(t *testing.T) {
	m := map[string]interface{}{
		"str":   "value",
		"int":   int(7),
		"int64": int64(8),
		"float": float64(9.5),
	}

	if got := getString(m, "str"); got != "value" {
		t.Fatalf("getString() = %q", got)
	}
	if got := getString(m, "missing"); got != "" {
		t.Fatalf("getString(missing) = %q, want empty", got)
	}

	if got := getInt(m, "float"); got != 9 {
		t.Fatalf("getInt(float) = %d, want 9", got)
	}
	if got := getInt(m, "int"); got != 7 {
		t.Fatalf("getInt(int) = %d, want 7", got)
	}
	if got := getInt(m, "int64"); got != 8 {
		t.Fatalf("getInt(int64) = %d, want 8", got)
	}

	if got := getInt64(m, "float"); got != 9 {
		t.Fatalf("getInt64(float) = %d, want 9", got)
	}
	if got := getInt64(m, "int"); got != 7 {
		t.Fatalf("getInt64(int) = %d, want 7", got)
	}
	if got := getInt64(m, "int64"); got != 8 {
		t.Fatalf("getInt64(int64) = %d, want 8", got)
	}

	if got := getFloat64(m, "float"); got != 9.5 {
		t.Fatalf("getFloat64(float) = %v, want 9.5", got)
	}
	if got := getFloat64(m, "str"); got != 0 {
		t.Fatalf("getFloat64(str) = %v, want 0", got)
	}
}

func TestYtDlp_DownloadThumbnail(t *testing.T) {
	outputDir := t.TempDir()

	t.Run("success", func(t *testing.T) {
		binary := writeMockExecutable(t, `
has_delimiter=0
for arg in "$@"; do
  if [ "$arg" = "--" ]; then
    has_delimiter=1
  fi
done
if [ "$has_delimiter" -ne 1 ]; then
  echo "missing -- delimiter" 1>&2
  exit 1
fi
exit 0`)
		y := NewYtDlp(binary, outputDir)

		if err := y.DownloadThumbnail(context.Background(), "https://example.com/video", filepath.Join(outputDir, "thumb")); err != nil {
			t.Fatalf("DownloadThumbnail() error = %v", err)
		}
	})

	t.Run("command failure", func(t *testing.T) {
		binary := writeMockExecutable(t, `echo "thumb failed" 1>&2; exit 1`)
		y := NewYtDlp(binary, outputDir)

		err := y.DownloadThumbnail(context.Background(), "https://example.com/video", filepath.Join(outputDir, "thumb"))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to download thumbnail") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
