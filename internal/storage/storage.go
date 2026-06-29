// Package storage abstracts blob storage for media (originals, renditions,
// thumbnails, captions) behind a small Backend interface. The local-filesystem
// backend is the development default; S3-compatible and IPFS backends land
// later behind the same interface. Keys are forward-slash object paths laid out
// PeerTube-style — one top-level dir per asset kind, e.g. "web-videos/<id>.mp4"
// or "thumbnails/<id>.jpg" (see vidra-core/.ralph/specs/storage-layout.md) —
// never OS paths.
package storage

import (
	"context"
	"errors"
	"io"
)

// Sentinel errors callers can branch on.
var (
	// ErrInvalidKey means the object key is empty, absolute, or attempts to
	// escape the storage root (path traversal).
	ErrInvalidKey = errors.New("storage: invalid key")
	// ErrNotFound means no object exists at the key.
	ErrNotFound = errors.New("storage: object not found")
)

// Backend is a content store keyed by opaque, forward-slash object paths. All
// methods take a context so remote backends can honour cancellation/timeouts;
// the local backend ignores it.
type Backend interface {
	// Put stores r at key (creating intermediate "directories"), overwriting any
	// existing object, and returns the number of bytes written.
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	// Open returns a reader for the object at key. ErrNotFound when absent. The
	// caller must Close the returned reader.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object at key. It is idempotent: deleting a missing
	// object is not an error.
	Delete(ctx context.Context, key string) error
	// Exists reports whether an object is stored at key.
	Exists(ctx context.Context, key string) (bool, error)
}

// PathProvider is an optional capability implemented by backends that can expose
// a local filesystem path for an object (the local backend does). Tools that
// need a seekable file on disk — e.g. ffprobe — use it; backends without it
// require the caller to stream the object to a temporary file first.
type PathProvider interface {
	// Path returns the local filesystem path for key. It resolves the key (and
	// rejects unsafe ones with ErrInvalidKey) but does not require the object to
	// exist.
	Path(key string) (string, error)
}
