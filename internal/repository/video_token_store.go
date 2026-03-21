package repository

import (
	"context"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RedisVideoTokenStore implements video.VideoTokenStore using Redis.
type RedisVideoTokenStore struct {
	rdb *redis.Client
}

// NewRedisVideoTokenStore creates a new RedisVideoTokenStore.
func NewRedisVideoTokenStore(rdb *redis.Client) *RedisVideoTokenStore {
	return &RedisVideoTokenStore{rdb: rdb}
}

func (s *RedisVideoTokenStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("video token store set: %w", err)
	}
	return nil
}

func (s *RedisVideoTokenStore) Get(ctx context.Context, key string) (string, error) {
	val, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return "", fmt.Errorf("video token store get: %w", err)
	}
	return val, nil
}

func (s *RedisVideoTokenStore) Del(ctx context.Context, key string) error {
	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("video token store del: %w", err)
	}
	return nil
}
