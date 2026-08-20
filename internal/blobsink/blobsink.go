// Package blobsink is a loopback HTTP origin that lets ffmpeg write directly
// into a storage.Backend.
//
// # Why this exists
//
// ffmpeg's HLS muxer can push its output over HTTP (-method PUT), but it cannot
// speak S3: every request would need its own SigV4 signature and ffmpeg has no
// way to produce one. Pointing -hls_segment_filename at an S3 URL therefore does
// not work, which is why the usual shape — vidra's included — is to write the
// whole tree to local disk and upload it afterwards. That makes peak scratch a
// multiple of the source size.
//
// A Sink closes the gap: it serves plain HTTP on 127.0.0.1, and each PUT it
// receives is streamed straight into the configured backend, which signs the
// upload properly. ffmpeg sees an ordinary HTTP origin; the object store sees a
// correctly authenticated write; no segment has to exist on disk.
//
// It also serves GET for anything it has stored, because the transcode pipeline
// reads its own output back (the progressive-download remux consumes the variant
// playlist and its segments; the trick-play codec probe reads the media file).
// Those reads become range requests against the backend instead of local file
// reads — which is the trade this component asks callers to make consciously:
// scratch disk for object-store bandwidth.
//
// # Security
//
// The listener binds to loopback only, and every request must carry a per-Sink
// token generated from crypto/rand. Without the token the socket would be an
// unauthenticated write proxy into the deployment's media bucket for any local
// process. Keys are confined to a caller-supplied prefix and validated with the
// same rules as the storage layer, so a crafted path cannot escape it.
//
// # Playlist coalescing
//
// The HLS muxer rewrites the whole playlist after every segment. Passing each
// rewrite through to the backend would mean one object write per segment per
// playlist — pointless on any store, and actively expensive on a versioned one
// (Backblaze B2 buckets are versioned by default, so each rewrite would leave a
// billable hidden version). Playlists are therefore buffered in memory and
// written once, when the caller calls Flush after ffmpeg exits. Segments, which
// are written once and never rewritten, stream straight through.
package blobsink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// coalescedSuffixes are the outputs the HLS muxer rewrites in place as it goes.
// They are buffered until Flush instead of being written per rewrite.
var coalescedSuffixes = []string{".m3u8"}

// maxCoalescedBytes bounds one buffered playlist. A VOD playlist for a long
// video is tens of KiB; this is generous enough never to bite in practice and
// small enough that a runaway writer cannot exhaust memory.
const maxCoalescedBytes = 8 << 20

// Sink is a loopback HTTP origin backed by a storage.Backend. Create it with
// New, point ffmpeg at the URLs from SegmentURL/PlaylistURL, then call Flush
// once the encoder has exited and Close to release the listener.
type Sink struct {
	backend Backend
	prefix  string
	token   string
	srv     *http.Server
	ln      net.Listener

	mu        sync.Mutex
	coalesced map[string][]byte
	// stored is bytes written per key, so the pipeline can total a rendition's
	// size without a scratch directory to walk.
	stored map[string]int64
	err    error
	done   chan struct{}
}

// Backend is the subset of storage.Backend a Sink needs. Narrowed so the
// package does not depend on capabilities it never uses.
type Backend interface {
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// New starts a Sink listening on a random loopback port. Every object it stores
// lands under prefix. The caller must Close it.
func New(backend Backend, prefix string) (*Sink, error) {
	if backend == nil {
		return nil, errors.New("blobsink: backend is required")
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return nil, errors.New("blobsink: prefix is required")
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, fmt.Errorf("blobsink: generate token: %w", err)
	}
	// Loopback only: this endpoint writes into the deployment's media bucket, so
	// it must never be reachable off-host even for a moment.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("blobsink: listen: %w", err)
	}
	s := &Sink{
		backend:   backend,
		prefix:    prefix,
		token:     hex.EncodeToString(tok),
		ln:        ln,
		coalesced: map[string][]byte{},
		stored:    map[string]int64{},
		done:      make(chan struct{}),
	}
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		defer close(s.done)
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// BaseURL is the root ffmpeg writes under, including the token path segment.
func (s *Sink) BaseURL() string {
	return "http://" + s.ln.Addr().String() + "/" + s.token
}

// URL is the loopback URL for a relative path within the sink's prefix, e.g.
// "720p/playlist.m3u8". ffmpeg patterns like "seg_%05d.ts" pass through
// unescaped so the muxer can expand them.
func (s *Sink) URL(rel string) string {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	// Escape each segment, then restore the ffmpeg format specifier: url.PathEscape
	// turns '%' into '%25', which would break -hls_segment_filename patterns.
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(url.PathEscape(p), "%25", "%")
	}
	return s.BaseURL() + "/" + strings.Join(parts, "/")
}

