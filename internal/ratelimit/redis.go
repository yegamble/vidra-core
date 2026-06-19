package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCounter is a Counter backed by Redis. It uses a single pipelined
// INCR + EXPIRE(NX) + PTTL so the window's TTL is set only on the first request
// of a window (ExpireNX is a no-op when a TTL already exists), giving a true
// fixed window rather than a sliding one.
type RedisCounter struct {
	client    redis.Cmdable
	keyPrefix string
}

// NewRedisCounter builds a RedisCounter. Keys are namespaced under "ratelimit:".
func NewRedisCounter(client redis.Cmdable) *RedisCounter {
	return &RedisCounter{client: client, keyPrefix: "ratelimit:"}
}

// Incr implements Counter using a pipelined INCR / ExpireNX / PTTL.
func (r *RedisCounter) Incr(ctx context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	k := r.keyPrefix + key

	pipe := r.client.Pipeline()
	incr := pipe.Incr(ctx, k)
	pipe.ExpireNX(ctx, k, window)
	pttl := pipe.PTTL(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, err
	}

	ttl := pttl.Val()
	if ttl < 0 { // -1 (no expiry yet) or -2 (missing) — fall back to the window
		ttl = window
	}
	return incr.Val(), ttl, nil
}
