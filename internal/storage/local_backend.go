package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LocalBackend implements StorageBackend for local filesystem storage.
// Keys are relative paths stored under baseDir.
type LocalBackend struct {
	baseDir string
}

// NewLocalBackend creates a LocalBackend that stores files under baseDir.
func NewLocalBackend(baseDir string) *LocalBackend {
	return &LocalBackend{baseDir: baseDir}
}

func (b *LocalBackend) fullPath(key string) string {
	return filepath.Join(b.baseDir, filepath.FromSlash(key))
}

// Upload writes data from an io.Reader to baseDir/key.
func (b *LocalBackend) Upload(_ context.Context, key string, data io.Reader, _ string) error {
	dest := b.fullPath(key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("local backend upload: mkdir %s: %w", filepath.Dir(dest), err)
	}
	f, err := os.Create(dest) //nolint:gosec // key is internal, not user-controlled
	if err != nil {
		return fmt.Errorf("local backend upload: create %s: %w", dest, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, data); err != nil {
		return fmt.Errorf("local backend upload: write %s: %w", dest, err)
	}
	return nil
}

// UploadFile copies a file from localPath to baseDir/key.
func (b *LocalBackend) UploadFile(_ context.Context, key string, localPath string, _ string) error {
	src, err := os.Open(localPath) //nolint:gosec // localPath is internal
	if err != nil {
		return fmt.Errorf("local backend upload file: open %s: %w", localPath, err)
	}
	defer src.Close()

	dest := b.fullPath(key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("local backend upload file: mkdir %s: %w", filepath.Dir(dest), err)
	}
	dst, err := os.Create(dest) //nolint:gosec
	if err != nil {
		return fmt.Errorf("local backend upload file: create %s: %w", dest, err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("local backend upload file: copy to %s: %w", dest, err)
	}
	return nil
}

// Download returns a ReadCloser for the file at baseDir/key.
func (b *LocalBackend) Download(_ context.Context, key string) (io.ReadCloser, error) {
	path := b.fullPath(key)
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("local backend download: open %s: %w", path, err)
	}
	return f, nil
}

// GetURL returns the absolute filesystem path for the given key.
func (b *LocalBackend) GetURL(key string) string {
	abs, err := filepath.Abs(b.fullPath(key))
	if err != nil {
		return b.fullPath(key)
	}
	return abs
}

// GetSignedURL returns the same as GetURL — local files don't need signing.
func (b *LocalBackend) GetSignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return b.GetURL(key), nil
}

// Delete removes the file at baseDir/key.
func (b *LocalBackend) Delete(_ context.Context, key string) error {
	path := b.fullPath(key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local backend delete: remove %s: %w", path, err)
	}
	return nil
}

// Exists checks whether the file at baseDir/key exists.
func (b *LocalBackend) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(b.fullPath(key))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("local backend exists: stat %s: %w", b.fullPath(key), err)
}

// Copy duplicates the file at sourceKey to destKey within the same base directory.
func (b *LocalBackend) Copy(ctx context.Context, sourceKey, destKey string) error {
	src, err := b.Download(ctx, sourceKey)
	if err != nil {
		return fmt.Errorf("local backend copy: %w", err)
	}
	defer src.Close()
	return b.Upload(ctx, destKey, src, "")
}

// GetMetadata returns filesystem metadata for the file at baseDir/key.
func (b *LocalBackend) GetMetadata(_ context.Context, key string) (*FileMetadata, error) {
	path := b.fullPath(key)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("local backend get metadata: stat %s: %w", path, err)
	}
	return &FileMetadata{
		Key:          key,
		Size:         info.Size(),
		LastModified: info.ModTime(),
	}, nil
}
