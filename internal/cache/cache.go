package cache

import (
    "context"
    "github.com/redis/go-redis/v9"
    "github.com/yegamble/athena/internal/config"
)

// New creates a new Redis client using the provided configuration.  The
// returned client is safe for concurrent use.  It is the caller's
// responsibility to close the client when no longer needed.
func New(cfg *config.Config) *redis.Client {
    return redis.NewClient(&redis.Options{
        Addr:     cfg.RedisAddr,
        Password: cfg.RedisPassword,
        DB:       0,
    })
}

// ctx is a package-level context used for simple health checks.
var ctx = context.Background()

// Health pings the Redis server to verify connectivity.
func Health(rdb *redis.Client) error {
    return rdb.Ping(ctx).Err()
}