package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeCounter is an in-memory fixed-window counter for deterministic tests.
type fakeCounter struct {
	counts map[string]int64
	ttl    time.Duration
	err    error
}

func newFakeCounter(ttl time.Duration) *fakeCounter {
	return &fakeCounter{counts: map[string]int64{}, ttl: ttl}
}

func (f *fakeCounter) Incr(_ context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	f.counts[key]++
	if f.ttl == 0 {
		return f.counts[key], window, nil
	}
	return f.counts[key], f.ttl, nil
}

func TestLimiterAllowsUpToLimitThenDenies(t *testing.T) {
	l := NewLimiter(newFakeCounter(30*time.Second), 3, time.Minute)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		res, err := l.Allow(ctx, "ip:1.2.3.4")
		if err != nil {
			t.Fatalf("Allow #%d error: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("request #%d denied, want allowed", i)
		}
		if res.Remaining != 3-i {
			t.Errorf("request #%d remaining = %d, want %d", i, res.Remaining, 3-i)
		}
	}

	res, err := l.Allow(ctx, "ip:1.2.3.4")
	if err != nil {
		t.Fatalf("Allow #4 error: %v", err)
	}
	if res.Allowed {
		t.Error("request #4 allowed, want denied")
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d, want 0", res.Remaining)
	}
	if res.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", res.RetryAfter)
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiter(newFakeCounter(time.Minute), 1, time.Minute)
	ctx := context.Background()

	if res, _ := l.Allow(ctx, "a"); !res.Allowed {
		t.Fatal("first key, first request should be allowed")
	}
	if res, _ := l.Allow(ctx, "a"); res.Allowed {
		t.Fatal("first key, second request should be denied")
	}
	if res, _ := l.Allow(ctx, "b"); !res.Allowed {
		t.Fatal("second key, first request should be allowed (independent budget)")
	}
}

func TestLimiterPropagatesCounterError(t *testing.T) {
	fc := newFakeCounter(time.Minute)
	fc.err = errors.New("redis down")
	l := NewLimiter(fc, 5, time.Minute)

	if _, err := l.Allow(context.Background(), "k"); err == nil {
		t.Fatal("expected counter error to propagate, got nil")
	}
}

func TestResultReportsLimitAndReset(t *testing.T) {
	l := NewLimiter(newFakeCounter(45*time.Second), 10, time.Minute)
	res, err := l.Allow(context.Background(), "k")
	if err != nil {
		t.Fatalf("Allow error: %v", err)
	}
	if res.Limit != 10 {
		t.Errorf("Limit = %d, want 10", res.Limit)
	}
	if res.Reset != 45*time.Second {
		t.Errorf("Reset = %v, want 45s", res.Reset)
	}
}
