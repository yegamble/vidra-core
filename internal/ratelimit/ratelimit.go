// Package ratelimit provides a fixed-window rate limiter backed by a pluggable
// counter. The Redis-backed counter (see redis.go) is the production store;
// tests use an in-memory fake to exercise the windowing logic deterministically.
//
// Fixed window: each key accumulates a count that expires after the window. The
// (N+1)-th request inside a window is denied until the key expires and the
// count resets. This is simple, cheap, and good enough for abuse protection on
// auth/upload/search/federation endpoints; per-route limits can layer on later.
package ratelimit

import (
	"context"
	"time"
)

// Counter increments a per-key counter that resets after a window. Implementations
// must set the expiry only when the key is first created within a window, so the
// window has a fixed start (a refreshed TTL on every call would never reset).
type Counter interface {
	// Incr increments the counter at key (creating it with the given window TTL
	// on first use) and returns the post-increment count and remaining TTL.
	Incr(ctx context.Context, key string, window time.Duration) (count int64, ttl time.Duration, err error)
}

// Limiter applies a fixed request budget per window using a Counter.
type Limiter struct {
	counter Counter
	limit   int
	window  time.Duration
}

// NewLimiter builds a Limiter allowing limit requests per window. limit must be
// > 0 and window > 0; callers should guard configuration before constructing.
func NewLimiter(counter Counter, limit int, window time.Duration) *Limiter {
	return &Limiter{counter: counter, limit: limit, window: window}
}

// Limit reports the configured request budget per window.
func (l *Limiter) Limit() int { return l.limit }

// Result describes the outcome of an Allow check.
type Result struct {
	// Allowed is true when the request is within budget.
	Allowed bool
	// Limit is the configured budget per window.
	Limit int
	// Remaining is the budget left in the current window (never negative).
	Remaining int
	// Reset is the time until the current window expires.
	Reset time.Duration
	// RetryAfter is how long the client should wait before retrying. Zero when
	// the request is allowed.
	RetryAfter time.Duration
}

// Allow records one request against key and reports whether it is within budget.
// A counter error is returned to the caller, which decides the fail-open vs
// fail-closed policy.
func (l *Limiter) Allow(ctx context.Context, key string) (Result, error) {
	count, ttl, err := l.counter.Incr(ctx, key, l.window)
	if err != nil {
		return Result{}, err
	}
	if ttl <= 0 {
		ttl = l.window
	}

	res := Result{Limit: l.limit, Reset: ttl}
	if remaining := l.limit - int(count); remaining > 0 {
		res.Remaining = remaining
	}
	if int(count) <= l.limit {
		res.Allowed = true
	} else {
		res.RetryAfter = ttl
	}
	return res, nil
}
