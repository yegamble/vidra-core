// Package storage abstracts blob storage for media (originals, renditions,
// thumbnails, captions) behind a small Backend interface. The local-filesystem
// backend is the development default; the S3 backend covers S3-compatible
// object stores (MinIO, AWS S3, Backblaze B2, DigitalOcean Spaces); an IPFS
// backend lands later. Keys are forward-slash object paths laid out
// PeerTube-style — one top-level dir per asset kind, e.g. "web-videos/<id>.mp4"
// or "thumbnails/<id>.jpg" (see vidra-core/.ralph/specs/storage-layout.md) —
// never OS paths.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
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

// PrefixDeleter is an optional capability implemented by backends that can
// remove every object under a key prefix (a "directory") in one call — used by
// best-effort media cleanup (e.g. a deleted video's HLS ladder, whose segment
// objects are not individually recorded in the database). Both first-party
// backends implement it; callers must feature-detect and degrade gracefully
// when a backend does not.
type PrefixDeleter interface {
	// DeletePrefix removes all objects stored under prefix. Idempotent: a
	// prefix with no objects is not an error. The prefix is validated by the
	// same key rules as every other method.
	DeletePrefix(ctx context.Context, prefix string) error
}

// ObjectLister is an optional capability implemented by backends that can
// enumerate the object keys stored under a prefix — used by the media
// garbage-collector to find orphaned blobs. Both first-party backends implement
// it; callers must feature-detect and skip GC of a prefix when a backend does
// not support listing.
type ObjectLister interface {
	// ListKeys returns every object key stored under prefix (recursively), as
	// forward-slash keys relative to the backend root (i.e. the same keys Put
	// accepts). An empty/absent prefix returns no keys, not an error. The prefix
	// is validated by the same key rules as every other method.
	ListKeys(ctx context.Context, prefix string) ([]string, error)
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

// Presigner is an optional capability implemented by backends that can mint a
// time-limited URL granting read access to one object without the caller
// holding credentials. The S3 backend implements it; the local backend cannot
// (it has no HTTP surface of its own).
//
// The URL embeds a signature derived from the backend's secret key, so it is
// itself a credential for that object: it must never be logged, traced, stored,
// or included in an error message. Mint it as late as possible and give it the
// shortest workable lifetime.
type Presigner interface {
	// PresignGet returns a URL that serves the object at key by plain HTTP GET,
	// valid for ttl. It does not verify the object exists — a missing object
	// surfaces as a 404 when the URL is used. ErrInvalidKey for an unsafe key.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// SizeUnknown is the size argument meaning "the caller cannot say how many bytes
// r will yield". Backends must still store the object correctly; they just
// cannot pick an upload strategy from the length.
const SizeUnknown int64 = -1

// SizedPutter is an optional capability implemented by backends that can upload
// more efficiently when the object's exact byte length is known up front. The S3
// backend implements it because the difference is large and non-obvious: with an
// unknown length the SDK must assume the 5 TiB maximum and divide by the
// 10,000-part limit, which yields a ~528 MiB part buffer per call AND turns even
// a 4 KB thumbnail into a three-request multipart upload. With a known length at
// or below the part size it is a single PUT and no buffer at all — and an HLS
// ladder is thousands of small objects.
//
// Callers should prefer the package-level PutSized helper, which feature-detects
// and degrades to plain Put.
type SizedPutter interface {
	// PutSized stores exactly size bytes read from r at key, overwriting any
	// existing object, and returns the number of bytes written. size must be the
	// exact length r will yield; pass SizeUnknown when it cannot be known.
	PutSized(ctx context.Context, key string, r io.Reader, size int64) (int64, error)
}

// PutSized stores r at key, telling the backend the object's exact length when
// the caller knows it. Backends that cannot use the hint fall back to Put, so
// this is always safe to call. Pass SizeUnknown when the length is not known —
// a wrong size is an error, not a hint.
func PutSized(ctx context.Context, b Backend, key string, r io.Reader, size int64) (int64, error) {
	if size >= 0 {
		if sp, ok := b.(SizedPutter); ok {
			return sp.PutSized(ctx, key, r, size)
		}
	}
	return b.Put(ctx, key, r)
}
