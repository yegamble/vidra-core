package peertubeimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// sizeRecordingBackend is a destination store that remembers what length it was
// told to expect. It implements storage.SizedPutter because that is the whole
// question: a destination that is told SizeUnknown uploads multipart with a
// 16 MiB part buffer, however small the object.
type sizeRecordingBackend struct {
	storage.Backend
	sawSize int64
	written map[string][]byte
}

func newSizeRecordingBackend(t *testing.T) *sizeRecordingBackend {
	t.Helper()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return &sizeRecordingBackend{Backend: local, sawSize: -2, written: map[string][]byte{}}
}

func (b *sizeRecordingBackend) PutSized(ctx context.Context, key string, r io.Reader, size int64) (int64, error) {
	b.sawSize = size
	buf, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	b.written[key] = buf
	return int64(len(buf)), nil
}

func (b *sizeRecordingBackend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	return b.PutSized(ctx, key, r, storage.SizeUnknown)
}

// TestCopyMediaPassesTheSourceLength proves the importer tells the destination
// how long the object is.
//
// The subtlety this pins is the io.LimitReader in copyMedia: it is a CAP
// (maxSourceFileBytes+1, a guard against an absurd source), not a length, and it
// hides the source reader's concrete type from the sniffing PutSizedHashed does.
// So the length has to be read off the SOURCE reader before it is wrapped —
// without that, every imported thumbnail and caption went up with an unknown
// length.
func TestCopyMediaPassesTheSourceLength(t *testing.T) {
	ctx := context.Background()
	srcRoot := t.TempDir()
	src, err := storage.NewLocal(srcRoot)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	body := []byte("a small thumbnail's worth of bytes")
	if _, err := src.Put(ctx, "thumbnails/x.jpg", bytes.NewReader(body)); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	dest := newSizeRecordingBackend(t)
	im := &Importer{srcMedia: src, destMedia: dest}

	n, sum, err := im.copyMedia(ctx, "thumbnails/x.jpg", "thumbnails/y.jpg")
	if err != nil {
		t.Fatalf("copyMedia: %v", err)
	}
	if n != int64(len(body)) {
		t.Errorf("copied %d bytes, want %d", n, len(body))
	}
	want := sha256.Sum256(body)
	if sum != hex.EncodeToString(want[:]) {
		t.Errorf("checksum = %q, want %q", sum, hex.EncodeToString(want[:]))
	}
	if dest.sawSize != int64(len(body)) {
		t.Errorf("destination was told size %d, want the exact length %d — the LimitReader cap must never be passed as a size, and SizeUnknown costs a multipart upload per object", dest.sawSize, len(body))
	}
	if string(dest.written["thumbnails/y.jpg"]) != string(body) {
		t.Errorf("stored bytes = %q, want %q", dest.written["thumbnails/y.jpg"], body)
	}
}
