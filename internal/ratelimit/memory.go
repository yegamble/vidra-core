package ratelimit

import (
	"context"
	"sync"
	"time"
)

// MemoryCounter is a process-local, in-memory fixed-window Counter. It is the
// fallback store used when no Redis-backed Counter is configured, so a sensitive
// surface (e.g. the video-unlock password-guessing endpoint) still gets a real
// limiter on a single-node deployment that runs without Redis.
//
// It is NOT shared across processes: on a multi-node deployment each node keeps
// its own budget, so configure the Redis counter there for a shared window.
// Memory is bounded by an opportunistic sweep of expired keys (at most once per
// window), so a churn of distinct keys cannot grow the map without limit.
type MemoryCounter struct {
	mu        sync.Mutex
	entries   map[string]memEntry
	lastSweep time.Time
	now       func() time.Time // injectable for deterministic tests
}

// memEntry is one key's current fixed-window count and its expiry.
type memEntry struct {
	count   int64
	expires time.Time
}

// NewMemoryCounter builds an empty in-memory fixed-window counter.
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{entries: make(map[string]memEntry), now: time.Now}
}

// Incr implements Counter. The first Incr for a key (or the first after its
// window elapsed) starts a fresh window of length window; subsequent Incrs
// within that window increment the count and report the remaining TTL. It never
// returns an error — an in-memory map cannot fail — so a MemoryCounter-backed
// limiter never exercises the caller's fail-open path.
func (m *MemoryCounter) Incr(_ context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()

	// Opportunistic sweep: drop expired keys at most once per window so a churn
	// of distinct client IPs cannot grow the map without bound.
	if now.Sub(m.lastSweep) >= window {
		for k, e := range m.entries {
			if !now.Before(e.expires) {
				delete(m.entries, k)
			}
		}
		m.lastSweep = now
	}

	e, ok := m.entries[key]
	if !ok || !now.Before(e.expires) {
		// New key, or its window has elapsed: start a fresh window.
		e = memEntry{expires: now.Add(window)}
	}
	e.count++
	m.entries[key] = e
	return e.count, e.expires.Sub(now), nil
}
