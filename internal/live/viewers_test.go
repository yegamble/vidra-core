package live

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// memViewerStore is a sorted set in a map: it applies the same window semantics
// Redis would, so the test asserts the POLICY (the window, the cache, the
// degradation) rather than a mock's call log.
type memViewerStore struct {
	sets    map[string]map[string]int64
	ttl     map[string]time.Duration
	failNow error
	counts  int
}

func newMemViewerStore() *memViewerStore {
	return &memViewerStore{sets: map[string]map[string]int64{}, ttl: map[string]time.Duration{}}
}

func (m *memViewerStore) Seen(_ context.Context, key, member string, now, cutoff int64, ttl time.Duration) error {
	if m.failNow != nil {
		return m.failNow
	}
	if m.sets[key] == nil {
		m.sets[key] = map[string]int64{}
	}
	m.sets[key][member] = now
	for k, score := range m.sets[key] {
		if score < cutoff {
			delete(m.sets[key], k)
		}
	}
	m.ttl[key] = ttl
	return nil
}

func (m *memViewerStore) CountSince(_ context.Context, key string, cutoff int64) (int64, error) {
	m.counts++
	if m.failNow != nil {
		return 0, m.failNow
	}
	var n int64
	for _, score := range m.sets[key] {
		if score >= cutoff {
			n++
		}
	}
	return n, nil
}

func (m *memViewerStore) Drop(_ context.Context, key string) error {
	delete(m.sets, key)
	delete(m.ttl, key)
	return nil
}

