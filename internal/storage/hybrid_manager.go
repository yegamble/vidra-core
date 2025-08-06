package storage

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    
    "github.com/go-redis/redis/v8"
    
    "gotube/internal/service"
)

// HybridStorageManager routes object retrieval through a tiered storage
// architecture. It first attempts to serve from a local cache, falls back
// to an object store (e.g. S3) and then uses IPFS as a final fallback.
// Metrics are recorded in Redis for monitoring popularity and latency.
type HybridStorageManager struct {
    LocalStore  *LocalStore
    ObjectStore ObjectStore
    IPFS        *service.IPFSService
    Redis       *redis.Client
}

// NewHybridStorageManager constructs a new manager. The local store and
// object store may be nil if not used. IPFS may also be nil if not
// configured. Redis is optional but recommended for metrics.
func NewHybridStorageManager(local *LocalStore, obj ObjectStore, ipfs *service.IPFSService, rdb *redis.Client) *HybridStorageManager {
    return &HybridStorageManager{
        LocalStore:  local,
        ObjectStore: obj,
        IPFS:        ipfs,
        Redis:       rdb,
    }
}

// GetVideo returns a reader for the requested video. The key typically
// corresponds to a file path like "videoID/resolution/playlist.m3u8".
// It tries local, object and IPFS in order.
func (hm *HybridStorageManager) GetVideo(ctx context.Context, key string) (io.ReadCloser, error) {
    // 1. Local
    if hm.LocalStore != nil {
        if reader, err := hm.LocalStore.Get(ctx, "videos", key); err == nil {
            if hm.Redis != nil {
                hm.Redis.Incr(ctx, fmt.Sprintf("metrics:%s:local", key))
            }
                return reader, nil
        }
    }
    // 2. Object store
    if hm.ObjectStore != nil {
        if reader, err := hm.ObjectStore.Get(ctx, "videos", key); err == nil {
            if hm.Redis != nil {
                hm.Redis.Incr(ctx, fmt.Sprintf("metrics:%s:object", key))
            }
            // Optionally seed to IPFS asynchronously
            if hm.IPFS != nil {
                go func() {
                    tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("seed-%s", filepath.Base(key)))
                    f, err := os.Create(tmpFile)
                    if err != nil {
                        return
                    }
                    defer os.Remove(tmpFile)
                    defer f.Close()
                    if _, err := io.Copy(f, reader); err != nil {
                        return
                    }
                    hm.IPFS.AddFile(tmpFile)
                }()
            }
            return reader, nil
        }
    }
    // 3. IPFS
    if hm.IPFS != nil {
        // Key is not a CID; look up CID in a database or mapping (omitted)
        // For demonstration, treat key as a CID.
        if data, err := hm.IPFS.Cat(key); err == nil {
            if hm.Redis != nil {
                hm.Redis.Incr(ctx, fmt.Sprintf("metrics:%s:ipfs", key))
            }
            return io.NopCloser(bytes.NewReader(data)), nil
        }
    }
    return nil, fmt.Errorf("video %s not found", key)
}