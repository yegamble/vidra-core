package ratelimit

import (
	"context"
	"testing"
	"time"
)

// TestMemoryCounterFixedWindow proves the in-memory counter accumulates within a
// window and resets once the window elapses (fixed-window semantics), driving a
// Limiter exactly like the Redis counter does.
func TestMemoryCounterFixedWindow(t *testing.T) {
	now := time.Unix(0, 0)
	mc := NewMemoryCounter()
	mc.now = func() time.Time { return now }
	l := NewLimiter(mc, 2, time.Minute)
	ctx := context.Background()

	// Two within budget, third denied — all inside the same window.
	for i := 1; i <= 2; i++ {
		res, err := l.Allow(ctx, "auth:ip")
		if err != nil || !res.Allowed {
			t.Fatalf("attempt #%d allowed=%v err=%v, want allowed", i, res.Allowed, err)
		}
	}
	if res, _ := l.Allow(ctx, "auth:ip"); res.Allowed {
		t.Fatal("attempt #3 allowed, want denied")
	}

	// A different key has its own budget.
	if res, _ := l.Allow(ctx, "auth:other"); !res.Allowed {
		t.Fatal("a distinct key must not share the budget")
	}

	// After the window elapses the count resets.
	now = now.Add(time.Minute + time.Second)
	if res, _ := l.Allow(ctx, "auth:ip"); !res.Allowed {
		t.Fatal("after the window elapsed the budget must reset")
	}
}

// TestMemoryCounterSweepsExpiredKeys proves the opportunistic sweep drops expired
// entries so a churn of distinct keys cannot grow the map without bound.
func TestMemoryCounterSweepsExpiredKeys(t *testing.T) {
	now := time.Unix(0, 0)
	mc := NewMemoryCounter()
	mc.now = func() time.Time { return now }
	ctx := context.Background()

	if _, _, err := mc.Incr(ctx, "a", time.Minute); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	// Advance past the window so "a" is expired, then touch a new key: the sweep
	// (at most once per window) must evict "a".
	now = now.Add(time.Minute + time.Second)
	if _, _, err := mc.Incr(ctx, "b", time.Minute); err != nil {
		t.Fatalf("Incr: %v", err)
	}
	mc.mu.Lock()
	_, stillThere := mc.entries["a"]
	n := len(mc.entries)
	mc.mu.Unlock()
	if stillThere {
		t.Error("expired key \"a\" was not swept")
	}
	if n != 1 {
		t.Errorf("entries = %d, want 1 (only the live key)", n)
	}
}
