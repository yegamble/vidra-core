//go:build integration

// Excluded from `make ci`; run with: go test -tags integration ./internal/media/
package media

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// TestHLSTranscoderRealVideo drives the real ffmpeg exec path: a tiny 320x240
// testsrc fixture is transcoded and the master playlist, the single 240p rung
// (cap-at-source), its variant playlist, and its segments must all land in
// storage with relative playlist URIs.
func TestHLSTranscoderRealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	path, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=24", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}

	prefix := HLSKeyPrefix(videoID)
	if res.MasterKey != prefix+"/master.m3u8" {
		t.Errorf("MasterKey = %q, want %q", res.MasterKey, prefix+"/master.m3u8")
	}
	// Cap-at-source: a 240p source plans exactly one 320x240 rung.
	if len(res.Renditions) != 1 {
		t.Fatalf("renditions = %+v, want exactly one (cap-at-source)", res.Renditions)
	}
	r := res.Renditions[0]
	if r.Width != 320 || r.Height != 240 || r.KeyPrefix != prefix+"/240p" {
		t.Errorf("rendition = %+v, want 320x240 at %s/240p", r, prefix)
	}

	read := func(key string) string {
		rc, err := blobs.Open(context.Background(), key)
		if err != nil {
			t.Fatalf("Open %q: %v", key, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %q: %v", key, err)
		}
		return string(b)
	}

	master := read(res.MasterKey)
	if !strings.HasPrefix(master, "#EXTM3U") {
		t.Fatalf("master is not an m3u8:\n%s", master)
	}
	if !strings.Contains(master, "RESOLUTION=320x240") || !strings.Contains(master, "240p/playlist.m3u8") {
		t.Errorf("master missing the 240p variant:\n%s", master)
	}

	variant := read(r.KeyPrefix + "/playlist.m3u8")
	if !strings.HasPrefix(variant, "#EXTM3U") || !strings.Contains(variant, "#EXT-X-ENDLIST") {
		t.Errorf("variant playlist malformed:\n%s", variant)
	}
	// Segment URIs must be relative bare filenames so the API can proxy them.
	var segments []string
	for _, line := range strings.Split(variant, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "/") {
			t.Errorf("segment URI %q must be a bare relative filename", line)
		}
		segments = append(segments, line)
	}
	if len(segments) == 0 {
		t.Fatal("variant playlist lists no segments")
	}
	for _, seg := range segments {
		exists, err := blobs.Exists(context.Background(), r.KeyPrefix+"/"+seg)
		if err != nil || !exists {
			t.Errorf("segment %q not stored under %s (exists=%v err=%v)", seg, r.KeyPrefix, exists, err)
		}
	}
}
