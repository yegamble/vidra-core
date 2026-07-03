//go:build integration

// This test exercises the real ffmpeg binary (audio extraction) and so is
// excluded from the default `make ci` gate. Run it with:
//
//	go test -tags integration ./internal/media/
package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// TestWhisperExtractAudioRealFFmpeg generates a tiny video WITH an audio track,
// runs the real ffmpeg audio-extraction path, and probes the result to confirm
// it is a 16 kHz mono WAV — the canonical Whisper input.
func TestWhisperExtractAudioRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	blobs, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	key := "web-videos/v1.mp4"
	path, err := blobs.Path(key)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A 1-second test video with a 440 Hz sine audio track.
	gen := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-shortest", path,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	wc := NewWhisperClient(blobs, "http://unused")
	wavPath, cleanup, err := wc.ffmpegExtract(context.Background(), key)
	if err != nil {
		t.Fatalf("ffmpegExtract: %v", err)
	}
	defer cleanup()

	// The extracted WAV must be a real, non-empty file.
	fi, err := os.Stat(wavPath)
	if err != nil {
		t.Fatalf("stat wav: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("extracted wav is empty")
	}

	// Probe the audio stream: 16 kHz, mono.
	probe := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=sample_rate,channels",
		"-of", "default=noprint_wrappers=1",
		wavPath,
	)
	out, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe wav: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "sample_rate=16000") {
		t.Errorf("extracted wav sample rate not 16000:\n%s", got)
	}
	if !strings.Contains(got, "channels=1") {
		t.Errorf("extracted wav is not mono:\n%s", got)
	}
}
