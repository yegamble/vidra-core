package storage

import (
    "context"
    "fmt"
    "io"
    "net/url"
    "os"
    "path/filepath"
    "time"
)

// LocalStore implements ObjectStore using the local filesystem. It stores
// objects under a root directory. Buckets are implemented as sub‑directories.
// This implementation is useful for development or small installations
// where a full S3 service is unnecessary.
type LocalStore struct {
    Root string
}

// NewLocalStore creates a LocalStore writing files under root. The root
// directory must exist and be writable.
func NewLocalStore(root string) *LocalStore {
    return &LocalStore{Root: root}
}

// Upload writes the object body to the specified bucket and key on local
// disk. It creates directories as needed. Size and contentType are
// ignored for local storage but retained for interface compatibility.
func (ls *LocalStore) Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
    path := filepath.Join(ls.Root, bucket, key)
    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        return fmt.Errorf("mkdir: %w", err)
    }
    f, err := os.Create(path)
    if err != nil {
        return fmt.Errorf("create file: %w", err)
    }
    defer f.Close()
    if _, err := io.Copy(f, body); err != nil {
        return fmt.Errorf("copy: %w", err)
    }
    return nil
}

// Get opens the object at bucket/key and returns a ReadCloser.
func (ls *LocalStore) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
    path := filepath.Join(ls.Root, bucket, key)
    return os.Open(path)
}

// Delete removes the object at bucket/key.
func (ls *LocalStore) Delete(ctx context.Context, bucket, key string) error {
    path := filepath.Join(ls.Root, bucket, key)
    return os.Remove(path)
}

// PresignUpload is not supported for local storage and returns nil.
func (ls *LocalStore) PresignUpload(ctx context.Context, bucket, key string, expiry time.Duration) (*url.URL, error) {
    return nil, nil
}