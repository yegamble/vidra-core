package cache

import (
    "context"
    "github.com/redis/go-redis/v9"
    "github.com/yourname/gotube/internal/config"
)

func New(cfg *config.Config) *redis.Client {
    return redis.NewClient(&redis.Options{
        Addr:     cfg.RedisAddr,
        Password: cfg.RedisPassword,
        DB:       0,
    })
}

var ctx = context.Background()

func Health(rdb *redis.Client) error {
    return rdb.Ping(ctx).Err()
}
