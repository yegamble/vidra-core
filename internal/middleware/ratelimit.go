package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// visitor tracks the rate limiting state for a single IP
type visitor struct {
	lastSeen time.Time
	count    int
	window   time.Time
}

// RateLimiter implements a simple rate limiting mechanism with automatic cleanup
type RateLimiter struct {
	visitors       map[string]*visitor
	mu             sync.RWMutex
	rate           time.Duration
	burst          int
	done           chan struct{}  // Shutdown signal
	wg             sync.WaitGroup // Wait for cleanup to finish
	shutdownOnce   sync.Once      // Ensure shutdown is idempotent
	cleanupPeriod  time.Duration  // How often to run cleanup
	visitorTimeout time.Duration  // How long to keep idle visitors
	isShutdown     atomic.Bool    // Track shutdown state
}

// NewRateLimiter creates a new rate limiter with default cleanup settings
func NewRateLimiter(rate time.Duration, burst int) *RateLimiter {
	return NewRateLimiterWithCleanup(rate, burst, time.Minute, 3*time.Minute)
}

// NewRateLimiterWithCleanup creates a rate limiter with custom cleanup settings
func NewRateLimiterWithCleanup(rate time.Duration, burst int, cleanupPeriod, visitorTimeout time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors:       make(map[string]*visitor),
		rate:           rate,
		burst:          burst,
		done:           make(chan struct{}),
		cleanupPeriod:  cleanupPeriod,
		visitorTimeout: visitorTimeout,
	}

	rl.wg.Add(1)
	go rl.cleanupVisitors()

	return rl
}

// cleanupVisitors removes old visitor entries to prevent memory leaks
func (rl *RateLimiter) cleanupVisitors() {
	defer rl.wg.Done()

	ticker := time.NewTicker(rl.cleanupPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			// Shutdown signal received
			return
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, v := range rl.visitors {
				if now.Sub(v.lastSeen) > rl.visitorTimeout {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Allow checks if a request from the given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	now := time.Now()

	if !exists {
		rl.visitors[ip] = &visitor{
			lastSeen: now,
			count:    1,
			window:   now,
		}
		return true
	}

	// Reset the window if rate duration has passed
	if now.Sub(v.window) > rl.rate {
		v.count = 1
		v.window = now
	} else {
		v.count++
	}

	v.lastSeen = now

	return v.count <= rl.burst
}

// GetVisitor returns visitor stats for monitoring (read-only)
func (rl *RateLimiter) GetVisitor(ip string) (count int, windowStart time.Time, exists bool) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	v, exists := rl.visitors[ip]
	if !exists {
		return 0, time.Time{}, false
	}

	return v.count, v.window, true
}

// Shutdown gracefully stops the rate limiter
func (rl *RateLimiter) Shutdown() error {
	return rl.ShutdownWithContext(context.Background())
}

// ShutdownWithContext gracefully stops the rate limiter with context timeout
func (rl *RateLimiter) ShutdownWithContext(ctx context.Context) error {
	var err error

	rl.shutdownOnce.Do(func() {
		// Mark as shutdown
		rl.isShutdown.Store(true)

		// Send shutdown signal
		close(rl.done)

		// Wait for cleanup goroutine to finish with context timeout
		done := make(chan struct{})
		go func() {
			rl.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Cleanup finished successfully
		case <-ctx.Done():
			// Context timeout/cancelled
			err = ctx.Err()
		}
	})

	return err
}

// IsShutdown returns whether the rate limiter has been shut down
func (rl *RateLimiter) IsShutdown() bool {
	return rl.isShutdown.Load()
}

// Limit returns an HTTP middleware that applies rate limiting
func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If shutting down, allow all requests through (graceful shutdown)
		if rl.IsShutdown() {
			next.ServeHTTP(w, r)
			return
		}

		// Extract IP address
		ip := extractIP(r)

		// Check rate limit
		if !rl.Allow(ip) {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractIP gets the real IP address from the request
func extractIP(r *http.Request) string {
	// Try X-Real-IP header first (single proxy)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Try X-Forwarded-For header (multiple proxies)
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if comma := strings.Index(ip, ","); comma != -1 {
			return strings.TrimSpace(ip[:comma])
		}
		return ip
	}

	// Fallback to RemoteAddr
	// RemoteAddr includes port, so we need to extract just the IP
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	// If SplitHostPort fails, return RemoteAddr as-is
	return r.RemoteAddr
}

// RateLimit creates a new rate limiter middleware (convenience function)
func RateLimit(rate time.Duration, burst int) func(http.Handler) http.Handler {
	rl := NewRateLimiter(rate, burst)

	return func(next http.Handler) http.Handler {
		return rl.Limit(next)
	}
}

// Stats returns current rate limiter statistics
type Stats struct {
	VisitorCount int
	IsShutdown   bool
}

// GetStats returns current statistics about the rate limiter
func (rl *RateLimiter) GetStats() Stats {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	return Stats{
		VisitorCount: len(rl.visitors),
		IsShutdown:   rl.IsShutdown(),
	}
}
