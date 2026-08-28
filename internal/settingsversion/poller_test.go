package settingsversion

import (
	"context"
	"errors"
	"testing"
)

// fakeCounter is an in-memory settings_version row.
type fakeCounter struct {
	version int64
	reads   int
	err     error
}

func (f *fakeCounter) GetSettingsVersion(context.Context) (int64, error) {
	f.reads++
	if f.err != nil {
		return 0, f.err
	}
	return f.version, nil
}

func (f *fakeCounter) BumpSettingsVersion(context.Context) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.version++
	return f.version, nil
}

type countingCache struct {
	loads int
	err   error
}

func (c *countingCache) reload(context.Context) error {
	c.loads++
	return c.err
}

func newTestPoller(counter Repository, caches ...Cache) *Poller {
	return New(counter, 0, caches...)
}

// TestTickReloadsOnlyWhenTheCounterMoves is the whole design in one test: a
// tick against an unchanged counter must NOT reload, or every replica would
// rebuild three caches every ten seconds forever for nothing.
func TestTickReloadsOnlyWhenTheCounterMoves(t *testing.T) {
	ctx := context.Background()
	counter := &fakeCounter{version: 7}
	cache := &countingCache{}
	p := newTestPoller(counter, Cache{Name: "settings", Reload: cache.reload})

	if err := p.Prime(ctx); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if cache.loads != 0 {
		t.Fatalf("Prime reloaded %d caches, want 0 (boot already loaded them)", cache.loads)
	}

	for i := 0; i < 3; i++ {
		changed, err := p.Tick(ctx)
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		if changed {
			t.Fatalf("tick %d reported a change against an unchanged counter", i)
		}
	}
	if cache.loads != 0 {
		t.Fatalf("reloads on an unchanged counter = %d, want 0", cache.loads)
	}

	// Another replica writes.
	if _, err := counter.BumpSettingsVersion(ctx); err != nil {
		t.Fatalf("bump: %v", err)
	}
	changed, err := p.Tick(ctx)
	if err != nil {
		t.Fatalf("tick after bump: %v", err)
	}
	if !changed {
		t.Fatal("tick after a bump reported no change")
	}
	if cache.loads != 1 {
		t.Fatalf("reloads after a bump = %d, want 1", cache.loads)
	}
	if p.Known() != 8 {
		t.Fatalf("known version = %d, want 8", p.Known())
	}

	// And it settles again rather than reloading on every tick from here on.
	if _, err := p.Tick(ctx); err != nil {
		t.Fatalf("settled tick: %v", err)
	}
	if cache.loads != 1 {
		t.Fatalf("reloads after settling = %d, want 1", cache.loads)
	}
}

// TestTickReloadsEveryCache: one counter guards all three caches, so a change
// to any of them reloads all of them (they are cheap, boot-sized reads, and a
// per-store counter would be three round trips per tick instead of one).
func TestTickReloadsEveryCache(t *testing.T) {
	counter := &fakeCounter{version: 1}
	a, b, c := &countingCache{}, &countingCache{}, &countingCache{}
	p := newTestPoller(counter,
		Cache{Name: "settings", Reload: a.reload},
		Cache{Name: "documents", Reload: b.reload},
		Cache{Name: "branding", Reload: c.reload},
	)
	if err := p.Prime(context.Background()); err != nil {
		t.Fatalf("prime: %v", err)
	}
	counter.version = 2
	if _, err := p.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if a.loads != 1 || b.loads != 1 || c.loads != 1 {
		t.Fatalf("reloads = %d/%d/%d, want 1/1/1", a.loads, b.loads, c.loads)
	}
}

