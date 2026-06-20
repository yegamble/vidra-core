//go:build integration

// Integration tests require a live Redis reachable via REDIS_URL. Run with:
//
//	docker compose --profile core up -d redis
//	REDIS_URL=redis://localhost:6379/0 go test -tags=integration ./internal/cache/...
package cache

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

// TestDeduperFirstOnlyOncePerWindow proves the first call for a key returns true
// and repeats within the window return false, until the window elapses.
func TestDeduperFirstOnlyOncePerWindow(t *testing.T) {
	client := redisClient(t)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	d := NewDeduper(client)
	key := "test:dedupe:" + time.Now().Format("150405.000000")
	defer client.Del(ctx, key)

	window := 2 * time.Second

	first, err := d.First(ctx, key, window)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !first {
		t.Fatal("first call = false, want true")
	}

	again, err := d.First(ctx, key, window)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if again {
		t.Error("second call within window = true, want false")
	}

	time.Sleep(window + 500*time.Millisecond)
	after, err := d.First(ctx, key, window)
	if err != nil {
		t.Fatalf("after window: %v", err)
	}
	if !after {
		t.Error("call after window = false, want true (reset)")
	}
}
