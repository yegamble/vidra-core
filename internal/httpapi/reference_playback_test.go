package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestApprovedReferencePlayback(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	id := uuid.New()
	master := "streaming-playlists/" + id.String() + "/r5/master.m3u8"
	prefix := "/api/v1/videos/" + id.String() + "/ipfs-reference/r5/"
	dir, err := os.MkdirTemp("/tmp", "vidra-ref-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "ref.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("forwarded a viewer credential or query")
		}
		switch r.URL.Path {
		case prefix + "master.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		case prefix + "cmaf/seg-01.m4s":
			if r.Header.Get("Range") != "bytes=0-1" {
				t.Error("lost byte range")
			}
			w.Header().Set("Content-Range", "bytes 0-1/4")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("ab"))
		case prefix + "cmaf/redirect.m4s":
			http.Redirect(w, r, "https://unrelated.invalid/", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	bridge.Listener.Close()
	bridge.Listener = l
	bridge.Start()
	t.Cleanup(bridge.Close)
	eligible := master
	remainingChecks := -1
	WithReferencePlayback(socket, func(ctx context.Context, video uuid.UUID) (string, error) {
		if video != id || eligible == "" || remainingChecks == 0 {
			return "", pgx.ErrNoRows
		}
		if remainingChecks > 0 {
			remainingChecks--
		}
		return eligible, nil
	})(env.srv)
	check := func(method, path, byteRange string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Cookie", "ignored=secret")
		r.Header.Set("Authorization", "Bearer ignored")
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		rec := httptest.NewRecorder()
		env.srv.Handler().ServeHTTP(rec, r)
		if rec.Code != want || rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s: got %d %q", method, path, rec.Code, rec.Header().Get("Cache-Control"))
		}
		return rec
	}
	check("HEAD", prefix+"master.m3u8", "", 200)
	rec := check("GET", prefix+"cmaf/seg-01.m4s?ignore=1", "bytes=0-1", 206)
	if rec.Body.String() != "ab" || rec.Header().Get("Content-Type") != "video/mp4" || rec.Header().Get("Content-Range") != "bytes 0-1/4" {
		t.Fatal("partial response corrupted")
	}
	check("GET", prefix+"cmaf/withdrawn.m4s", "", 404)
	check("GET", prefix+"../r6/seek.ts", "", 404)
	check("GET", prefix+"cmaf%2fseg-01.m4s", "", 404)
	check("GET", prefix+"cmaf/redirect.m4s", "", 503)
	check("GET", prefix+"cmaf/seg-01.m4s", "bytes=1-2,3-4", 416)
	remainingChecks = 1
	check("GET", prefix+"cmaf/seg-01.m4s", "bytes=0-1", 404)
	remainingChecks = -1
	eligible = "streaming-playlists/" + id.String() + "/r6/master.m3u8"
	check("GET", prefix+"master.m3u8", "", 404)
	eligible = ""
	check("GET", prefix+"master.m3u8", "", 404)
}