// Err reports the first storage failure a request hit, if any. ffmpeg reports a
// failed PUT only as a generic write error, so callers check this to learn what
// actually went wrong.
func (s *Sink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// BytesUnder totals the bytes stored under a relative sub-path (e.g. "720p").
// With no scratch directory to walk, this is how the pipeline measures what a
// rendition actually cost — the equivalent of stat-ing the tree it used to
// write locally. Buffered playlists count at their current size.
func (s *Sink) BytesUnder(rel string) int64 {
	want := s.prefix + "/" + strings.Trim(rel, "/") + "/"
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for k, n := range s.stored {
		if strings.HasPrefix(k, want) {
			total += n
		}
	}
	for k, b := range s.coalesced {
		if strings.HasPrefix(k, want) {
			total += int64(len(b))
		}
	}
	return total
}

// Get returns the bytes of an object the sink holds, whether already written to
// the backend or still buffered. rel is relative to the sink's prefix.
func (s *Sink) Get(ctx context.Context, rel string) ([]byte, error) {
	key := s.prefix + "/" + strings.Trim(rel, "/")
	s.mu.Lock()
	body, buffered := s.coalesced[key]
	s.mu.Unlock()
	if buffered {
		return append([]byte(nil), body...), nil
	}
	rc, err := s.backend.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// Replace overwrites an object the sink holds. A buffered playlist is updated in
// place so it still reaches the backend exactly once at Flush; anything already
// written goes straight through. The trick-play finalisation uses it to add
// EXT-X-I-FRAMES-ONLY to a playlist ffmpeg has just produced.
func (s *Sink) Replace(ctx context.Context, rel string, body []byte) error {
	key := s.prefix + "/" + strings.Trim(rel, "/")
	s.mu.Lock()
	_, buffered := s.coalesced[key]
	if buffered {
		s.coalesced[key] = append([]byte(nil), body...)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	n, err := s.backend.Put(ctx, key, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("blobsink: replace %q: %w", key, err)
	}
	s.mu.Lock()
	s.stored[key] = n
	s.mu.Unlock()
	return nil
}

// Flush writes every coalesced playlist to the backend. Call it once the encoder
// has exited and the playlists are final.
func (s *Sink) Flush(ctx context.Context) error {
	s.mu.Lock()
	pending := s.coalesced
	s.coalesced = map[string][]byte{}
	firstErr := s.err
	s.mu.Unlock()
	if firstErr != nil {
		return firstErr
	}
	for key, body := range pending {
		n, err := s.backend.Put(ctx, key, strings.NewReader(string(body)))
		if err != nil {
			return fmt.Errorf("blobsink: flush %q: %w", key, err)
		}
		s.mu.Lock()
		s.stored[key] = n
		s.mu.Unlock()
	}
	return nil
}

// Close shuts the listener down. It does not flush; call Flush first.
func (s *Sink) Close() error {
	err := s.srv.Close()
	<-s.done
	return err
}

// key maps a request path to a storage key under the sink's prefix, rejecting
// anything that is not under the token or that tries to escape the prefix.
func (s *Sink) key(reqPath string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(reqPath, "/"), s.token)
	if !ok {
		return "", false
	}
	rel := strings.TrimPrefix(rest, "/")
	if rel == "" {
		return "", false
	}
	// Reject traversal and absolute forms outright rather than cleaning them, so
	// the contract matches the storage layer's key rules exactly.
	if strings.ContainsRune(rel, 0) {
		return "", false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", false
		}
	}
	return s.prefix + "/" + rel, true
}

func (s *Sink) handle(w http.ResponseWriter, r *http.Request) {
	key, ok := s.key(r.URL.Path)
	if !ok {
		// Uniform 404 for a bad token and a bad path: an attacker probing the
		// socket learns nothing about which part was wrong.
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		s.put(w, r, key)
	case http.MethodGet, http.MethodHead:
		s.get(w, r, key)
	case http.MethodDelete:
		// The HLS muxer issues DELETE only for flags vidra does not enable
		// (delete_segments). Accept it as a no-op rather than failing the encode.
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Sink) put(w http.ResponseWriter, r *http.Request, key string) {
	defer func() { _, _ = io.Copy(io.Discard, r.Body) }()

	if isCoalesced(key) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxCoalescedBytes+1))
		if err != nil {
			s.fail(w, key, err)
			return
		}
		if len(body) > maxCoalescedBytes {
			s.fail(w, key, fmt.Errorf("playlist exceeds %d bytes", maxCoalescedBytes))
			return
		}
		s.mu.Lock()
		s.coalesced[key] = body
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		return
	}

	n, err := s.backend.Put(r.Context(), key, r.Body)
	if err != nil {
		s.fail(w, key, err)
		return
	}
	s.mu.Lock()
	s.stored[key] = n
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

func (s *Sink) get(w http.ResponseWriter, r *http.Request, key string) {
	// A playlist still buffered in memory has not reached the backend yet, and
	// the remux reads it back mid-job — serve the buffered copy.
	s.mu.Lock()
	body, buffered := s.coalesced[key]
	s.mu.Unlock()
	if buffered {
		w.Header().Set("Content-Type", contentTypeFor(key))
		http.ServeContent(w, r, path.Base(key), time.Time{}, strings.NewReader(string(body)))
		return
	}

	rc, err := s.backend.Open(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", contentTypeFor(key))
	if rs, ok := rc.(io.ReadSeeker); ok {
		// A seekable backend reader turns into ranged GETs, so ffmpeg's demuxer
		// gets Range support without buffering the object.
		http.ServeContent(w, r, path.Base(key), time.Time{}, rs)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// fail records the first storage error and reports it to ffmpeg as a 500.
func (s *Sink) fail(w http.ResponseWriter, key string, err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = fmt.Errorf("blobsink: store %q: %w", key, err)
	}
	s.mu.Unlock()
	http.Error(w, "store failed", http.StatusInternalServerError)
}

func isCoalesced(key string) bool {
	for _, suffix := range coalescedSuffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func contentTypeFor(key string) string {
	switch {
	case strings.HasSuffix(key, ".m3u8"):
		return "application/vnd.apple.mpegurl"
	case strings.HasSuffix(key, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(key, ".mp4"):
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}
