//go:build integration

// End-to-end HLS pipeline test (real ffmpeg/ffprobe, excluded from `make ci`):
// a real 320x240 fixture is uploaded over HTTP, the publish hook enqueues a
// transcode job, the worker's DrainJobs runs the REAL media.HLSTranscoder into
// the server's storage backend, and the HTTP endpoints then serve the master
// playlist, variant playlist, and segments with correct types. The single low
// rung for a 240p source also proves cap-at-source end to end.
// Run with: go test -tags integration ./internal/httpapi/
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/transcode"
	"github.com/vidra/vidra-core/internal/video"
)

func TestHLSPipelineEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}

	// The worker service is built after the server (it needs the server's blob
	// backend), so the publish hook closes over a late-bound pointer — exactly
	// how cmd/api wires it, minus the ticker.
	var tcsvc *transcode.Service
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig(),
		video.WithTranscodeHook(func(ctx context.Context, videoID uuid.UUID, sourceKey string) {
			if tcsvc != nil {
				_ = tcsvc.Enqueue(ctx, videoID, sourceKey)
			}
		}),
	)
	hlst, ok := media.DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	tcsvc = transcode.NewService(tcRepo, hlst)

	// Generate a tiny real fixture (~2s, 320x240) and upload it over HTTP.
	fixture := filepath.Join(t.TempDir(), "fixture.mp4")
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=24", fixture)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Real clip","privacy":"public"}`)
	if rec := uploadVideoFile(srv, id, "fixture.mp4", "video/mp4", string(raw), tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The publish hook must have enqueued exactly one job; DrainJobs (the same
	// call the cmd/api worker makes on its ticker) runs the REAL transcoder and
	// persists the playlist + renditions.
	if len(tcRepo.jobs) != 1 {
		t.Fatalf("enqueued jobs = %d, want 1", len(tcRepo.jobs))
	}
	n, err := tcsvc.DrainJobs(context.Background(), 5)
	if err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
	for _, j := range tcRepo.jobs {
		if j.VideoID.String() != id || j.State != "done" {
			t.Fatalf("job = video %s state %q, want %s done", j.VideoID, j.State, id)
		}
	}

	// Detail now advertises HLS with the single cap-at-source rung.
	var v videoView
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.HLSURL == nil || *v.HLSURL != "/api/v1/videos/"+id+"/hls/master.m3u8" {
		t.Fatalf("hls_url = %v, want the master playlist path", v.HLSURL)
	}
	if len(v.Renditions) != 1 || v.Renditions[0].Height != 240 || v.Renditions[0].Width != 320 {
		t.Fatalf("renditions = %+v, want the single 320x240 rung (cap-at-source)", v.Renditions)
	}

	// Master playlist over HTTP: parses, references the 240p variant relatively.
	rec = getHLS(srv, *v.HLSURL, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("master = (%d, %q)", rec.Code, rec.Header().Get("Content-Type"))
	}
	master := rec.Body.String()
	if !strings.HasPrefix(master, "#EXTM3U") || !strings.Contains(master, "RESOLUTION=320x240") {
		t.Fatalf("master malformed:\n%s", master)
	}
	if !strings.Contains(master, "240p/playlist.m3u8") {
		t.Fatalf("master must reference the variant relatively:\n%s", master)
	}

	// Variant playlist over HTTP: parses and lists real segments.
	rec = getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/playlist.m3u8", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("variant = (%d, %q)", rec.Code, rec.Header().Get("Content-Type"))
	}
	variant := rec.Body.String()
	if !strings.HasPrefix(variant, "#EXTM3U") || !strings.Contains(variant, "#EXT-X-ENDLIST") {
		t.Fatalf("variant malformed:\n%s", variant)
	}
	var segments []string
	for _, line := range strings.Split(variant, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			segments = append(segments, line)
		}
	}
	if len(segments) == 0 {
		t.Fatal("variant lists no segments")
	}

	// Every listed segment is fetchable over HTTP as MPEG-TS (sync byte 0x47).
	for _, seg := range segments {
		rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/"+seg, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("segment %q = %d, want 200", seg, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "video/mp2t" {
			t.Errorf("segment %q Content-Type = %q, want video/mp2t", seg, ct)
		}
		if b := rec.Body.Bytes(); len(b) == 0 || b[0] != 0x47 {
			t.Errorf("segment %q is not MPEG-TS (first byte %x)", seg, rec.Body.Bytes()[:1])
		}
	}
}
