package media

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVP9WebMArgs(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	args := vp9WebMArgs(localSource("/in.mp4"), "/out.webm", r)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-c:v libvpx-vp9") {
		t.Errorf("expected VP9 video codec: %q", joined)
	}
	if !strings.Contains(joined, "-c:a libopus") {
		t.Errorf("expected Opus audio codec: %q", joined)
	}
	if !strings.Contains(joined, "scale=1280:720") {
		t.Errorf("expected scale to rung dims: %q", joined)
	}
	if !strings.Contains(joined, "-b:v 2800k") {
		t.Errorf("expected rung video bitrate: %q", joined)
	}
	if !strings.Contains(joined, "-f webm") {
		t.Errorf("expected webm container: %q", joined)
	}
	if args[len(args)-1] != "/out.webm" {
		t.Errorf("args should end with output path, got %q", args[len(args)-1])
	}
	// Audio map is optional so silent sources still encode.
	if !strings.Contains(joined, "0:a:0?") {
		t.Errorf("expected optional audio map: %q", joined)
	}
}

func TestVP9WebMKey(t *testing.T) {
	id := uuid.New()
	want := "streaming-playlists/" + id.String() + "/vp9.webm"
	if got := VP9WebMKey(id); got != want {
		t.Errorf("VP9WebMKey = %q, want %q", got, want)
	}
}
