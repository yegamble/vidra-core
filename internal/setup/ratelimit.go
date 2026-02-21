package setup

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	rateLimitWindow   = 5 * time.Minute
	rateLimitMax      = 3
	rateLimitMapCap   = 1000
	redisKeyPrefix    = "athena:wizard:ratelimit:"
)

// RateLimiter checks whether a client IP has exceeded the rate limit
// for test connection requests during the setup wizard.
type RateLimiter interface {
	// CheckRateLimit returns true if the request is allowed, false if rate limited.
	CheckRateLimit(clientIP string) bool
}

// memoryRateLimiter is an in-memory sliding window rate limiter.
// Rate limits reset on process restart. Suitable for the single-use setup wizard.
type memoryRateLimiter struct {
	requests map[string][]int64
}

// NewMemoryRateLimiter creates an in-memory rate limiter.
func NewMemoryRateLimiter() RateLimiter {
	return &memoryRateLimiter{
		requests: make(map[string][]int64),
	}
}

func (rl *memoryRateLimiter) CheckRateLimit(clientIP string) bool {
	if len(rl.requests) > rateLimitMapCap {
		for k := range rl.requests {
			delete(rl.requests, k)
		}
	}

	now := time.Now().Unix()
	windowStart := now - int64(rateLimitWindow.Seconds())

	rl.requests[clientIP] = append(rl.requests[clientIP], now)

	recent := make([]int64, 0, len(rl.requests[clientIP]))
	for _, t := range rl.requests[clientIP] {
		if t >= windowStart {
			recent = append(recent, t)
		}
	}
	rl.requests[clientIP] = recent

	return len(recent) <= rateLimitMax
}

// redisRateLimiter uses Redis Sorted Sets (ZSET) for persistent sliding window
// rate limiting. Rate limits survive process restarts and work across multiple
// wizard instances.
type redisRateLimiter struct {
	client *redis.Client
}

// NewRedisRateLimiter creates a Redis-backed rate limiter.
// Falls back to in-memory if the Redis client is nil.
func NewRedisRateLimiter(client *redis.Client) RateLimiter {
	if client == nil {
		return NewMemoryRateLimiter()
	}
	return &redisRateLimiter{client: client}
}

func (rl *redisRateLimiter) CheckRateLimit(clientIP string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	key := redisKeyPrefix + clientIP
	now := time.Now()
	nowUnix := float64(now.UnixMilli())
	windowStart := float64(now.Add(-rateLimitWindow).UnixMilli())

	pipe := rl.client.Pipeline()

	// Remove expired entries
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%f", windowStart))

	// Add current request
	pipe.ZAdd(ctx, key, redis.Z{Score: nowUnix, Member: nowUnix})

	// Count entries in window
	countCmd := pipe.ZCard(ctx, key)

	// Set TTL so keys auto-expire
	pipe.Expire(ctx, key, rateLimitWindow+time.Minute)

	_, err := pipe.Exec(ctx)
	if err != nil {
		// On Redis error, allow the request (fail open for wizard usability)
		return true
	}

	return countCmd.Val() <= rateLimitMax
}
