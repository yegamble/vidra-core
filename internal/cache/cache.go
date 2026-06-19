// Package cache provides the Redis client used for sessions, rate limiting,
// idempotency keys, and hot status lookups. Redis is never the durable source
// of truth for data that must survive a restart.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache wraps a Redis client.
type Cache struct {
	Client *redis.Client
}

// New parses a Redis URL, builds a client, and verifies connectivity with a
// ping bounded by ctx.
func New(ctx context.Context, redisURL string) (*Cache, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse redis url: %w", err)
	}
	client := redis.NewClient(opt)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}
	return &Cache{Client: client}, nil
}

// Ping checks Redis connectivity, bounded by ctx. Used by readiness probes.
func (c *Cache) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.Client.Ping(pingCtx).Err()
}

// Close closes the Redis client.
func (c *Cache) Close() error {
	if c.Client != nil {
		return c.Client.Close()
	}
	return nil
}
