package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCacheRepository implements CacheRepository using Redis
type RedisCacheRepository struct {
	client *redis.Client
}

// NewRedisCacheRepository creates a new RedisCacheRepository
func NewRedisCacheRepository(client *redis.Client) *RedisCacheRepository {
	return &RedisCacheRepository{client: client}
}

// Get retrieves a value from Redis
func (r *RedisCacheRepository) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil // Return empty string on miss to indicate not found
	}
	if err != nil {
		return "", fmt.Errorf("redis get failed: %w", err)
	}
	return val, nil
}

// Set stores a value in Redis with expiration
func (r *RedisCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	err := r.client.Set(ctx, key, value, ttl).Err()
	if err != nil {
		return fmt.Errorf("redis set failed: %w", err)
	}
	return nil
}

// Del removes a value from Redis
func (r *RedisCacheRepository) Del(ctx context.Context, key string) error {
	err := r.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("redis del failed: %w", err)
	}
	return nil
}
