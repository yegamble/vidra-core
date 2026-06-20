//go:build integration

// These tests exercise the real ffprobe binary and so are excluded from the
// default `make ci` gate (which must stay green on hosts without ffmpeg). Run
// them with: go test -tags integration ./internal/media/
package media

import (
	"context"
	"os/exec"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestFFProbeRealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	blobs, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	// Generate a 1-second 320x240 test video directly at the object's path.
	path, err := blobs.Path("videos/v1/original.mp4")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=24", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	md, err := NewFFProbe(blobs).Probe(context.Background(), "videos/v1/original.mp4")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if md.Width != 320 || md.Height != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", md.Width, md.Height)
	}
	if md.DurationSeconds != 1 {
		t.Errorf("duration = %d, want 1", md.DurationSeconds)
	}
}
