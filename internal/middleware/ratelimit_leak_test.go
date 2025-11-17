package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_NoGoroutineLeak(t *testing.T) {
	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	// Create and shutdown multiple rate limiters
	for i := 0; i < 100; i++ {
		rl := NewRateLimiter(100*time.Millisecond, 10)
		time.Sleep(10 * time.Millisecond)
		rl.Shutdown()
	}

	// Wait for goroutines to clean up
	runtime.GC()
	time.Sleep(500 * time.Millisecond)

	final := runtime.NumGoroutine()

	// Allow some variance (5 goroutines) but should be close to baseline
	assert.InDelta(t, baseline, final, 5,
		"Goroutine leak detected: baseline=%d, final=%d", baseline, final)
}

func TestRateLimiter_ShutdownStopsCleanup(t *testing.T) {
	rl := NewRateLimiter(50*time.Millisecond, 10)

	// Add some visitors
	assert.True(t, rl.Allow("192.168.1.1"))
	assert.True(t, rl.Allow("192.168.1.2"))

	// Verify cleanup is running by checking goroutine count before shutdown
	initialGoroutines := runtime.NumGoroutine()

	// Shutdown
	err := rl.Shutdown()
	assert.NoError(t, err)

	// Wait to ensure cleanup goroutine has stopped
	time.Sleep(200 * time.Millisecond)

	// Goroutine count should decrease after shutdown
	finalGoroutines := runtime.NumGoroutine()
	assert.Less(t, finalGoroutines, initialGoroutines, "Cleanup goroutine should have stopped")

	// Rate limiter should still work after shutdown (graceful handling)
	// It just won't clean up old visitors anymore
	assert.True(t, rl.Allow("192.168.1.3"))
}

func TestRateLimiter_ShutdownIdempotent(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 10)

	// Multiple shutdowns should be safe
	err1 := rl.Shutdown()
	err2 := rl.Shutdown()
	err3 := rl.Shutdown()

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)
}

func TestRateLimiter_ShutdownWithContext(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := rl.ShutdownWithContext(ctx)
	assert.NoError(t, err)
}

func TestRateLimiter_ShutdownWithContextTimeout(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 10)

	// Create a very short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give context time to expire
	time.Sleep(10 * time.Millisecond)

	err := rl.ShutdownWithContext(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestRateLimiter_RaceCondition(t *testing.T) {
	// Test with -race flag to ensure thread safety
	rl := NewRateLimiter(10*time.Millisecond, 5)

	done := make(chan bool)

	// Concurrent writers
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				ip := fmt.Sprintf("192.168.1.%d", id)
				rl.Allow(ip)
				time.Sleep(time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Concurrent shutdown after some time
	go func() {
		time.Sleep(50 * time.Millisecond)
		rl.Shutdown()
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 11; i++ {
		<-done
	}
}

func TestRateLimiter_CleanupActuallyRemovesOldVisitors(t *testing.T) {
	// Create a custom rate limiter with short cleanup interval for testing
	rl := NewRateLimiterWithCleanup(10*time.Millisecond, 5, 100*time.Millisecond, 200*time.Millisecond)

	// Add some visitors
	require.True(t, rl.Allow("192.168.1.1"))
	require.True(t, rl.Allow("192.168.1.2"))
	require.True(t, rl.Allow("192.168.1.3"))

	// Check initial count
	rl.mu.RLock()
	initialCount := len(rl.visitors)
	rl.mu.RUnlock()
	assert.Equal(t, 3, initialCount)

	// Wait for visitors to expire and cleanup to run
	time.Sleep(350 * time.Millisecond)

	// Visitors should be cleaned up
	rl.mu.RLock()
	finalCount := len(rl.visitors)
	rl.mu.RUnlock()
	assert.Equal(t, 0, finalCount, "Old visitors should have been cleaned up")

	// Cleanup
	rl.Shutdown()
}

func TestRateLimiter_MiddlewareFunctionAfterShutdown(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 10)

	// Shutdown the rate limiter
	err := rl.Shutdown()
	require.NoError(t, err)

	// Create a test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap with rate limiter middleware
	wrapped := rl.Limit(handler)

	// Test that requests still go through after shutdown (graceful)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}
