package port

import (
	"context"
	"time"
)

// CacheRepository defines the interface for caching operations
type CacheRepository interface {
	// Get retrieves a value from the cache
	Get(ctx context.Context, key string) (string, error)
	// Set stores a value in the cache with an expiration time
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	// Del removes a value from the cache
	Del(ctx context.Context, key string) error
}
