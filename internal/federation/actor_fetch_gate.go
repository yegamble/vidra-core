package federation

import (
	"sync"
	"time"
)

// actorFetchCooldown is how long a refreshed actor key, or a failed actor
// resolution, stands before the same actor URL may be fetched again.
const actorFetchCooldown = 5 * time.Minute

// maxActorFetchEntries caps each map of the gate. Past it, expired entries are
// swept and then an arbitrary one is dropped: an attacker cycling through
// fresh actor URLs gains nothing by filling it, since each fresh URL costs it
// a fetch either way, and the cap keeps memory bounded.
const maxActorFetchEntries = 10000

// actorFetchGate bounds the outbound actor fetches an inbound inbox request can
// cause. An inbox POST is unauthenticated until its signature verifies, and
// verifying it is what fetches: without a bound, every request naming a new
// keyId was one outbound GET, and every forged signature naming a cached actor
// was one more re-fetch of that actor's key. The gate is keyed on the actor
// URL, never on the client address, because the cost being bounded is the
// fetch, whoever asks for it.
//
// It is in-memory and per process. With several API replicas each holds its
// own copy, so the bound is per replica (N replicas, at most N fetches per
// actor per cooldown) — accepted: the point is that the count is bounded by
// time, not by the number of requests.
type actorFetchGate struct {
	now func() time.Time

	mu sync.Mutex
	// refreshed is when each cached actor's key was last re-fetched.
	refreshed map[string]time.Time
	// failed is when each actor URL last failed to resolve.
	failed map[string]time.Time
}

func newActorFetchGate() *actorFetchGate {
	return &actorFetchGate{
		now:       time.Now,
		refreshed: map[string]time.Time{},
		failed:    map[string]time.Time{},
	}
}

// allowRefresh reports whether a cached actor's key may be re-fetched now, and
// when it may, records the attempt — success or not, the next one waits.
func (g *actorFetchGate) allowRefresh(actorURL string) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if at, ok := g.refreshed[actorURL]; ok && now.Sub(at) < actorFetchCooldown {
		return false
	}
	g.put(g.refreshed, actorURL, now)
	return true
}

// recentlyFailed reports whether actorURL failed to resolve within the
// cooldown, in which case it is not fetched again yet.
func (g *actorFetchGate) recentlyFailed(actorURL string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.failed[actorURL]
	return ok && g.now().Sub(at) < actorFetchCooldown
}

// recordFailure remembers that actorURL failed to resolve.
func (g *actorFetchGate) recordFailure(actorURL string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.put(g.failed, actorURL, g.now())
}

// put stores m[key]=at, first making room when m is at its cap. Callers hold mu.
func (g *actorFetchGate) put(m map[string]time.Time, key string, at time.Time) {
	if _, ok := m[key]; !ok && len(m) >= maxActorFetchEntries {
		for k, t := range m {
			if at.Sub(t) >= actorFetchCooldown {
				delete(m, k)
			}
		}
		for k := range m {
			if len(m) < maxActorFetchEntries {
				break
			}
			delete(m, k)
		}
	}
	m[key] = at
}
