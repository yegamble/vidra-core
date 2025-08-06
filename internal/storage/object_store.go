package storage

import (
    "context"
    "io"
    "net/url"
    "time"
)

// ObjectStore defines the methods required for S3‑compatible storage. It
// abstracts away the underlying storage backend (AWS S3, Backblaze B2,
// DigitalOcean Spaces or local filesystem). Implementations should
// provide concurrency‑safe operations and proper error handling.
//
// Upload uploads an object to the given bucket and key. The caller
// provides an io.Reader and the expected size in bytes along with
// content type.
//
// Get fetches an object from the given bucket and key. It returns a
// ReadCloser that the caller must close.
//
// Delete removes an object from the given bucket and key.
//
// PresignUpload generates a pre‑signed URL for uploading an object.
// This is optional and may return nil if not supported.
type ObjectStore interface {
    Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error
    Get(ctx context.Context, bucket, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, bucket, key string) error
    PresignUpload(ctx context.Context, bucket, key string, expiry time.Duration) (*url.URL, error)
}