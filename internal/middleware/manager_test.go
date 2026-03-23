package middleware

import (
	"context"
	"testing"
	"time"
)

func TestNewRateLimiterManager(t *testing.T) {
	manager := NewRateLimiterManager()
	if manager == nil {
		t.Fatal("Expected NewRateLimiterManager to return a non-nil pointer")
	}

	if manager.limiters == nil {
		t.Error("Expected limiters slice to be initialized")
	}

	if len(manager.limiters) != 0 {
		t.Errorf("Expected limiters slice to be empty, got length %d", len(manager.limiters))
	}
}

func TestRateLimiterManager_CreateRateLimiter(t *testing.T) {
	manager := NewRateLimiterManager()
	rate := time.Second
	burst := 10

	rl := manager.CreateRateLimiter(rate, burst)
	if rl == nil {
		t.Fatal("Expected CreateRateLimiter to return a non-nil pointer")
	}

	if len(manager.limiters) != 1 {
		t.Errorf("Expected 1 rate limiter in manager, got %d", len(manager.limiters))
	}

	if manager.limiters[0] != rl {
		t.Error("The rate limiter in manager should be the one returned by CreateRateLimiter")
	}

	// Verify rate limiter properties
	if rl.rate != rate {
		t.Errorf("Expected rate %v, got %v", rate, rl.rate)
	}
	if rl.burst != burst {
		t.Errorf("Expected burst %d, got %d", burst, rl.burst)
	}
}

func TestRateLimiterManager_Shutdown(t *testing.T) {
	manager := NewRateLimiterManager()
	rl1 := manager.CreateRateLimiter(time.Second, 10)
	rl2 := manager.CreateRateLimiter(time.Second, 20)

	if len(manager.limiters) != 2 {
		t.Errorf("Expected 2 rate limiters, got %d", len(manager.limiters))
	}

	ctx := context.Background()
	err := manager.Shutdown(ctx)
	if err != nil {
		t.Errorf("Expected Shutdown to return nil, got %v", err)
	}

	if !rl1.IsShutdown() {
		t.Error("Expected rate limiter 1 to be shut down")
	}

	if !rl2.IsShutdown() {
		t.Error("Expected rate limiter 2 to be shut down")
	}

	if manager.limiters != nil {
		t.Error("Expected limiters slice to be nil after Shutdown")
	}
}

func TestRateLimiterManager_Shutdown_Timeout(t *testing.T) {
	manager := NewRateLimiterManager()
	_ = manager.CreateRateLimiter(time.Second, 10)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.Shutdown(ctx)
	// Current implementation returns nil because it ignores errors from individual shutdowns
	if err != nil {
		t.Errorf("Expected Shutdown to return nil even with cancelled context, got %v", err)
	}

	if manager.limiters != nil {
		t.Error("Expected limiters slice to be nil after Shutdown even if context was cancelled")
	}
}
