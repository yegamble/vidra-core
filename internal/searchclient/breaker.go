package searchclient

import (
	"sync"
	"time"
)

// breaker is a minimal per-endpoint-group circuit breaker (MASTER-PLAN §2.4):
// it opens after breakerThreshold consecutive transport/5xx failures and, once
// open, refuses calls until breakerCooldown has elapsed, then admits a single
// half-open probe. A success (any 2xx or a service-level 4xx — the service is up,
// it just rejected the request) closes it and resets the failure count.
type breaker struct {
	mu        sync.Mutex
	failures  int
	openedAt  time.Time
	halfOpen  bool
	now       func() time.Time
	threshold int
	cooldown  time.Duration
}

func newBreaker(now func() time.Time) *breaker {
	return &breaker{now: now, threshold: breakerThreshold, cooldown: breakerCooldown}
}

const (
	// breakerThreshold is the consecutive-failure count that opens a group.
	breakerThreshold = 5
	// breakerCooldown is how long an open breaker waits before a half-open probe.
	breakerCooldown = 30 * time.Second
)

// allow reports whether a call may proceed. When the breaker is open and the
// cooldown has elapsed it admits exactly one half-open probe (blocking further
// calls until that probe reports back via success/failure).
func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.threshold {
		return true // closed
	}
	if b.halfOpen {
		return false // a probe is already in flight
	}
	if b.now().Sub(b.openedAt) >= b.cooldown {
		b.halfOpen = true
		return true // admit the probe
	}
	return false // still open, cooling down
}

// success closes the breaker.
func (b *breaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.halfOpen = false
	b.mu.Unlock()
}

// failure records a transport/5xx failure, opening the breaker at the threshold.
// A failed half-open probe re-opens the cooldown window.
func (b *breaker) failure() {
	b.mu.Lock()
	if b.halfOpen {
		b.halfOpen = false
		b.openedAt = b.now()
		b.mu.Unlock()
		return
	}
	b.failures++
	if b.failures == b.threshold {
		b.openedAt = b.now()
	}
	b.mu.Unlock()
}

// isOpen reports whether the breaker is currently refusing calls (for metrics/
// tests). It does not admit a probe.
func (b *breaker) isOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.threshold {
		return false
	}
	if b.halfOpen {
		return true
	}
	return b.now().Sub(b.openedAt) < b.cooldown
}
