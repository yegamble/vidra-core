//go:build integration

// Excluded from `make ci`; run with: go test -tags integration ./internal/media/
package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestThumbnailerRealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	blobs, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	path, err := blobs.Path("web-videos/v1.mp4")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	// ffmpeg won't create missing parent dirs; in production the upload writes
	// original.mp4 (creating videos/v1/) before any thumbnailing runs.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=24", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	jpg, err := NewThumbnailer(blobs).Thumbnail(context.Background(), "web-videos/v1.mp4", 2)
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	if len(jpg) == 0 {
		t.Fatal("empty thumbnail bytes")
	}
	// JPEG SOI marker.
	if len(jpg) < 2 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Errorf("output is not a JPEG (first bytes %x %x)", jpg[0], jpg[1])
	}
}
