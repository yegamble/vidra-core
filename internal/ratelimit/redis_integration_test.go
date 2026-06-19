//go:build integration

// Integration tests require a live Redis reachable via REDIS_URL. Run with:
//
//	docker compose --profile core up -d redis
//	REDIS_URL=redis://localhost:6379/0 go test -tags=integration ./internal/ratelimit/...
package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis integration test")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse REDIS_URL: %v", err)
	}
	return redis.NewClient(opt)
}

// TestRedisCounterFixedWindow proves the Redis counter enforces a fixed window:
// the count rises monotonically within the window, the TTL is set once (not
// refreshed each call), and the count resets after the window expires.
func TestRedisCounterFixedWindow(t *testing.T) {
	client := redisClient(t)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	counter := NewRedisCounter(client)
	// Unique key per run to avoid cross-test contamination.
	key := "test:fixedwindow:" + time.Now().Format("150405.000000")
	defer client.Del(ctx, "ratelimit:"+key)

	window := 2 * time.Second

	c1, ttl1, err := counter.Incr(ctx, key, window)
	if err != nil {
		t.Fatalf("incr 1: %v", err)
	}
	if c1 != 1 {
		t.Fatalf("count 1 = %d, want 1", c1)
	}
	if ttl1 <= 0 || ttl1 > window {
		t.Fatalf("ttl 1 = %v, want (0, %v]", ttl1, window)
	}

	c2, ttl2, err := counter.Incr(ctx, key, window)
	if err != nil {
		t.Fatalf("incr 2: %v", err)
	}
	if c2 != 2 {
		t.Fatalf("count 2 = %d, want 2", c2)
	}
	// TTL must not be refreshed upward by the second call (fixed, not sliding).
	if ttl2 > ttl1 {
		t.Errorf("ttl refreshed: ttl2 %v > ttl1 %v (window should be fixed)", ttl2, ttl1)
	}

	// After the window elapses the key expires and the count resets.
	time.Sleep(window + 500*time.Millisecond)
	c3, _, err := counter.Incr(ctx, key, window)
	if err != nil {
		t.Fatalf("incr 3: %v", err)
	}
	if c3 != 1 {
		t.Errorf("count after window = %d, want 1 (reset)", c3)
	}
}
