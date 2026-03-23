package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryRateLimiter_AllowsUpToMax(t *testing.T) {
	rl := NewMemoryRateLimiter()

	for i := 0; i < rateLimitMax; i++ {
		assert.True(t, rl.CheckRateLimit("192.168.1.1"), "Request %d should be allowed", i+1)
	}
}

func TestMemoryRateLimiter_BlocksExcessRequests(t *testing.T) {
	rl := NewMemoryRateLimiter()

	// Use up the limit
	for i := 0; i < rateLimitMax; i++ {
		rl.CheckRateLimit("192.168.1.1")
	}

	// Next request should be blocked
	assert.False(t, rl.CheckRateLimit("192.168.1.1"), "4th request should be blocked")
}

func TestMemoryRateLimiter_IndependentPerIP(t *testing.T) {
	rl := NewMemoryRateLimiter()

	// Exhaust limit for IP 1
	for i := 0; i < rateLimitMax+1; i++ {
		rl.CheckRateLimit("192.168.1.1")
	}

	// IP 2 should still be allowed
	assert.True(t, rl.CheckRateLimit("192.168.1.2"), "Different IP should have independent limit")
}

func TestMemoryRateLimiter_CleansUpLargeMap(t *testing.T) {
	rl := NewMemoryRateLimiter().(*memoryRateLimiter)

	// Fill map beyond capacity
	for i := 0; i < rateLimitMapCap+10; i++ {
		rl.requests[string(rune(i))] = []int64{1}
	}

	// Next check should trigger cleanup
	assert.True(t, rl.CheckRateLimit("new-ip"), "Request after cleanup should be allowed")
	// Map should have been cleared (only the new entry remains)
	assert.LessOrEqual(t, len(rl.requests), rateLimitMapCap)
}

func TestNewRedisRateLimiter_NilClientFallsBack(t *testing.T) {
	rl := NewRedisRateLimiter(nil)

	// Should work as a memory rate limiter
	for i := 0; i < rateLimitMax; i++ {
		assert.True(t, rl.CheckRateLimit("192.168.1.1"))
	}
	assert.False(t, rl.CheckRateLimit("192.168.1.1"), "Should be rate limited even with nil Redis")
}

func TestRateLimiterInterface(t *testing.T) {
	// Verify both implementations satisfy the interface
	var _ RateLimiter = NewMemoryRateLimiter()
	var _ RateLimiter = NewRedisRateLimiter(nil)
}
