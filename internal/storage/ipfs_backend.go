package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"vidra-core/internal/ipfs"
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

// Copy downloads the source CID content and re-adds it to IPFS, ensuring
// the content is locally pinned. Because IPFS is content-addressed, the
// resulting CID is identical for the same content.
func (i *IPFSBackend) Copy(ctx context.Context, sourceKey, destKey string) error {
	rc, err := i.client.Cat(ctx, sourceKey)
	if err != nil {
		return fmt.Errorf("failed to read source for IPFS copy: %w", err)
	}
	defer rc.Close()
	_, err = i.client.AddReader(ctx, destKey, rc)
	if err != nil {
		return fmt.Errorf("failed to re-add content during IPFS copy: %w", err)
	}
	return nil
}

// GetMetadata returns metadata for an IPFS object via the object/stat API.
func (i *IPFSBackend) GetMetadata(ctx context.Context, key string) (*FileMetadata, error) {
	stat, err := i.client.ObjectStat(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get IPFS object metadata: %w", err)
	}
	return &FileMetadata{
		Key:          key,
		Size:         stat.CumulativeSize,
		ContentType:  "application/octet-stream",
		LastModified: time.Time{}, // IPFS does not track modification time
		ETag:         key,
	}, nil
}
