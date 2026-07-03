package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// seekableFakeBackend is a storage.Backend whose Open returns an io.ReadSeeker
// but which — like the S3 backend — does NOT implement storage.PathProvider.
// It stands in for a remote object store in handler tests.
type seekableFakeBackend struct {
	objects map[string][]byte
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

func (b *seekableFakeBackend) Put(_ context.Context, key string, r io.Reader) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	b.objects[key] = data
	return int64(len(data)), nil
}

func (b *seekableFakeBackend) Open(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := b.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return nopSeekCloser{bytes.NewReader(data)}, nil
}

func (b *seekableFakeBackend) Delete(_ context.Context, key string) error {
	delete(b.objects, key)
	return nil
}

func (b *seekableFakeBackend) Exists(_ context.Context, key string) (bool, error) {
	_, ok := b.objects[key]
	return ok, nil
}

// TestStreamOriginalRangeViaSeekableBackend proves serveStoredObject honours
// HTTP Range requests through a backend WITHOUT a filesystem path, as long as
// Open returns a seekable reader — the contract the S3 backend provides. This
// is the unit-level counterpart of the MinIO-backed
// storage.TestS3ServeContentRange integration test.
func TestStreamOriginalRangeViaSeekableBackend(t *testing.T) {
	srv, blobs, _, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	// Mirror the stored original ("tiny") into a seekable, path-less backend and
	// swap it in as the server's media store.
	key := "web-videos/" + id + ".mp4"
	rc, err := blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("open uploaded original: %v", err)
	}
	fake := &seekableFakeBackend{objects: map[string][]byte{}}
	if _, err := fake.Put(context.Background(), key, rc); err != nil {
		t.Fatalf("mirror original: %v", err)
	}
	_ = rc.Close()
	if _, ok := interface{}(fake).(storage.PathProvider); ok {
		t.Fatal("test backend must not implement PathProvider (that is the point)")
	}
	srv.media = fake

	// Full fetch still works (200, whole body).
	rec := streamOriginal(srv, id, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stream = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "tiny" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "tiny")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}

	// Range fetch gets a real 206 slice — not a degraded full-body 200.
	rec = streamOriginal(srv, id, "", "bytes=0-1")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range stream = %d, want 206; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ti" {
		t.Errorf("range body = %q, want %q", rec.Body.String(), "ti")
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-1/4" {
		t.Errorf("Content-Range = %q, want bytes 0-1/4", cr)
	}
}

// TestStreamOriginalMissingObjectViaSeekableBackend keeps the 404 contract for
// path-less backends: a recorded file whose object vanished is a clean 404.
func TestStreamOriginalMissingObjectViaSeekableBackend(t *testing.T) {
	srv, _, _, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	srv.media = &seekableFakeBackend{objects: map[string][]byte{}} // empty store

	rec := streamOriginal(srv, id, "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stream with missing object = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
