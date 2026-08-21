package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

// helloWorldSHA256 is the published SHA-256 of "hello world". The digest is
// pinned to a known vector rather than recomputed in the test, so a change to
// how PutSizedHashed takes the hash fails here instead of quietly agreeing with
// itself.
const (
	helloWorld       = "hello world"
	helloWorldSHA256 = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
)

func TestPutSizedHashedReturnsTheDigestOfTheStoredBytes(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		size int64
	}{
		{"exact size", int64(len(helloWorld))},
		{"size unknown", SizeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "thumbnails/" + strings.ReplaceAll(tc.name, " ", "-") + ".jpg"
			n, sum, err := PutSizedHashed(ctx, b, key, strings.NewReader(helloWorld), tc.size)
			if err != nil {
				t.Fatalf("PutSizedHashed: %v", err)
			}
			if n != int64(len(helloWorld)) {
				t.Errorf("wrote %d bytes, want %d", n, len(helloWorld))
			}
			if sum != helloWorldSHA256 {
				t.Errorf("sha256 = %q, want %q", sum, helloWorldSHA256)
			}
			// The digest must describe what is actually retrievable, not what
			// the caller handed in.
			rc, err := b.Open(ctx, key)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = rc.Close() }()
			stored, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			want := sha256.Sum256(stored)
			if hex.EncodeToString(want[:]) != sum {
				t.Errorf("digest does not match the stored object")
			}
		})
	}
}

// An empty object still gets a real digest (the SHA-256 of no bytes), never the
// empty string — the empty string is the "not computed yet" state and a
// legitimately empty object must not be mistaken for one.
func TestPutSizedHashedHashesAnEmptyObject(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	_, sum, err := PutSizedHashed(ctx, b, "captions/empty.vtt", bytes.NewReader(nil), 0)
	if err != nil {
		t.Fatalf("PutSizedHashed: %v", err)
	}
	if sum != emptySHA256 {
		t.Errorf("sha256 = %q, want %q", sum, emptySHA256)
	}
}

// plainBackend hides Local's SizedPutter (and every other optional capability)
// so the degrade-to-Put path is exercised: hashing must not depend on a backend
// implementing the sized upload.
type plainBackend struct{ inner Backend }

func (p plainBackend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	return p.inner.Put(ctx, key, r)
}
func (p plainBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return p.inner.Open(ctx, key)
}
func (p plainBackend) Delete(ctx context.Context, key string) error {
	return p.inner.Delete(ctx, key)
}
func (p plainBackend) Exists(ctx context.Context, key string) (bool, error) {
	return p.inner.Exists(ctx, key)
}

func TestPutSizedHashedWorksWithoutSizedPutter(t *testing.T) {
	ctx := context.Background()
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, sum, err := PutSizedHashed(ctx, plainBackend{inner: local}, "web-videos/plain.mp4",
		strings.NewReader(helloWorld), int64(len(helloWorld)))
	if err != nil {
		t.Fatalf("PutSizedHashed: %v", err)
	}
	if sum != helloWorldSHA256 {
		t.Errorf("sha256 = %q, want %q", sum, helloWorldSHA256)
	}
}

// A failed Put returns no digest. A partial upload's hash describes bytes that
// are not a complete object, and handing one back would let a caller record it.
func TestPutSizedHashedReturnsNoDigestOnFailure(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, sum, err := PutSizedHashed(ctx, b, "../escape.mp4", strings.NewReader(helloWorld), SizeUnknown); err == nil {
		t.Fatal("PutSizedHashed accepted a traversal key")
	} else if sum != "" {
		t.Errorf("sha256 = %q on failure, want empty", sum)
	}
}
