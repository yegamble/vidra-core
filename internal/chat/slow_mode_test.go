package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSlowModeLimiter_AllowsFirstSendAlways(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userID := uuid.New()

	allowed, _ := l.allow(streamID, userID, 10, time.Now())
	if !allowed {
		t.Fatal("expected first send to be allowed")
	}
}

func TestSlowModeLimiter_RejectsSecondSendWithinWindow(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	if a, _ := l.allow(streamID, userID, 10, now); !a {
		t.Fatal("first send should be allowed")
	}
	allowed, nextAt := l.allow(streamID, userID, 10, now.Add(5*time.Second))
	if allowed {
		t.Fatal("second send within 10s window should be rejected")
	}
	expected := now.Add(10 * time.Second)
	if !nextAt.Equal(expected) {
		t.Fatalf("nextAllowedAt should be %v, got %v", expected, nextAt)
	}
}

func TestSlowModeLimiter_AllowsAfterWindowElapses(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	_, _ = l.allow(streamID, userID, 10, now)
	allowed, _ := l.allow(streamID, userID, 10, now.Add(11*time.Second))
	if !allowed {
		t.Fatal("send after slow_mode_seconds elapsed should be allowed")
	}
}

func TestSlowModeLimiter_DisabledWhenSecondsZero(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	for i := 0; i < 10; i++ {
		allowed, _ := l.allow(streamID, userID, 0, now)
		if !allowed {
			t.Fatalf("send #%d should be allowed when slow-mode is disabled", i)
		}
	}
}

func TestSlowModeLimiter_PerUserIndependence(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userA := uuid.New()
	userB := uuid.New()
	now := time.Now()

	if a, _ := l.allow(streamID, userA, 10, now); !a {
		t.Fatal("userA first send should be allowed")
	}
	if a, _ := l.allow(streamID, userB, 10, now); !a {
		t.Fatal("userB first send should be allowed even though userA just sent")
	}
}

func TestSlowModeLimiter_PerStreamIndependence(t *testing.T) {
	l := newSlowModeLimiter()
	userID := uuid.New()
	streamA := uuid.New()
	streamB := uuid.New()
	now := time.Now()

	if a, _ := l.allow(streamA, userID, 10, now); !a {
		t.Fatal("streamA first send should be allowed")
	}
	if a, _ := l.allow(streamB, userID, 10, now); !a {
		t.Fatal("streamB first send should be allowed (different stream from A)")
	}
}

func TestSlowModeLimiter_SweepRemovesStaleEntries(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	now := time.Now()

	// Insert 100 entries with timestamps older than the cutoff.
	for i := 0; i < 100; i++ {
		uid := uuid.New()
		l.last.Store(slowModeKey(streamID, uid), now.Add(-2*slowModeMaxEntryAge))
	}
	// And 50 fresh ones that should survive.
	freshIDs := make([]uuid.UUID, 50)
	for i := range freshIDs {
		freshIDs[i] = uuid.New()
		l.last.Store(slowModeKey(streamID, freshIDs[i]), now)
	}

	if got := l.size(); got != 150 {
		t.Fatalf("pre-sweep size: want 150 got %d", got)
	}
	l.sweep(now)
	if got := l.size(); got != 50 {
		t.Fatalf("post-sweep size: want 50 got %d", got)
	}
	for _, uid := range freshIDs {
		if _, ok := l.last.Load(slowModeKey(streamID, uid)); !ok {
			t.Fatalf("fresh entry for user %s should not be swept", uid)
		}
	}
}

func TestSlowModeLimiter_JanitorStartsAndStopsCleanly(t *testing.T) {
	l := newSlowModeLimiter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l.startJanitor(ctx, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	l.stopJanitor()
	// If janitor leaked, subsequent stopJanitor calls would block; verify idempotent:
	l.stopJanitor()
}

func TestSlowModeLimiter_ConcurrentAllowCallsDontPanic(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			l.allow(streamID, userID, 10, now.Add(time.Duration(offset)*time.Millisecond))
		}(i)
	}
	wg.Wait()
}

// TestSlowModeLimiter_PropertyMinIntervalHolds verifies the invariant: across a random
// sequence of (timestamp, slowModeSeconds) calls for one user, accepted timestamps are at
// least slowModeSeconds apart. This catches off-by-one in the elapsed-window math.
func TestSlowModeLimiter_PropertyMinIntervalHolds(t *testing.T) {
	l := newSlowModeLimiter()
	streamID := uuid.New()
	userID := uuid.New()

	// Fixed pseudo-random sequence so the test is deterministic.
	cases := []struct {
		offsetMs int
		seconds  int
	}{
		{0, 10}, {500, 10}, {1500, 10}, {9500, 10}, {10500, 10},
		{11000, 5}, {15000, 5}, {15500, 5}, {20000, 5}, {25000, 30},
		{40000, 30}, {55000, 30}, {56000, 30},
	}
	base := time.Now()
	var lastAccepted time.Time
	for _, c := range cases {
		now := base.Add(time.Duration(c.offsetMs) * time.Millisecond)
		allowed, _ := l.allow(streamID, userID, c.seconds, now)
		if !allowed {
			continue
		}
		// Invariant: when a send is accepted with current seconds = N, the gap from the most
		// recent prior accept is >= N. (The window is computed using the CURRENT slow-mode
		// value at request time — when slow-mode is reduced, the lockout shrinks accordingly.)
		if !lastAccepted.IsZero() && c.seconds > 0 {
			gap := now.Sub(lastAccepted)
			if gap < time.Duration(c.seconds)*time.Second {
				t.Fatalf("invariant violated: accepted gap %v < %ds at offset=%dms",
					gap, c.seconds, c.offsetMs)
			}
		}
		lastAccepted = now
	}
}
