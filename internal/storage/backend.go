package storage

import (
	"context"
	"io"
	"time"
)

// StorageBackend defines the interface for different storage backends (local, S3, IPFS)
type StorageBackend interface {
	// Upload uploads a file to the storage backend
	Upload(ctx context.Context, key string, data io.Reader, contentType string) error

	// UploadFile uploads a file from a local path
	UploadFile(ctx context.Context, key string, localPath string, contentType string) error

	// Download downloads a file from the storage backend
	Download(ctx context.Context, key string) (io.ReadCloser, error)

	// GetURL returns the public URL for accessing the file
	GetURL(key string) string

	// GetSignedURL returns a time-limited signed URL for private access
	GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error)

	// Delete removes a file from the storage backend
	Delete(ctx context.Context, key string) error

	// Exists checks if a file exists in the storage backend
	Exists(ctx context.Context, key string) (bool, error)

	// Copy copies a file within the storage backend
	Copy(ctx context.Context, sourceKey, destKey string) error

	// GetMetadata retrieves metadata about a stored file
	GetMetadata(ctx context.Context, key string) (*FileMetadata, error)
}

// FileMetadata represents metadata about a stored file
type FileMetadata struct {
	Key          string
	Size         int64
	ContentType  string
	LastModified time.Time
	ETag         string
}

// StorageTier represents the storage tier for a file
type StorageTier string

const (
	TierHot  StorageTier = "hot"  // Local fast storage
	TierWarm StorageTier = "warm" // IPFS distributed storage
	TierCold StorageTier = "cold" // S3-compatible cold storage
)
