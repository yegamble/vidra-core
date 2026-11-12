package video

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// anonCacheMaxEntries bounds the cache. Search counts are keyed by a caller's
// query, so the key space is whatever callers send; at the bound the cache is
// emptied, which costs the next callers one load each and nothing else.
const anonCacheMaxEntries = 256

// anonCache shares one answer among ANONYMOUS callers for a short time. It may
// only hold answers that are the same for every caller without an account: a
// signed-in caller's mutes, blocks and per-flag policies are their own, so
// nothing keyed here may depend on a viewer.
//
// Two rules keep it honest. A write that can change a public list calls
// invalidate, after which nothing loaded before that write is served again, so
// a takedown leaves the lists at once rather than when the TTL runs out. And
// one load runs per key at a time: callers arriving during it wait for its
// answer instead of each running the statement.
//
// It is per process. With a second api process, a write on one would leave the
// other serving its copy until the TTL; that needs a shared signal this does
// not have.
type anonCache struct {
	ttl time.Duration
	now func() time.Time
	gen atomic.Uint64

	mu      sync.Mutex
	seenGen uint64
	entries map[string]*anonEntry
}

type anonEntry struct {
	done    chan struct{} // closed once val/err are set
	val     any
	err     error
	expires time.Time
}

func newAnonCache(ttl time.Duration) *anonCache {
	return &anonCache{ttl: ttl, now: time.Now, entries: map[string]*anonEntry{}}
}

func (c *anonCache) invalidate() { c.gen.Add(1) }

// acquire returns the entry for key and whether the caller must load it.
func (c *anonCache) acquire(key string) (*anonEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen := c.gen.Load(); gen != c.seenGen || len(c.entries) >= anonCacheMaxEntries {
		c.entries, c.seenGen = map[string]*anonEntry{}, gen
	}
	if e, ok := c.entries[key]; ok {
		select {
		case <-e.done:
			if e.err == nil && c.now().Before(e.expires) {
				return e, false
			}
		default:
			return e, false // loading: wait for it
		}
	}
	e := &anonEntry{done: make(chan struct{})}
	c.entries[key] = e
	return e, true
}

func (c *anonCache) get(ctx context.Context, key string, load func(context.Context) (any, error)) (any, error) {
	for attempt := 0; ; attempt++ {
		e, leader := c.acquire(key)
		if leader {
			return c.fill(ctx, e, load)
		}
		select {
		case <-e.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// The loader's request going away is not this caller's failure: load
		// once more (as the loader, if nobody else is). Any other error is the
		// shared answer.
		if e.err != nil && attempt == 0 && ctx.Err() == nil &&
			(errors.Is(e.err, context.Canceled) || errors.Is(e.err, context.DeadlineExceeded)) {
			continue
		}
		return e.val, e.err
	}
}

func (c *anonCache) fill(ctx context.Context, e *anonEntry, load func(context.Context) (any, error)) (any, error) {
	e.err = errors.New("anonymous cache: the load did not finish")
	defer close(e.done) // also on a panic, so no waiter is left hanging
	e.val, e.err = load(ctx)
	e.expires = c.now().Add(c.ttl)
	return e.val, e.err
}

// anonCached is get with the value's type restored. A nil cache loads directly.
func anonCached[T any](ctx context.Context, c *anonCache, key string, load func(context.Context) (T, error)) (T, error) {
	if c == nil {
		return load(ctx)
	}
	v, err := c.get(ctx, key, func(ctx context.Context) (any, error) { return load(ctx) })
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}
