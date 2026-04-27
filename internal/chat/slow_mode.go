package chat

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// slowModeLimiter tracks the most recent send time per (streamID, userID) pair so the chat
// hub can reject messages that arrive within the configured slow-mode window. State is
// in-memory and lost on process restart — fine because slow-mode is a UX guardrail, not a
// security boundary, and bans (which ARE persisted) handle the actual abuse case.
//
// A background janitor sweeps stale entries on a fixed interval. sync.Map has no TTL
// primitive, so the janitor is the only thing that prevents unbounded growth in a long-lived
// process serving many streams.
type slowModeLimiter struct {
	// last maps "streamID:userID" → time.Time of most recent accepted send.
	last sync.Map

	janitorOnce sync.Once
	janitorStop context.CancelFunc
	janitorWG   sync.WaitGroup
}

const (
	// slowModeJanitorInterval is how often the janitor sweeps. Long enough that the sweep
	// itself isn't measurable on the hot path; short enough that idle entries don't pile up.
	slowModeJanitorInterval = 5 * time.Minute

	// slowModeMaxEntryAge is the cutoff: entries with last-send older than this are removed.
	// Set to 1 hour, comfortably above any realistic slow-mode window (the handler caps at
	// 600 seconds), so a returning user's prior timer doesn't disappear within their session.
	slowModeMaxEntryAge = time.Hour
)

func newSlowModeLimiter() *slowModeLimiter {
	return &slowModeLimiter{}
}

// startJanitor launches the sweep goroutine. Idempotent — only the first call has effect.
// The caller cancels via stopJanitor; the goroutine cooperatively exits on ctx.Done.
func (l *slowModeLimiter) startJanitor(parent context.Context, interval time.Duration) {
	l.janitorOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		l.janitorStop = cancel
		l.janitorWG.Add(1)
		go l.runJanitor(ctx, interval)
	})
}

// stopJanitor cancels the janitor and waits for it to drain. Safe to call when the janitor
// was never started — it's a no-op.
func (l *slowModeLimiter) stopJanitor() {
	if l.janitorStop != nil {
		l.janitorStop()
	}
	l.janitorWG.Wait()
}

func (l *slowModeLimiter) runJanitor(ctx context.Context, interval time.Duration) {
	defer l.janitorWG.Done()
	if interval <= 0 {
		interval = slowModeJanitorInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.sweep(time.Now())
		}
	}
}

// sweep removes entries older than slowModeMaxEntryAge. Exposed for tests; production callers
// go through the janitor.
func (l *slowModeLimiter) sweep(now time.Time) {
	cutoff := now.Add(-slowModeMaxEntryAge)
	l.last.Range(func(key, value any) bool {
		if t, ok := value.(time.Time); ok && t.Before(cutoff) {
			l.last.Delete(key)
		}
		return true
	})
}

// allow returns true when the user is permitted to send a message right now. When false,
// nextAllowedAt indicates when the next send will be allowed (epoch time). slowModeSeconds=0
// disables enforcement and always returns true. The function is the SOLE place we mutate
// last[key] for accepted sends.
func (l *slowModeLimiter) allow(streamID, userID uuid.UUID, slowModeSeconds int, now time.Time) (allowed bool, nextAllowedAt time.Time) {
	if slowModeSeconds <= 0 {
		l.last.Store(slowModeKey(streamID, userID), now)
		return true, time.Time{}
	}

	key := slowModeKey(streamID, userID)
	if v, ok := l.last.Load(key); ok {
		if last, isTime := v.(time.Time); isTime {
			windowEnd := last.Add(time.Duration(slowModeSeconds) * time.Second)
			if now.Before(windowEnd) {
				return false, windowEnd
			}
		}
	}
	l.last.Store(key, now)
	return true, time.Time{}
}

// reset removes the cached last-send for a single (stream, user) — used by tests and could be
// used by a future "exempt this user" feature.
func (l *slowModeLimiter) reset(streamID, userID uuid.UUID) {
	l.last.Delete(slowModeKey(streamID, userID))
}

// size returns the current number of tracked entries. For tests and metrics.
func (l *slowModeLimiter) size() int {
	count := 0
	l.last.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func slowModeKey(streamID, userID uuid.UUID) string {
	return streamID.String() + ":" + userID.String()
}
