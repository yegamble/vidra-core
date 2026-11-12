package video

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnonCacheServesOneLoadUntilTheTTL(t *testing.T) {
	c := newAnonCache(15 * time.Second)
	clock := time.Unix(1000, 0)
	c.now = func() time.Time { return clock }
	loads := 0
	get := func() int {
		v, err := anonCached(context.Background(), c, "k", func(context.Context) (int, error) { loads++; return loads, nil })
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return v
	}
	first, second := get(), get()
	if first != 1 || second != 1 {
		t.Fatalf("second call reloaded (loads = %d)", loads)
	}
	clock = clock.Add(14 * time.Second)
	if get() != 1 {
		t.Fatalf("reloaded before the TTL (loads = %d)", loads)
	}
	clock = clock.Add(time.Second)
	if get() != 2 {
		t.Fatalf("did not reload at the TTL (loads = %d)", loads)
	}
}

func TestAnonCacheInvalidateDropsEveryKeyAtOnce(t *testing.T) {
	c := newAnonCache(time.Hour)
	loads := map[string]int{}
	get := func(key string) int {
		v, _ := anonCached(context.Background(), c, key, func(context.Context) (int, error) { loads[key]++; return loads[key], nil })
		return v
	}
	get("a")
	get("b")
	c.invalidate()
	if get("a") != 2 || get("b") != 2 {
		t.Fatalf("served a pre-invalidation answer: %v", loads)
	}
	if get("a") != 2 {
		t.Fatalf("did not cache after the invalidation: %v", loads)
	}
}

// A write that lands while a load is running must not leave that load's answer
// cached: it may have read the rows before the write.
func TestAnonCacheDoesNotKeepALoadThatStraddlesAnInvalidation(t *testing.T) {
	c := newAnonCache(time.Hour)
	first := true
	load := func(context.Context) (string, error) {
		if first {
			first = false
			c.invalidate() // the write commits mid-load
			return "stale", nil
		}
		return "fresh", nil
	}
	if v, _ := anonCached(context.Background(), c, "k", load); v != "stale" {
		t.Fatalf("first answer = %q", v)
	}
	if v, _ := anonCached(context.Background(), c, "k", load); v != "fresh" {
		t.Fatalf("answer after the write = %q, want a new load", v)
	}
}

func TestAnonCacheRunsOneLoadForConcurrentCallers(t *testing.T) {
	c := newAnonCache(time.Hour)
	var loads atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := anonCached(context.Background(), c, "k", func(context.Context) (int, error) {
				loads.Add(1)
				<-release
				return 7, nil
			})
			if v != 7 || err != nil {
				t.Errorf("got %d, %v", v, err)
			}
		}()
	}
	for loads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the others reach the wait
	close(release)
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Fatalf("loads = %d, want 1", n)
	}
}

func TestAnonCacheDoesNotCacheErrors(t *testing.T) {
	c := newAnonCache(time.Hour)
	boom := errors.New("database is down")
	calls := 0
	load := func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, boom
		}
		return 5, nil
	}
	if _, err := anonCached(context.Background(), c, "k", load); !errors.Is(err, boom) {
		t.Fatalf("first error = %v", err)
	}
	if v, err := anonCached(context.Background(), c, "k", load); v != 5 || err != nil {
		t.Fatalf("after the error: %d, %v", v, err)
	}
}

// The request that happens to be loading can be cancelled by its own client;
// the callers waiting on it must still get an answer.
func TestAnonCacheWaiterSurvivesTheLoadersCancellation(t *testing.T) {
	c := newAnonCache(time.Hour)
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	inLoad := make(chan struct{})
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		_, _ = anonCached(leaderCtx, c, "k", func(ctx context.Context) (int, error) {
			close(inLoad)
			<-ctx.Done()
			return 0, ctx.Err()
		})
	}()
	<-inLoad
	waiter := make(chan int, 1)
	go func() {
		v, err := anonCached(context.Background(), c, "k", func(context.Context) (int, error) { return 9, nil })
		if err != nil {
			t.Errorf("waiter error: %v", err)
		}
		waiter <- v
	}()
	time.Sleep(20 * time.Millisecond)
	cancelLeader()
	<-leaderDone
	if v := <-waiter; v != 9 {
		t.Fatalf("waiter got %d, want its own load's 9", v)
	}
}

func TestAnonCacheStaysBounded(t *testing.T) {
	c := newAnonCache(time.Hour)
	for i := 0; i < anonCacheMaxEntries*3; i++ {
		_, _ = anonCached(context.Background(), c, string(rune('a'+i%26))+time.Duration(i).String(), func(context.Context) (int, error) { return i, nil })
		if len(c.entries) > anonCacheMaxEntries {
			t.Fatalf("entries = %d after %d keys", len(c.entries), i+1)
		}
	}
}

func TestAnonCachedWithoutACacheLoadsEveryTime(t *testing.T) {
	loads := 0
	for i := 0; i < 2; i++ {
		_, _ = anonCached(context.Background(), nil, "k", func(context.Context) (int, error) { loads++; return 0, nil })
	}
	if loads != 2 {
		t.Fatalf("loads = %d, want 2", loads)
	}
}

// A load that panics (the request's own recover middleware catches it) must
// not leave its key locked for every caller after it.
func TestAnonCacheSurvivesAPanickingLoad(t *testing.T) {
	c := newAnonCache(time.Hour)
	func() {
		defer func() { _ = recover() }()
		_, _ = anonCached(context.Background(), c, "k", func(context.Context) (int, error) { panic("boom") })
	}()
	done := make(chan int, 1)
	go func() {
		v, _ := anonCached(context.Background(), c, "k", func(context.Context) (int, error) { return 3, nil })
		done <- v
	}()
	select {
	case v := <-done:
		if v != 3 {
			t.Fatalf("after the panic: %d, want a new load's 3", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the key stayed locked after a panicking load")
	}
}
