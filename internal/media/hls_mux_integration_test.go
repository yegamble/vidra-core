//go:build integration

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// urlBackend presigns every key to the matching file on an httptest server, so
// the muxer's remote (presigned URL) input path runs against real ffmpeg.
type urlBackend struct {
	storage.Backend
	base string
}

func (u urlBackend) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return u.base + "/" + key, nil
}

// urlBackend embeds the Backend INTERFACE, so the local backend's PathProvider is
// not promoted and the muxer takes the presign branch.

func TestHLSMuxerRealFFmpeg(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
		}
	}
	// PeerTube split-audio shape: a video-only and an audio-only fragmented MP4.
	run("-f", "lavfi", "-i", "testsrc2=duration=2:size=160x90:rate=24", "-an", "-c:v", "libx264", "-g", "24",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof", filepath.Join(dir, "v.mp4"))
	run("-f", "lavfi", "-i", "sine=duration=2", "-vn", "-c:a", "aac",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof", filepath.Join(dir, "a.mp4"))
	local, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	for name, backend := range map[string]storage.Backend{
		"local":     local,
		"presigned": urlBackend{Backend: local, base: srv.URL},
	} {
		t.Run(name, func(t *testing.T) {
			rc, err := NewHLSMuxer(backend).Mux(context.Background(), "v.mp4", "a.mp4")
			if err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil || len(out) == 0 {
				t.Fatalf("mux output %d bytes, err=%v", len(out), err)
			}
			probe := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "json", "-")
			probe.Stdin = bytes.NewReader(out)
			pb, err := probe.Output()
			if err != nil {
				t.Fatalf("ffprobe: %v", err)
			}
			var parsed struct {
				Streams []struct {
					CodecType string `json:"codec_type"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(pb, &parsed); err != nil {
				t.Fatal(err)
			}
			var video, audio int
			for _, s := range parsed.Streams {
				switch s.CodecType {
				case "video":
					video++
				case "audio":
					audio++
				}
			}
			if video != 1 || audio != 1 || len(parsed.Streams) != 2 {
				t.Fatalf("streams = %+v, want one video and one audio", parsed.Streams)
			}
		})
	}

	t.Run("failure surfaces as a Read error with no presigned URL", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "bad.mp4"), []byte("<?xml version=\"1.0\"?>\n#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://example.test/x.m3u8\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		rc, err := NewHLSMuxer(urlBackend{Backend: local, base: srv.URL}).Mux(context.Background(), "bad.mp4", "a.mp4")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rc.Close() }()
		out, err := io.ReadAll(rc)
		if err == nil || len(out) != 0 {
			t.Fatalf("want an error and no bytes, got %d bytes err=%v", len(out), err)
		}
		if strings.Contains(err.Error(), srv.URL) {
			t.Errorf("error leaks the presigned URL: %v", err)
		}
	})

	t.Run("cancel kills ffmpeg", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		rc, err := NewHLSMuxer(local).Mux(ctx, "v.mp4", "a.mp4")
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		done := make(chan struct{})
		go func() { _ = rc.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Close did not return after cancel")
		}
	})
}
