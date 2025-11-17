package middleware

import (
	"context"
	"sync"
	"time"
)

// RateLimiterManager manages lifecycle of all rate limiters
type RateLimiterManager struct {
	limiters []*RateLimiter
	mu       sync.Mutex
}

// NewRateLimiterManager creates a new rate limiter manager
func NewRateLimiterManager() *RateLimiterManager {
	return &RateLimiterManager{
		limiters: make([]*RateLimiter, 0),
	}
}

// CreateRateLimiter creates a new rate limiter and tracks it for cleanup
func (m *RateLimiterManager) CreateRateLimiter(rate time.Duration, burst int) *RateLimiter {
	m.mu.Lock()
	defer m.mu.Unlock()

	rl := NewRateLimiter(rate, burst)
	m.limiters = append(m.limiters, rl)
	return rl
}

// Shutdown shuts down all managed rate limiters
func (m *RateLimiterManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rl := range m.limiters {
		if err := rl.ShutdownWithContext(ctx); err != nil {
			// Log error but continue shutting down others
			// In production, you might want to aggregate errors
			continue
		}
	}

	// Clear the slice
	m.limiters = nil
	return nil
}
