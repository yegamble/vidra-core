package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"athena/internal/ipfs"
)

// IPFSBackend implements the StorageBackend interface for IPFS storage
type IPFSBackend struct {
	client     *ipfs.Client
	gatewayURL string
}

// IPFSConfig holds configuration for IPFS storage
type IPFSConfig struct {
	Client     *ipfs.Client
	GatewayURL string // e.g., "https://ipfs.io" or custom gateway
}

// NewIPFSBackend creates a new IPFS storage backend
func NewIPFSBackend(cfg IPFSConfig) (*IPFSBackend, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("IPFS client is required")
	}
	if cfg.GatewayURL == "" {
		cfg.GatewayURL = "https://ipfs.io"
	}

	return &IPFSBackend{
		client:     cfg.Client,
		gatewayURL: cfg.GatewayURL,
	}, nil
}

// Upload uploads data to IPFS
func (i *IPFSBackend) Upload(ctx context.Context, key string, data io.Reader, contentType string) error {
	// Note: key is not used in IPFS as it uses content-addressing (CID)
	// We return the CID but the interface doesn't capture it directly
	// In practice, you'd want to modify the interface or use UploadFile
	// For now, this method is not fully supported for IPFS
	return fmt.Errorf("Upload with io.Reader not supported for IPFS - use UploadFile instead")
}

// UploadFile uploads a file from local filesystem to IPFS
func (i *IPFSBackend) UploadFile(ctx context.Context, key string, localPath string, contentType string) error {
	_, err := i.client.AddFile(ctx, localPath)
	if err != nil {
		return fmt.Errorf("failed to upload file to IPFS: %w", err)
	}

	// File is automatically pinned by AddFile
	return nil
}

// Download downloads a file from IPFS using the CID stored in the key
func (i *IPFSBackend) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	// In IPFS, the "key" would typically be the CID
	// This is a simplified implementation
	return nil, fmt.Errorf("IPFS download not implemented - use gateway URL instead")
}

// GetURL returns the public URL for accessing content via IPFS gateway
func (i *IPFSBackend) GetURL(key string) string {
	// Assume key is the CID
	return fmt.Sprintf("%s/ipfs/%s", i.gatewayURL, key)
}

// GetSignedURL is not applicable for IPFS (content is public by default)
func (i *IPFSBackend) GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	// IPFS content is publicly accessible, no signed URLs
	return i.GetURL(key), nil
}

// Delete attempts to unpin content from IPFS
// Note: Unpinning doesn't guarantee deletion due to IPFS's distributed nature
func (i *IPFSBackend) Delete(ctx context.Context, key string) error {
	// Note: Actual deletion in IPFS requires unpinning
	// This is a simplified stub
	return fmt.Errorf("IPFS delete not implemented - content is immutable")
}

// Exists checks if content is pinned in our IPFS node
func (i *IPFSBackend) Exists(ctx context.Context, key string) (bool, error) {
	// This would require checking if CID is pinned
	// Simplified implementation
	return false, fmt.Errorf("IPFS exists check not implemented")
}

// Copy is not applicable for IPFS (content-addressed storage)
func (i *IPFSBackend) Copy(ctx context.Context, sourceKey, destKey string) error {
	// In IPFS, copying is just referencing the same CID
	return nil
}

// GetMetadata retrieves metadata about IPFS content
func (i *IPFSBackend) GetMetadata(ctx context.Context, key string) (*FileMetadata, error) {
	// This would require calling IPFS stat
	// Simplified stub
	return &FileMetadata{
		Key:          key,
		Size:         0,
		ContentType:  "application/octet-stream",
		LastModified: time.Now(),
		ETag:         key, // Use CID as ETag
	}, nil
}
