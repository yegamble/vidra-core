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
	"crypto/sha256"
	"encoding/hex"
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

// PresignResponse overrides the headers the store sends when a presigned URL is
// used. Empty fields are left to the store.
//
// It exists because a presigned redirect has to be header-equivalent to the API
// byte path it replaces, and Vidra's byte path builds those headers from the
// DATABASE, not from the object: objects are uploaded with no Content-Type at
// all, and a download's filename comes from the video row rather than the key.
// Without overrides, redirecting `/download/original` would save "<uuid>.mp4"
// instead of the creator's filename, and redirecting `/original` would offer an
// application/octet-stream download where a video used to play inline.
type PresignResponse struct {
	// ContentType is the media type the object is served with.
	ContentType string
	// ContentDisposition is the full header value (e.g. an RFC 2183
	// `attachment; filename="..."`), already formatted by the caller.
	ContentDisposition string
	// CacheControl is the cache policy the OBJECT RESPONSE carries. A signed
	// URL is a per-viewer credential, so this is how a delivery redirect keeps
	// the bytes behind it out of shared caches too.
	CacheControl string
}

// IsZero reports whether no override is requested.
func (p PresignResponse) IsZero() bool {
	return p.ContentType == "" && p.ContentDisposition == "" && p.CacheControl == ""
}

// ResponsePresigner is an optional capability implemented by backends that can
// presign a GET *and* pin the response headers that GET answers with. The S3
// backend implements it (the S3 API's `response-*` query overrides, supported by
// MinIO, AWS, Backblaze B2 and DigitalOcean Spaces alike); the local backend
// cannot presign at all.
//
// Consumers that need header equivalence must feature-detect this rather than
// Presigner and DECLINE to redirect when it is absent — degrading to a signed
// URL with the wrong headers is not the same delivery.
type ResponsePresigner interface {
	Presigner
	// PresignGetAs is PresignGet with response-header overrides. A zero
	// PresignResponse is exactly PresignGet.
	PresignGetAs(ctx context.Context, key string, ttl time.Duration, resp PresignResponse) (string, error)
}

// SizeUnknown is the size argument meaning "the caller cannot say how many bytes
// r will yield". Backends must still store the object correctly; they just
// cannot pick an upload strategy from the length.
const SizeUnknown int64 = -1

// SizedReader is an optional capability implemented by readers that already know
// exactly how many bytes they will still yield. Recognising it is what lets a
// source that is itself an object store hand the destination a length — see
// S3.Open, whose reader carries the size the Stat it already performs returned.
//
// CAUTION for implementors: several standard-library readers spell a DIFFERENT
// question `Size() int64`. *bytes.Reader, *strings.Reader and *io.SectionReader
// all report their ORIGINAL length, which is not the remaining one once anything
// has been read. This interface's Size is REMAINING bytes, and sniffSize
// therefore matches the concrete types it already understood first and only
// falls through to this interface for types it does not recognise.
type SizedReader interface {
	io.Reader
	// Size reports how many bytes are left to read, or SizeUnknown when the
	// reader cannot say exactly. An inexact answer is worse than none: the
	// backend reads precisely the length it is told.
	Size() int64
}

// SizeOf reports how many bytes r will still yield, or SizeUnknown when that
// cannot be established without consuming it. Only lengths that are provably
// exact are returned.
//
// It is exported for callers that must WRAP r before handing it to a Put — an
// io.LimitReader guard, say — because the wrapper hides r's concrete type and
// with it the single-PUT path. Sniff before wrapping, then pass the answer
// explicitly. A wrapper that caps rather than bounds (LimitReader with a
// defensive maximum) must NOT pass its own bound as the size: that is an upper
// limit, and the backend would sit waiting for bytes that are not coming.
func SizeOf(r io.Reader) int64 { return sniffSize(r) }

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

// PutSizedHashed stores r at key exactly like PutSized and additionally returns
// the SHA-256 of the bytes stored, as lowercase hex. The digest is taken from
// the same single pass that uploads: the reader is tee'd into the hash as the
// backend consumes it, so nothing is buffered and nothing is read twice — which
// matters, because the objects going through here are whole video originals.
//
// The returned digest is only meaningful when err is nil. A partial upload
// hashes only the bytes that made it, and the object it would describe does not
// exist as a complete object anyway.
//
// It sniffs the length itself when the caller passes SizeUnknown, because the
// tee wrapper hides r's concrete type from the backend and would otherwise cost
// callers the single-PUT path they get today (see sniffSize / SizedPutter: an
// unknown length turns a 4 KB thumbnail into a multipart upload with a 16 MiB
// buffer). Callers that know the exact length should still pass it.
func PutSizedHashed(ctx context.Context, b Backend, key string, r io.Reader, size int64) (int64, string, error) {
	if size < 0 {
		size = sniffSize(r)
	}
	h := sha256.New()
	n, err := PutSized(ctx, b, key, io.TeeReader(r, h), size)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