// TestViewerCountIsDistinctSessionsInTheWindow: the same viewer refetching the
// playlist every two seconds is ONE viewer, and two viewers are two.
func TestViewerCountIsDistinctSessionsInTheWindow(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c.SetNowFunc(func() time.Time { return now })
	id := uuid.New()
	ctx := context.Background()

	// One player, many playlist fetches.
	for i := 0; i < 20; i++ {
		if err := c.Touch(ctx, id, "digest-a"); err != nil {
			t.Fatalf("touch: %v", err)
		}
		now = now.Add(2 * time.Second)
		c.SetNowFunc(func() time.Time { return now })
	}
	if n, ok := c.Count(ctx, id); !ok || n != 1 {
		t.Fatalf("count = %d (known=%v), want 1 — a player refetching its playlist is one viewer, not twenty", n, ok)
	}

	// A second viewer joins. The cache must not hide them for its whole TTL, so
	// step past the refresh interval.
	if err := c.Touch(ctx, id, "digest-b"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	now = now.Add(viewerCountRefresh + time.Second)
	c.SetNowFunc(func() time.Time { return now })
	if n, _ := c.Count(ctx, id); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

// TestViewerCountForgetsAViewerAfterTheWindow: a player that stops fetching
// leaves the count, on its own, with nothing sweeping.
func TestViewerCountForgetsAViewerAfterTheWindow(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c.SetNowFunc(func() time.Time { return now })
	id := uuid.New()
	ctx := context.Background()

	_ = c.Touch(ctx, id, "leaver")
	_ = c.Touch(ctx, id, "stayer")

	now = now.Add(viewerWindow + time.Second)
	c.SetNowFunc(func() time.Time { return now })
	_ = c.Touch(ctx, id, "stayer") // still watching

	n, ok := c.Count(ctx, id)
	if !ok || n != 1 {
		t.Errorf("count = %d (known=%v), want 1 — a viewer who stopped fetching %s ago is not watching", n, ok, viewerWindow)
	}
}

// TestViewerCountUnknownWithoutAStore is the "absent, not zero" rule: a nil
// counter must report UNKNOWN so the projection omits the field rather than
// telling a creator mid-broadcast that nobody is watching.
func TestViewerCountUnknownWithoutAStore(t *testing.T) {
	var c *ViewerCounter
	if n, ok := c.Count(context.Background(), uuid.New()); ok || n != 0 {
		t.Errorf("nil counter answered (%d, %v); it must report unknown", n, ok)
	}
	if err := c.Touch(context.Background(), uuid.New(), "d"); err != nil {
		t.Errorf("nil counter Touch returned %v; it must be a no-op", err)
	}
	c.Reset(context.Background(), uuid.New()) // must not panic
	if NewViewerCounter(nil) != nil {
		t.Error("a counter was built with no Redis client")
	}
}

// TestViewerCountServesTheLastAnswerThroughAnOutage: a count that vanishes off a
// creator's page and comes back is worse than one that is a few seconds stale.
func TestViewerCountServesTheLastAnswerThroughAnOutage(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c.SetNowFunc(func() time.Time { return now })
	id := uuid.New()
	ctx := context.Background()

	_ = c.Touch(ctx, id, "a")
	_ = c.Touch(ctx, id, "b")
	if n, _ := c.Count(ctx, id); n != 2 {
		t.Fatalf("setup count = %d, want 2", n)
	}

	store.failNow = errors.New("redis is gone")
	now = now.Add(viewerCountRefresh + time.Second)
	c.SetNowFunc(func() time.Time { return now })
	n, ok := c.Count(ctx, id)
	if !ok || n != 2 {
		t.Errorf("during an outage count = %d (known=%v), want the last known 2", n, ok)
	}

	// Far enough past the key's own TTL and the stale answer is no longer
	// defensible: unknown, not a number from a broadcast that may have ended.
	now = now.Add(viewerKeyTTL + time.Minute)
	c.SetNowFunc(func() time.Time { return now })
	if _, ok := c.Count(ctx, id); ok {
		t.Error("a long-dead cached count was still served as known")
	}
}

// TestViewerCountIsCachedForTheRefreshInterval bounds the read cost: the public
// rail renders many cards per request and must not pay a round trip per card.
func TestViewerCountIsCachedForTheRefreshInterval(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c.SetNowFunc(func() time.Time { return now })
	id := uuid.New()
	ctx := context.Background()
	_ = c.Touch(ctx, id, "a")

	for i := 0; i < 50; i++ {
		c.Count(ctx, id)
	}
	if store.counts != 1 {
		t.Errorf("%d store reads for 50 renders inside one %s window, want 1", store.counts, viewerCountRefresh)
	}
	now = now.Add(viewerCountRefresh + time.Second)
	c.SetNowFunc(func() time.Time { return now })
	c.Count(ctx, id)
	if store.counts != 2 {
		t.Errorf("%d store reads after the window elapsed, want 2 — the cache never refreshed", store.counts)
	}
}

// TestResetClearsAStreamsViewers: a PERMANENT stream going live again inside the
// window must not open on the previous session's tail.
func TestResetClearsAStreamsViewers(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c.SetNowFunc(func() time.Time { return now })
	id := uuid.New()
	ctx := context.Background()
	_ = c.Touch(ctx, id, "a")
	_ = c.Touch(ctx, id, "b")
	c.Reset(ctx, id)
	if n, _ := c.Count(ctx, id); n != 0 {
		t.Errorf("count after reset = %d, want 0 — a new broadcast inherited the last session's viewers", n)
	}
}

// TestTouchIgnoresAnEmptyDigest: an unkeyed instance must not collapse every
// viewer into one shared set member, which would make the count read 1 forever.
func TestTouchIgnoresAnEmptyDigest(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	id := uuid.New()
	if err := c.Touch(context.Background(), id, ""); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if n, _ := c.Count(context.Background(), id); n != 0 {
		t.Errorf("count = %d, want 0 — an empty digest was counted as a viewer", n)
	}
}

// TestTouchArmsTheKeyTTL is the memory bound: a stream nobody terminates
// cleanly must still evict itself.
func TestTouchArmsTheKeyTTL(t *testing.T) {
	store := newMemViewerStore()
	c := newViewerCounter(store)
	id := uuid.New()
	_ = c.Touch(context.Background(), id, "a")
	if got := store.ttl[viewerSetKey(id)]; got != viewerKeyTTL {
		t.Errorf("key TTL = %s, want %s — without it an abandoned stream's set lives forever", got, viewerKeyTTL)
	}
	if viewerKeyTTL <= viewerWindow {
		t.Error("the key expires inside its own counting window; a live stream's viewers would vanish mid-broadcast")
	}
}
