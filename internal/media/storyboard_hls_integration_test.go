//go:build integration

package media

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestStoryboardFromSingleFileHLS(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=4:size=320x180:rate=24", "-c:v", "libx264", "-g", "24", "-hls_time", "1", "-hls_segment_type", "fmp4", "-hls_flags", "single_file", "-hls_segment_filename", filepath.Join(dir, "video.mp4"), filepath.Join(dir, "low.m3u8"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate HLS fixture: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(storyboardTestMaster), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()
	for name, backend := range map[string]storage.Backend{"local": b, "presigned": &presignBackend{Backend: b, url: srv.URL + "/video.mp4"}} {
		t.Run(name, func(t *testing.T) {
			// No original object and no duration hint: probe the resolved video MP4.
			jpg, vtt, err := NewStoryboarder(backend).Storyboard(context.Background(), "master.m3u8", 0)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
			if err != nil || cfg.Width != 640 || cfg.Height != 90 || strings.Count(string(vtt), "#xywh=") != 4 {
				t.Fatalf("invalid storyboard: image=%+v err=%v vtt=%s", cfg, err, vtt)
			}
		})
	}
}
