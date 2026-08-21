package blobsink

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// memBackend is an in-memory Backend recording the exact sequence of writes, so
// tests can assert not just what was stored but how many times.
type memBackend struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    []string
	putErr  error
}

func newMemBackend() *memBackend {
	return &memBackend{objects: map[string][]byte{}}
}

func (m *memBackend) Put(_ context.Context, key string, r io.Reader) (int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return 0, m.putErr
	}
	m.objects[key] = b
	m.puts = append(m.puts, key)
	return int64(len(b)), nil
}

func (m *memBackend) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (m *memBackend) putCount(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, k := range m.puts {
		if k == key {
			n++
		}
	}
	return n
}

func newTestSink(t *testing.T) (*Sink, *memBackend) {
	t.Helper()
	b := newMemBackend()
	s, err := New(b, "streaming-playlists/vid1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, b
}

func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func TestNewValidatesArguments(t *testing.T) {
	if _, err := New(nil, "p"); err == nil {
		t.Error("New(nil backend) succeeded, want error")
	}
	if _, err := New(newMemBackend(), "  "); err == nil {
		t.Error("New(empty prefix) succeeded, want error")
	}
}

// TestPutStreamsIntoBackend is the core contract: a PUT ffmpeg makes becomes an
// object in the backend, under the sink's prefix, with no local file involved.
func TestPutStreamsIntoBackend(t *testing.T) {
	s, b := newTestSink(t)
	resp := do(t, http.MethodPut, s.URL("720p/seg_00000.ts"), "segment-bytes")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	got := string(b.objects["streaming-playlists/vid1/720p/seg_00000.ts"])
	if got != "segment-bytes" {
		t.Errorf("stored %q, want the PUT body", got)
	}
}

// TestPlaylistRewritesCoalesceToOneWrite pins the versioned-store contract. The
// HLS muxer rewrites the whole playlist after every segment; passing each
// rewrite through would be one object write per segment, and on a bucket with
// versioning on by default (Backblaze B2) every superseded rewrite would remain
// as a billable hidden version.
func TestPlaylistRewritesCoalesceToOneWrite(t *testing.T) {
	s, b := newTestSink(t)
	const key = "streaming-playlists/vid1/720p/playlist.m3u8"

	for i, body := range []string{"#EXTM3U\nv1", "#EXTM3U\nv2", "#EXTM3U\nv3-final"} {
		resp := do(t, http.MethodPut, s.URL("720p/playlist.m3u8"), body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("rewrite %d status = %d, want 201", i, resp.StatusCode)
		}
	}
	if n := b.putCount(key); n != 0 {
		t.Errorf("playlist hit the backend %d times before Flush, want 0", n)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n := b.putCount(key); n != 1 {
		t.Errorf("playlist written %d times, want exactly 1 after three rewrites", n)
	}
	if got := string(b.objects[key]); got != "#EXTM3U\nv3-final" {
		t.Errorf("stored playlist = %q, want the LAST rewrite", got)
	}
}

// TestSegmentsAreNotCoalesced proves only rewritten outputs are buffered:
// segments are written once and must stream straight through, or a long encode
// would accumulate the whole ladder in memory.
func TestSegmentsAreNotCoalesced(t *testing.T) {
	s, b := newTestSink(t)
	for _, name := range []string{"720p/seg_00000.ts", "720p/seg_00001.ts"} {
		resp := do(t, http.MethodPut, s.URL(name), "x")
		_ = resp.Body.Close()
	}
	if n := len(b.puts); n != 2 {
		t.Errorf("%d backend writes, want 2 (segments must not buffer)", n)
	}
}

// TestGetServesStoredAndBufferedObjects covers the read-back the pipeline needs:
// the progressive-download remux consumes the variant playlist and its segments
// while the playlist is still buffered in memory.
func TestGetServesStoredAndBufferedObjects(t *testing.T) {
	s, _ := newTestSink(t)
	_ = do(t, http.MethodPut, s.URL("720p/seg_00000.ts"), "segment-bytes").Body.Close()
	_ = do(t, http.MethodPut, s.URL("720p/playlist.m3u8"), "#EXTM3U\nseg_00000.ts").Body.Close()

	t.Run("stored segment", func(t *testing.T) {
		resp := do(t, http.MethodGet, s.URL("720p/seg_00000.ts"), "")
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "segment-bytes" {
			t.Errorf("GET segment = %q", body)
		}
	})
	t.Run("buffered playlist is readable before Flush", func(t *testing.T) {
		resp := do(t, http.MethodGet, s.URL("720p/playlist.m3u8"), "")
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "#EXTM3U\nseg_00000.ts" {
			t.Errorf("GET buffered playlist = %q; the remux reads it mid-job", body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
			t.Errorf("Content-Type = %q", ct)
		}
	})
	t.Run("missing object is 404", func(t *testing.T) {
		resp := do(t, http.MethodGet, s.URL("720p/nope.ts"), "")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
}

// TestRejectsRequestsWithoutTheToken is the security contract. Without it the
// socket is an unauthenticated write proxy into the deployment's media bucket
// for every other process on the host.
func TestRejectsRequestsWithoutTheToken(t *testing.T) {
	s, b := newTestSink(t)
	base := "http://" + s.ln.Addr().String()
	for _, p := range []string{
		"/720p/seg_00000.ts",
		"/wrong-token/720p/seg_00000.ts",
		"/",
		"/" + strings.Repeat("a", 64) + "/720p/seg.ts",
	} {
		resp := do(t, http.MethodPut, base+p, "hostile")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("PUT %s status = %d, want 404", p, resp.StatusCode)
		}
	}
	if len(b.puts) != 0 {
		t.Errorf("unauthenticated requests reached the backend: %v", b.puts)
	}
}

// TestRejectsPathEscape proves a crafted path cannot write outside the sink's
// prefix — the same rule the storage layer enforces on keys.
func TestRejectsPathEscape(t *testing.T) {
	s, b := newTestSink(t)
	for _, p := range []string{"../escape.ts", "a/../../escape.ts", "a/./b.ts"} {
		// Bypass URL()'s cleaning to send the raw path a hostile client would.
		resp := do(t, http.MethodPut, s.BaseURL()+"/"+p, "hostile")
		_ = resp.Body.Close()
	}
	for key := range b.objects {
		if !strings.HasPrefix(key, "streaming-playlists/vid1/") {
			t.Errorf("stored %q outside the sink prefix", key)
		}
	}
}

// TestErrSurfacesStorageFailure proves a backend failure is retrievable. ffmpeg
// reports a failed PUT only as a generic write error, so without this the real
// cause would be lost.
func TestErrSurfacesStorageFailure(t *testing.T) {
	b := newMemBackend()
	b.putErr = io.ErrClosedPipe
	s, err := New(b, "p")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = s.Close() }()

	resp := do(t, http.MethodPut, s.URL("720p/seg_00000.ts"), "x")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if s.Err() == nil {
		t.Fatal("Err() = nil after a failed store")
	}
	if !strings.Contains(s.Err().Error(), "seg_00000.ts") {
		t.Errorf("Err() = %v, want the failing key named", s.Err())
	}
	// Flush must report the recorded failure rather than appearing to succeed.
	if err := s.Flush(context.Background()); err == nil {
		t.Error("Flush() = nil after a failed store")
	}
}

// TestURLPreservesFFmpegPattern guards the segment-filename pattern: percent
// escaping would turn seg_%05d.ts into seg_%2505d.ts and the muxer would write a
// single literally-named object instead of a numbered sequence.
func TestURLPreservesFFmpegPattern(t *testing.T) {
	s, _ := newTestSink(t)
	got := s.URL("720p/seg_%05d.ts")
	if !strings.HasSuffix(got, "/720p/seg_%05d.ts") {
		t.Errorf("URL = %q, want the ffmpeg pattern intact", got)
	}
	if !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Errorf("URL = %q, want a loopback origin", got)
	}
}

// TestBindsLoopbackOnly pins that the listener is not reachable off-host.
func TestBindsLoopbackOnly(t *testing.T) {
	s, _ := newTestSink(t)
	if !strings.HasPrefix(s.ln.Addr().String(), "127.0.0.1:") {
		t.Errorf("listening on %s, want a loopback address", s.ln.Addr())
	}
}
