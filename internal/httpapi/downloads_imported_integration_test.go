//go:build integration

package httpapi

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/media"
)

// TestImportedMuxWithRealFFmpeg drives the handler with the REAL muxer: good
// split-audio objects stream a 200 MP4, and a rung object that is not ISOBMFF
// (here: playlist-looking text named .mp4) is a clean 502 with no attachment.
func TestImportedMuxWithRealFFmpeg(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), nil)
	srv.hlsMuxer = media.NewHLSMuxer(blobs)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Imp","privacy":"public"}`)
	seedImportedHLS(t, tcRepo, blobs, id, true)

	dir := t.TempDir()
	gen := func(out string, args ...string) []byte {
		t.Helper()
		path := filepath.Join(dir, out)
		full := append([]string{"-v", "error"}, args...)
		full = append(full, "-movflags", "+frag_keyframe+empty_moov+default_base_moof", path)
		if b, err := exec.Command("ffmpeg", full...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, b)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	video := gen("v.mp4", "-f", "lavfi", "-i", "testsrc2=duration=2:size=160x90:rate=24", "-an", "-c:v", "libx264", "-g", "24")
	audio := gen("a.mp4", "-f", "lavfi", "-i", "sine=duration=2", "-vn", "-c:a", "aac")
	put := func(name string, b []byte) {
		if _, err := blobs.Put(context.Background(), importedDir+"/"+name, strings.NewReader(string(b))); err != nil {
			t.Fatal(err)
		}
	}
	put("u-1280.mp4", video)
	put("u-0.mp4", audio)

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "video/mp4" || !strings.Contains(rec.Body.String()[:64], "ftyp") {
		t.Fatalf("real mux = %d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}

	put("u-1280.mp4", []byte("<?xml version=\"1.0\"?>\n#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://example.test/x.m3u8\n"))
	rec = sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/1280", "", "")
	if rec.Code != http.StatusBadGateway || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("non-ISOBMFF rung = %d disposition=%q, want 502 without attachment; body=%s", rec.Code, rec.Header().Get("Content-Disposition"), rec.Body.String())
	}
}
