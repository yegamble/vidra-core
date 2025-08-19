package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisSessionRepository implements session storage using Redis
type redisSessionRepository struct {
	rdb *redis.Client
}

func NewRedisSessionRepository(rdb *redis.Client) *redisSessionRepository {
	return &redisSessionRepository{rdb: rdb}
}

// Session keys
func sessionKey(id string) string          { return "sess:" + id }
func userSessionsKey(userID string) string { return "user:sessions:" + userID }

func (r *redisSessionRepository) CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	pipe := r.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey(sessionID), userID, ttl)
	pipe.SAdd(ctx, userSessionsKey(userID), sessionID)
	pipe.Expire(ctx, userSessionsKey(userID), ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis create session failed: %w", err)
	}
	return nil
}

func (r *redisSessionRepository) GetSession(ctx context.Context, sessionID string) (string, error) {
	userID, err := r.rdb.Get(ctx, sessionKey(sessionID)).Result()
	if err != nil {
		return "", fmt.Errorf("redis get session failed: %w", err)
	}
	return userID, nil
}

func (r *redisSessionRepository) DeleteSession(ctx context.Context, sessionID string) error {
	// Fetch user to remove from index set
	userID, _ := r.rdb.Get(ctx, sessionKey(sessionID)).Result()
	pipe := r.rdb.TxPipeline()
	pipe.Del(ctx, sessionKey(sessionID))
	if userID != "" {
		pipe.SRem(ctx, userSessionsKey(userID), sessionID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis delete session failed: %w", err)
	}
	return nil
}

func (r *redisSessionRepository) DeleteAllUserSessions(ctx context.Context, userID string) error {
	key := userSessionsKey(userID)
	ids, err := r.rdb.SMembers(ctx, key).Result()
	if err != nil {
		// If set missing, treat as no-op
		return nil
	}
	pipe := r.rdb.TxPipeline()
	for _, id := range ids {
		pipe.Del(ctx, sessionKey(id))
	}
	pipe.Del(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis delete all sessions failed: %w", err)
	}
	return nil
}
