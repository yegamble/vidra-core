package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"athena/internal/ipfs"
)

type IPFSBackend struct {
	client     *ipfs.Client
	gatewayURL string
}

type IPFSConfig struct {
	Client     *ipfs.Client
	GatewayURL string // e.g., "https://ipfs.io" or custom gateway
}

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

func (i *IPFSBackend) Upload(ctx context.Context, key string, data io.Reader, contentType string) error {
	_, err := i.client.AddReader(ctx, key, data)
	if err != nil {
		return fmt.Errorf("failed to upload to IPFS: %w", err)
	}
	return nil
}

func (i *IPFSBackend) UploadFile(ctx context.Context, key string, localPath string, contentType string) error {
	_, err := i.client.AddFile(ctx, localPath)
	if err != nil {
		return fmt.Errorf("failed to upload file to IPFS: %w", err)
	}
	return nil
}

func (i *IPFSBackend) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := i.client.Cat(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download from IPFS: %w", err)
	}
	return rc, nil
}

func (i *IPFSBackend) GetURL(key string) string {
	return fmt.Sprintf("%s/ipfs/%s", i.gatewayURL, key)
}

func (i *IPFSBackend) GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	return i.GetURL(key), nil
}

// Note: Unpinning doesn't guarantee deletion due to IPFS's distributed nature.
func (i *IPFSBackend) Delete(ctx context.Context, key string) error {
	if err := i.client.Unpin(ctx, key); err != nil {
		return fmt.Errorf("failed to delete from IPFS: %w", err)
	}
	return nil
}

func (i *IPFSBackend) Exists(ctx context.Context, key string) (bool, error) {
	pinned, err := i.client.IsPinned(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to check IPFS existence: %w", err)
	}
	return pinned, nil
}

func (i *IPFSBackend) Copy(ctx context.Context, sourceKey, destKey string) error {
	return nil
}

func (i *IPFSBackend) GetMetadata(ctx context.Context, key string) (*FileMetadata, error) {
	return &FileMetadata{
		Key:          key,
		Size:         0,
		ContentType:  "application/octet-stream",
		LastModified: time.Now(),
		ETag:         key,
	}, nil
}
