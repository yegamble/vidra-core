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

type visitor struct {
	lastSeen time.Time
	count    int
	window   time.Time
}

type RateLimiter struct {
	visitors       map[string]*visitor
	mu             sync.RWMutex
	rate           time.Duration
	burst          int
	done           chan struct{}
	wg             sync.WaitGroup
	shutdownOnce   sync.Once
	cleanupPeriod  time.Duration
	visitorTimeout time.Duration
	isShutdown     atomic.Bool
}

func NewRateLimiter(rate time.Duration, burst int) *RateLimiter {
	return NewRateLimiterWithCleanup(rate, burst, time.Minute, 3*time.Minute)
}

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

func (rl *RateLimiter) cleanupVisitors() {
	defer rl.wg.Done()

	ticker := time.NewTicker(rl.cleanupPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
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

	if now.Sub(v.window) > rl.rate {
		v.count = 1
		v.window = now
	} else {
		v.count++
	}

	v.lastSeen = now

	return v.count <= rl.burst
}

func (rl *RateLimiter) GetVisitor(ip string) (count int, windowStart time.Time, exists bool) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	v, exists := rl.visitors[ip]
	if !exists {
		return 0, time.Time{}, false
	}

	return v.count, v.window, true
}

func (rl *RateLimiter) Shutdown() error {
	return rl.ShutdownWithContext(context.Background())
}

func (rl *RateLimiter) ShutdownWithContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var err error

	rl.shutdownOnce.Do(func() {
		rl.isShutdown.Store(true)

		close(rl.done)

		done := make(chan struct{})
		go func() {
			rl.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})

	return err
}

func (rl *RateLimiter) IsShutdown() bool {
	return rl.isShutdown.Load()
}

func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl.IsShutdown() {
			next.ServeHTTP(w, r)
			return
		}

		ip := extractIP(r)

		if !rl.Allow(ip) {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func extractIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		if comma := strings.Index(ip, ","); comma != -1 {
			return strings.TrimSpace(ip[:comma])
		}
		return ip
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

func RateLimit(rate time.Duration, burst int) *RateLimiter {
	return NewRateLimiter(rate, burst)
}

type Stats struct {
	VisitorCount int
	IsShutdown   bool
}

func (rl *RateLimiter) GetStats() Stats {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	return Stats{
		VisitorCount: len(rl.visitors),
		IsShutdown:   rl.IsShutdown(),
	}
}
