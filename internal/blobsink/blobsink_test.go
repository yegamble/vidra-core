package blobsink

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memBackend is an in-memory Backend recording the exact sequence of writes, so
// tests can assert not just what was stored but how many times.
//
// Everything it holds is reachable from a Sink handler goroutine, so tests read
// it through the accessors below rather than touching the maps directly.
type memBackend struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    []string
	putErr  error
	// onPut runs inside Put, before the write is recorded. Set it before New so
	// the handler goroutines the sink starts see it. Tests use it to hold a store
	// open and observe what the sink does while a PUT is still in flight.
	onPut func(key string)
}

func newMemBackend() *memBackend {
	return &memBackend{objects: map[string][]byte{}}
}

func (m *memBackend) Put(_ context.Context, key string, r io.Reader) (int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if m.onPut != nil {
		m.onPut(key)
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

// object returns a stored object's bytes, or nil.
func (m *memBackend) object(key string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.objects[key]
}

// keys returns every stored key, sorted.
func (m *memBackend) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// putKeys returns the keys written, in order, including repeats.
func (m *memBackend) putKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.puts...)
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
	got := string(b.object("streaming-playlists/vid1/720p/seg_00000.ts"))
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
	if got := string(b.object(key)); got != "#EXTM3U\nv3-final" {
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
	if n := len(b.putKeys()); n != 2 {
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
	if reached := b.putKeys(); len(reached) != 0 {
		t.Errorf("unauthenticated requests reached the backend: %v", reached)
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
	for _, key := range b.keys() {
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

const heldSegmentKey = "streaming-playlists/vid1/720p/seg_00000.ts"

// heldPut leaves a sink in the state ffmpeg leaves behind: a segment PUT whose
// body has arrived and whose store is still running, on a connection the client
// has already abandoned without reading the response. The returned func releases
// the store. Both completion barriers have to survive exactly this.
func heldPut(t *testing.T) (*Sink, *memBackend, func()) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	b := newMemBackend()
	// Set before New: every handler goroutine descends from the one New starts,
	// so this write is ordered ahead of the reads of it.
	b.onPut = func(string) {
		close(started)
		<-release
	}
	s, err := New(b, "streaming-playlists/vid1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conn, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil {
		t.Fatalf("dial sink: %v", err)
	}
	// Written by hand rather than with an http.Client, which would wait for the
	// response the muxer never reads.
	const body = "segment-bytes"
	if _, err := fmt.Fprintf(conn, "PUT /%s/720p/seg_00000.ts HTTP/1.1\r\nHost: sink\r\nContent-Length: %d\r\n\r\n%s",
		s.token, len(body), body); err != nil {
		t.Fatalf("write PUT: %v", err)
	}
	<-started
	_ = conn.Close()
	return s, b, func() { close(release) }
}

// TestFlushWaitsForInFlightPuts is the durability barrier the pipeline depends
// on. ffmpeg's exit is not proof its last segment landed, so if Flush returned
// while a store was still running the transcode would carry on over a partial
// HLS tree: the progressive-download remux reads the ladder back through the
// sink and would 404 on the missing segment, and BytesUnder would undercount it.
func TestFlushWaitsForInFlightPuts(t *testing.T) {
	s, b, release := heldPut(t)
	defer func() { _ = s.Close() }()

	done := make(chan error, 1)
	go func() { done <- s.Flush(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("Flush returned (err=%v) with a segment PUT still in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := string(b.object(heldSegmentKey)); got != "segment-bytes" {
		t.Errorf("segment = %q after Flush, want it stored", got)
	}
}

// TestCloseWaitsForInFlightPuts pins the other half: closing the listener does
// not stop handlers that are already running, so without a wait a goroutine
// would still be writing into the media bucket after the caller believes the
// sink is done with it.
func TestCloseWaitsForInFlightPuts(t *testing.T) {
	s, b, release := heldPut(t)

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		t.Fatalf("Close returned (err=%v) with a segment PUT still in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := string(b.object(heldSegmentKey)); got != "segment-bytes" {
		t.Errorf("segment = %q after Close, want the in-flight store settled", got)
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

// TestDiscardedOutputIsReadableButNeverStored covers the case where a muxer has
// to write a file the pipeline only wants to read. ffmpeg's dash muxer always
// emits its own HLS master playlist; the CMAF packager reads the codec strings
// out of it and then replaces it with the master Vidra authors.
//
// Storing it and deleting it afterwards also "works", which is why it is worth
// pinning that this does not: on a bucket with versioning on by default
// (Backblaze B2) that delete leaves a billable hide-marker behind on every
// single transcode, forever.
func TestDiscardedOutputIsReadableButNeverStored(t *testing.T) {
	s, b := newTestSink(t)
	const rel = "cmaf/ffmpeg-master.m3u8"
	const key = "streaming-playlists/vid1/" + rel
	s.Discard(rel)

	_ = do(t, http.MethodPut, s.URL(rel), "#EXTM3U\nffmpeg's own").Body.Close()
	_ = do(t, http.MethodPut, s.URL("cmaf/media_0.m3u8"), "#EXTM3U\nkeep me").Body.Close()

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n := b.putCount(key); n != 0 {
		t.Errorf("discarded object was stored %d times, want 0 (it would need deleting again, "+
			"which on a versioned bucket is a billable hide-marker per transcode)", n)
	}
	// Its sibling still lands, so Discard is not a blanket opt-out of flushing.
	if got := string(b.object("streaming-playlists/vid1/cmaf/media_0.m3u8")); got != "#EXTM3U\nkeep me" {
		t.Errorf("non-discarded playlist = %q", got)
	}

	// Readable AFTER the flush barrier, because that is when the pipeline's
	// finalisation actually reads it.
	body, err := s.Get(context.Background(), rel)
	if err != nil {
		t.Fatalf("Get after Flush: %v", err)
	}
	if string(body) != "#EXTM3U\nffmpeg's own" {
		t.Errorf("read back %q", body)
	}
	resp := do(t, http.MethodGet, s.URL(rel), "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET discarded object = %d, want 200", resp.StatusCode)
	}

	// And it is not counted as something this rendition cost to store.
	if n := s.BytesUnder("cmaf"); n != int64(len("#EXTM3U\nkeep me")) {
		t.Errorf("BytesUnder = %d, want only the stored sibling (%d)", n, len("#EXTM3U\nkeep me"))
	}
}
