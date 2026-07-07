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

// TestHLSTranscoderHDLadderMultipleRenditions is the real-ffmpeg regression
// guard for the reported "can't switch resolutions" bug: a genuine HD (1280x720)
// source must produce MULTIPLE distinct renditions (720p/480p/360p) with a
// multivariant master playlist — the shape the player's quality selector needs.
// TestHLSTranscoderRealVideo above only exercises a 240p single-rung source, so
// it can pass while multi-resolution laddering is broken; this closes that gap
// against the real ffmpeg exec path.
func TestHLSTranscoderHDLadderMultipleRenditions(t *testing.T) {
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
	// A real 720p clip with audio, so the planned ladder is 720p/480p/360p.
	gen := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=1280x720:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
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

	// Multiple renditions, tallest-first, at the expected ladder heights.
	if len(res.Renditions) != 3 {
		t.Fatalf("renditions = %+v, want 3 (720p/480p/360p ladder)", res.Renditions)
	}
	wantH := []int{720, 480, 360}
	wantW := []int{1280, 854, 640}
	for i, r := range res.Renditions {
		if r.Height != wantH[i] || r.Width != wantW[i] {
			t.Errorf("rendition[%d] = %dx%d, want %dx%d", i, r.Width, r.Height, wantW[i], wantH[i])
		}
	}

	rc, err := blobs.Open(context.Background(), res.MasterKey)
	if err != nil {
		t.Fatalf("open master: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read master: %v", err)
	}
	master := string(b)
	if n := strings.Count(master, "#EXT-X-STREAM-INF"); n != 3 {
		t.Fatalf("master has %d variant streams, want 3:\n%s", n, master)
	}
	for _, want := range []string{"RESOLUTION=1280x720", "RESOLUTION=854x480", "RESOLUTION=640x360"} {
		if !strings.Contains(master, want) {
			t.Errorf("master missing %q:\n%s", want, master)
		}
	}
	// Every variant playlist + its first segment landed in storage.
	for _, r := range res.Renditions {
		variant, err := blobs.Open(context.Background(), r.KeyPrefix+"/playlist.m3u8")
		if err != nil {
			t.Fatalf("open variant %dp: %v", r.Height, err)
		}
		vb, _ := io.ReadAll(variant)
		_ = variant.Close()
		if !strings.Contains(string(vb), "#EXT-X-ENDLIST") {
			t.Errorf("%dp variant playlist malformed:\n%s", r.Height, string(vb))
		}
		if exists, err := blobs.Exists(context.Background(), r.KeyPrefix+"/seg_00000.ts"); err != nil || !exists {
			t.Errorf("%dp first segment not stored (exists=%v err=%v)", r.Height, exists, err)
		}
	}
}