// TestTickKeepsLastKnownVersionOnReadError: a database blip must degrade to
// bounded staleness, never to a crash and never to a silently-advanced token
// that would make the replica skip the change it never read.
func TestTickKeepsLastKnownVersionOnReadError(t *testing.T) {
	ctx := context.Background()
	counter := &fakeCounter{version: 4}
	cache := &countingCache{}
	p := newTestPoller(counter, Cache{Name: "settings", Reload: cache.reload})
	if err := p.Prime(ctx); err != nil {
		t.Fatalf("prime: %v", err)
	}

	boom := errors.New("connection reset")
	counter.err = boom
	if _, err := p.Tick(ctx); !errors.Is(err, boom) {
		t.Fatalf("tick error = %v, want the read error", err)
	}
	if p.Known() != 4 {
		t.Fatalf("known version after a failed read = %d, want 4 (unchanged)", p.Known())
	}

	// The write that happened during the outage is still picked up afterwards.
	counter.err = nil
	counter.version = 9
	changed, err := p.Tick(ctx)
	if err != nil || !changed {
		t.Fatalf("recovery tick: changed=%v err=%v, want true/nil", changed, err)
	}
	if cache.loads != 1 {
		t.Fatalf("reloads after recovery = %d, want 1", cache.loads)
	}
}

// TestTickDoesNotAdvanceOnReloadFailure: a half-reloaded replica is a stale
// replica. If any cache failed to reload, the token must stay put so the next
// tick tries the whole set again — otherwise the change is lost until restart.
func TestTickDoesNotAdvanceOnReloadFailure(t *testing.T) {
	ctx := context.Background()
	counter := &fakeCounter{version: 1}
	good := &countingCache{}
	boom := errors.New("select failed")
	bad := &countingCache{err: boom}
	p := newTestPoller(counter,
		Cache{Name: "settings", Reload: good.reload},
		Cache{Name: "documents", Reload: bad.reload},
	)
	if err := p.Prime(ctx); err != nil {
		t.Fatalf("prime: %v", err)
	}
	counter.version = 2

	if _, err := p.Tick(ctx); !errors.Is(err, boom) {
		t.Fatalf("tick error = %v, want the reload error", err)
	}
	if p.Known() != 1 {
		t.Fatalf("known version after a failed reload = %d, want 1 (unchanged)", p.Known())
	}
	// Every cache is still attempted — one failure does not skip the rest.
	if good.loads != 1 {
		t.Fatalf("healthy cache reloads = %d, want 1", good.loads)
	}

	bad.err = nil
	if _, err := p.Tick(ctx); err != nil {
		t.Fatalf("retry tick: %v", err)
	}
	if p.Known() != 2 {
		t.Fatalf("known version after a successful retry = %d, want 2", p.Known())
	}
}

// TestPrimeFailureLeavesTheTokenAtZero: boot must not fail because the counter
// was unreadable for a moment. Starting at zero costs at most one redundant
// reload on the first successful tick, and reloads are idempotent.
func TestPrimeFailureLeavesTheTokenAtZero(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down at boot")
	counter := &fakeCounter{version: 12, err: boom}
	cache := &countingCache{}
	p := newTestPoller(counter, Cache{Name: "settings", Reload: cache.reload})

	if err := p.Prime(ctx); !errors.Is(err, boom) {
		t.Fatalf("prime error = %v, want the read error", err)
	}
	if p.Known() != 0 {
		t.Fatalf("known version after a failed prime = %d, want 0", p.Known())
	}
	counter.err = nil
	changed, err := p.Tick(ctx)
	if err != nil || !changed {
		t.Fatalf("first tick after a failed prime: changed=%v err=%v, want true/nil", changed, err)
	}
	if cache.loads != 1 {
		t.Fatalf("reloads = %d, want 1", cache.loads)
	}
}

// TestBumpFuncAdaptsARepository proves the seam handed to the three cache
// owners: they take a plain func so none of them imports this package.
func TestBumpFuncAdaptsARepository(t *testing.T) {
	counter := &fakeCounter{}
	bump := BumpFunc(counter)
	if bump == nil {
		t.Fatal("BumpFunc returned nil for a real repository")
	}
	if err := bump(context.Background()); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if counter.version != 1 {
		t.Fatalf("version after one bump = %d, want 1", counter.version)
	}
	if BumpFunc(nil) != nil {
		t.Fatal("BumpFunc(nil) must be nil so an unwired service does not call through a nil interface")
	}
}
