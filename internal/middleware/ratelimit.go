package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     time.Duration
	burst    int
}

type visitor struct {
	lastSeen time.Time
	count    int
	window   time.Time
}

func NewRateLimiter(rate time.Duration, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		burst:    burst,
	}

	go rl.cleanupVisitors()

	return rl
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

func (rl *RateLimiter) cleanupVisitors() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// RateLimit applies IP-based rate limiting using only r.RemoteAddr for client identity.
// This is the safer default when the server is not behind a trusted proxy.
func RateLimit(rate time.Duration, burst int) func(http.Handler) http.Handler {
	return RateLimitWithTrust(rate, burst, false)
}

// RateLimitWithTrust allows optionally trusting proxy headers (X-Forwarded-For, X-Real-IP).
// When trustForwarded is true, the first IP from X-Forwarded-For (or X-Real-IP) is used.
// Otherwise, only r.RemoteAddr is used.
func RateLimitWithTrust(rate time.Duration, burst int, trustForwarded bool) func(http.Handler) http.Handler {
	rl := NewRateLimiter(rate, burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trustForwarded)
			if !rl.Allow(ip) {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		// Prefer the first X-Forwarded-For IP when present
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The first IP in the list is the original client
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				ip := strings.TrimSpace(parts[0])
				if ip != "" {
					return ip
				}
			}
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	// Fallback: use RemoteAddr (strip port if present)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
