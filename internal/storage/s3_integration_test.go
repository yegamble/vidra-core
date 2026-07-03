//go:build integration

// S3 backend integration tests require a live S3-compatible store (MinIO via
// the compose "storage" profile) reachable via S3_TEST_ENDPOINT; they self-skip
// when it is unset. Run with:
//
//	docker compose --profile storage up -d minio
//	S3_TEST_ENDPOINT=localhost:9000 go test -tags=integration ./internal/storage/...
//
// Optional overrides (defaults match the compose minio service):
// S3_TEST_ACCESS_KEY (vidra), S3_TEST_SECRET_KEY (vidra-dev-secret),
// S3_TEST_BUCKET (vidra-test), S3_TEST_USE_SSL (false).
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newS3TestBackend builds an S3 backend against the MinIO named by
// S3_TEST_ENDPOINT, skipping the test when it is unset.
func newS3TestBackend(t *testing.T) *S3 {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping S3 integration test")
	}
	useSSL := false
	if v := os.Getenv("S3_TEST_USE_SSL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			t.Fatalf("parse S3_TEST_USE_SSL: %v", err)
		}
		useSSL = b
	}
	cfg := S3Config{
		Endpoint:       endpoint,
		Bucket:         envOr("S3_TEST_BUCKET", "vidra-test"),
		AccessKey:      envOr("S3_TEST_ACCESS_KEY", "vidra"),
		SecretKey:      envOr("S3_TEST_SECRET_KEY", "vidra-dev-secret"),
		UseSSL:         useSSL,
		ForcePathStyle: true,
	}
	b, err := NewS3(cfg)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	return b
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// testKey returns a per-test unique object key and registers cleanup.
func testKey(t *testing.T, b *S3, suffix string) string {
	t.Helper()
	key := fmt.Sprintf("integration/%s/%d/%s", strings.ToLower(t.Name()), time.Now().UnixNano(), suffix)
	t.Cleanup(func() { _ = b.Delete(context.Background(), key) })
	return key
}

func TestS3PutOpenRoundTrip(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	key := testKey(t, b, "original.bin")
	content := []byte("hello vidra via s3")

	n, err := b.Put(ctx, key, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("wrote %d bytes, want %d", n, len(content))
	}

	rc, err := b.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("read %q, want %q", got, content)
	}
}

func TestS3PutOverwrites(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	key := testKey(t, b, "poster.jpg")
	if _, err := b.Put(ctx, key, strings.NewReader("first")); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	if _, err := b.Put(ctx, key, strings.NewReader("second")); err != nil {
		t.Fatalf("Put second: %v", err)
	}
	rc, err := b.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "second" {
		t.Errorf("read %q, want %q", got, "second")
	}
}

func TestS3ExistsAndDelete(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	key := testKey(t, b, "k")
	if ok, err := b.Exists(ctx, key); err != nil || ok {
		t.Fatalf("Exists before Put = (%v, %v), want (false, nil)", ok, err)
	}
	if _, err := b.Put(ctx, key, strings.NewReader("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ok, err := b.Exists(ctx, key); err != nil || !ok {
		t.Fatalf("Exists after Put = (%v, %v), want (true, nil)", ok, err)
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, err := b.Exists(ctx, key); err != nil || ok {
		t.Fatalf("Exists after Delete = (%v, %v), want (false, nil)", ok, err)
	}
	// Delete is idempotent.
	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete (missing) = %v, want nil", err)
	}
}

func TestS3OpenMissingIsNotFound(t *testing.T) {
	b := newS3TestBackend(t)
	key := testKey(t, b, "nope.bin")
	if _, err := b.Open(context.Background(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open(missing) err = %v, want ErrNotFound", err)
	}
}

// TestS3ServeContentRange proves the reader Open returns is seekable and works
// under http.ServeContent — the exact path internal/httpapi's
// serveStoredObject takes for a backend without PathProvider — including HTTP
// Range/206 handling (seeks translate to ranged GETs against the store).
func TestS3ServeContentRange(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	key := testKey(t, b, "clip.mp4")
	content := []byte("0123456789")
	if _, err := b.Put(ctx, key, bytes.NewReader(content)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := b.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	rs, ok := rc.(io.ReadSeeker)
	if !ok {
		t.Fatal("S3 Open reader does not implement io.ReadSeeker; Range serving would silently degrade to full-body 200s")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/clip.mp4", nil)
	req.Header.Set("Range", "bytes=2-5")
	http.ServeContent(rec, req, "", time.Time{}, rs)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "2345" {
		t.Errorf("range body = %q, want %q", got, "2345")
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 2-5/10" {
		t.Errorf("Content-Range = %q, want %q", cr, "bytes 2-5/10")
	}
}

// TestS3TempDownloadFallbackContract proves the probe/transcode temp-download
// path works against the real store: without a PathProvider, streaming the
// object to a temp file (what internal/media.objectPath does) yields the exact
// stored bytes.
func TestS3TempDownloadFallbackContract(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	key := testKey(t, b, "source.mp4")
	content := []byte("fake video bytes")
	if _, err := b.Put(ctx, key, bytes.NewReader(content)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := b.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(t.TempDir(), "vidra-media-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("temp download = %q, want %q", got, content)
	}
}

func TestS3DeletePrefix(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	prefix := fmt.Sprintf("integration/%s/%d/hls", strings.ToLower(t.Name()), time.Now().UnixNano())
	inside := []string{prefix + "/master.m3u8", prefix + "/720p/seg_00001.ts"}
	outside := prefix + "-other/master.m3u8"
	for _, key := range append(inside, outside) {
		key := key
		if _, err := b.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
		t.Cleanup(func() { _ = b.Delete(context.Background(), key) })
	}
	if err := b.DeletePrefix(ctx, prefix); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	for _, key := range inside {
		if ok, _ := b.Exists(ctx, key); ok {
			t.Errorf("%q survived DeletePrefix", key)
		}
	}
	if ok, _ := b.Exists(ctx, outside); !ok {
		t.Errorf("%q outside the prefix was deleted", outside)
	}
	if err := b.DeletePrefix(ctx, prefix); err != nil {
		t.Fatalf("second DeletePrefix (idempotence): %v", err)
	}
}

// TestS3ListKeys proves the ObjectLister capability the media GC relies on
// against a live MinIO: recursive listing scoped to a prefix, and empty for a
// missing prefix.
func TestS3ListKeys(t *testing.T) {
	b := newS3TestBackend(t)
	ctx := context.Background()
	root := fmt.Sprintf("integration/%s/%d", strings.ToLower(t.Name()), time.Now().UnixNano())
	prefix := root + "/streaming-playlists"
	inside := []string{prefix + "/v1/master.m3u8", prefix + "/v1/720p/seg_00000.ts"}
	outside := root + "/web-videos/a.mp4"
	for _, key := range append(append([]string{}, inside...), outside) {
		key := key
		if _, err := b.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
		t.Cleanup(func() { _ = b.Delete(context.Background(), key) })
	}

	keys, err := b.ListKeys(ctx, prefix)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("ListKeys(%q) = %v, want 2 keys", prefix, keys)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix+"/") {
			t.Errorf("listed key %q outside the prefix", k)
		}
		if k == outside {
			t.Errorf("listed key %q from a sibling prefix", k)
		}
	}
	// A missing prefix returns no keys, not an error.
	empty, err := b.ListKeys(ctx, root+"/absent-prefix")
	if err != nil {
		t.Fatalf("ListKeys(missing): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListKeys(missing) = %v, want empty", empty)
	}
}
