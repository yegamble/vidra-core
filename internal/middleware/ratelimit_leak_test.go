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
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < 100; i++ {
		rl := NewRateLimiter(100*time.Millisecond, 10)
		time.Sleep(10 * time.Millisecond)
		rl.Shutdown()
	}

	runtime.GC()

	require.Eventually(t, func() bool {
		final := runtime.NumGoroutine()
		return float64(final) >= float64(baseline)-5 && float64(final) <= float64(baseline)+5
	}, 1*time.Second, 10*time.Millisecond, "Goroutine leak detected: baseline=%d", baseline)
}

func TestRateLimiter_ShutdownStopsCleanup(t *testing.T) {
	rl := NewRateLimiter(50*time.Millisecond, 10)

	assert.True(t, rl.Allow("192.168.1.1"))
	assert.True(t, rl.Allow("192.168.1.2"))

	initialGoroutines := runtime.NumGoroutine()

	err := rl.Shutdown()
	assert.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	assert.Less(t, finalGoroutines, initialGoroutines, "Cleanup goroutine should have stopped")

	assert.True(t, rl.Allow("192.168.1.3"))
}

func TestRateLimiter_ShutdownIdempotent(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 10)

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

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond)

	err := rl.ShutdownWithContext(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
}

func TestRateLimiter_RaceCondition(t *testing.T) {
	rl := NewRateLimiter(10*time.Millisecond, 5)

	done := make(chan bool)

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

	go func() {
		time.Sleep(50 * time.Millisecond)
		rl.Shutdown()
		done <- true
	}()

	for i := 0; i < 11; i++ {
		<-done
	}
}

func TestRateLimiter_CleanupActuallyRemovesOldVisitors(t *testing.T) {
	rl := NewRateLimiterWithCleanup(10*time.Millisecond, 5, 100*time.Millisecond, 200*time.Millisecond)

	require.True(t, rl.Allow("192.168.1.1"))
	require.True(t, rl.Allow("192.168.1.2"))
	require.True(t, rl.Allow("192.168.1.3"))

	rl.mu.RLock()
	initialCount := len(rl.visitors)
	rl.mu.RUnlock()
	assert.Equal(t, 3, initialCount)

	require.Eventually(t, func() bool {
		rl.mu.RLock()
		finalCount := len(rl.visitors)
		rl.mu.RUnlock()
		return finalCount == 0
	}, 700*time.Millisecond, 10*time.Millisecond, "Old visitors should have been cleaned up")

	rl.Shutdown()
}

func TestRateLimiter_MiddlewareFunctionAfterShutdown(t *testing.T) {
	rl := NewRateLimiter(100*time.Millisecond, 10)

	err := rl.Shutdown()
	require.NoError(t, err)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := rl.Limit(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}
