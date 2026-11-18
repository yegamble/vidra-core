package federation

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// actorFetchCooldown is how long a refreshed actor key, or a failed actor
// resolution, stands before the same actor URL may be fetched again.
const actorFetchCooldown = 5 * time.Minute

// maxActorFetchEntries caps each map of the gate. A full map is trimmed in one
// batch to evictActorFetchTo — expired entries first, then arbitrary ones — so
// the scan runs once per batch of inserts, not on every insert. An attacker
// cycling through fresh actor URLs gains nothing by filling it, since each
// fresh URL costs it a fetch either way, and the cap keeps memory bounded.
const (
	maxActorFetchEntries = 10000
	evictActorFetchTo    = maxActorFetchEntries * 9 / 10
)

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

	// misses shares one fetch among concurrent resolutions of one uncached
	// actor URL.
	misses singleflight.Group

	// maxInflight caps inbound-path actor fetches in flight at once, across
	// all hosts (inboundMaxInflight; a field so a test can lower it).
	maxInflight int

	mu sync.Mutex
	// hosts is the per-host fetch budget of the inbound-signature path.
	hosts    map[string]*hostBudget
	inflight int
	// bypass and bypassInflight are the followed actors' own ceiling;
	// lookups is the non-blocking semaphore on followed lookups.
	bypass         *hostBudget
	bypassInflight int
	lookups        chan struct{}
	// refreshed is when each cached actor's key was last re-fetched.
	refreshed map[string]time.Time
	// failed is when each actor URL last failed to resolve.
	failed map[string]time.Time
}

func newActorFetchGate() *actorFetchGate {
	return &actorFetchGate{
		now:         time.Now,
		maxInflight: inboundMaxInflight,
		hosts:       map[string]*hostBudget{},
		lookups:     make(chan struct{}, maxFollowedLookups),
		refreshed:   map[string]time.Time{},
		failed:      map[string]time.Time{},
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
			if len(m) <= evictActorFetchTo {
				break
			}
			delete(m, k)
		}
	}
	m[key] = at
}

// The inbound-signature path's per-host budget. The per-URL bounds above are
// sidestepped by naming a fresh URL each time, so actor fetches that verifying
// an inbox POST causes are also budgeted per HOST (NormalizeFollowHost: case,
// trailing dot and port variants are one host): a token bucket of
// inboundHostBurst refilled one token per inboundHostRefill. Over any window W
// one host buys at most inboundHostBurst + W/inboundHostRefill fetches through
// this path — 10 + 2 per minute. On top, at most inboundMaxInflight such
// fetches run at once across all hosts, except for an actor this instance
// follows (or whose host it instance-follows), which a flood elsewhere must
// not starve. Key refreshes of cached actors are bounded per actor already and
// take a slot from the global cap but no token (they are limited per actor);
// no fetch a local user's action causes is counted.
//
// Like the rest of the gate this is per process: N replicas, N budgets.
const (
	inboundHostBurst   = 10
	inboundHostRefill  = 30 * time.Second
	inboundMaxInflight = 32
	// followedLookupTimeout bounds the "is it followed?" lookup an exhausted
	// limit asks; a slower answer counts as not followed.
	followedLookupTimeout = 2 * time.Second
	// maxFollowedLookups caps concurrent "is it followed?" lookups; with none
	// free, the answer is "not followed".
	maxFollowedLookups = 8
	// A followed actor admitted past the limits above is still held to a
	// ceiling of its own: inboundBypassBurst refilled one per
	// inboundBypassRefill (2/s), and inboundBypassMaxInflight at once.
	inboundBypassBurst       = 120
	inboundBypassRefill      = 500 * time.Millisecond
	inboundBypassMaxInflight = 16
)

// errInboundFetchBudget: the inbound-signature path's fetch budget for this
// host, or its global in-flight cap, is spent. The request fails like any
// unverifiable signature; the sender retries.
var errInboundFetchBudget = errors.New("federation: inbound actor fetch budget spent")

// errInboundBypassCeiling: a followed actor's fetch is past the bypass ceiling.
var errInboundBypassCeiling = fmt.Errorf("%w: followed ceiling", errInboundFetchBudget)

// The two limits behind errInboundFetchBudget, told apart for the counter.
var (
	errInboundHostBudget = fmt.Errorf("%w: host budget", errInboundFetchBudget)
	errInboundGlobalCap  = fmt.Errorf("%w: in-flight cap", errInboundFetchBudget)
)

type hostBudget struct {
	tokens float64
	at     time.Time
}

// admitInbound admits one inbound-path actor fetch: a token from host's
// bucket and an in-flight slot. When either limit would refuse, followed is
// asked (outside the lock); an actor this instance follows is admitted past
// both, spending a token only if one is there. Everything is re-read after
// the lock is retaken — the bucket may have been spent or trimmed meanwhile.
func (g *actorFetchGate) admitInbound(host string, followed func() bool) (release func(), err error) {
	return g.admit(host, followed)
}

// admitInflight admits one key refresh against the global in-flight cap
// only, with the same followed-actor bypass. A refresh is already limited per
// actor; what it must not do is run past the cap the misses are held to.
func (g *actorFetchGate) admitInflight(followed func() bool) (release func(), err error) {
	return g.admit("", followed)
}

// admit is admitInbound (host != "") or admitInflight (host == "").
func (g *actorFetchGate) admit(host string, followed func() bool) (func(), error) {
	g.mu.Lock()
	bypassed := false
	if refusal := g.limitLocked(host); refusal != nil {
		g.mu.Unlock()
		if !g.lookup(followed) {
			return nil, refusal
		}
		g.mu.Lock()
		// Past the ordinary limits, a followed actor is held to the bypass
		// ceiling, read now that the lock is held again.
		now := g.now()
		if g.bypass == nil {
			g.bypass = &hostBudget{tokens: inboundBypassBurst, at: now}
		}
		g.bypass.tokens = min(inboundBypassBurst, g.bypass.tokens+float64(now.Sub(g.bypass.at))/float64(inboundBypassRefill))
		g.bypass.at = now
		if g.bypass.tokens < 1 || g.bypassInflight >= inboundBypassMaxInflight {
			g.mu.Unlock()
			return nil, errInboundBypassCeiling
		}
		g.bypass.tokens--
		g.bypassInflight++
		bypassed = true
	}
	if b := g.bucketLocked(host); b != nil && b.tokens >= 1 {
		b.tokens--
	}
	g.inflight++
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		g.inflight--
		if bypassed {
			g.bypassInflight--
		}
		g.mu.Unlock()
	}, nil
}

// lookup runs followed under the lookup semaphore; with no slot free the
// answer is "not followed", so refused requests cannot queue on the database.
func (g *actorFetchGate) lookup(followed func() bool) bool {
	select {
	case g.lookups <- struct{}{}:
		defer func() { <-g.lookups }()
		return followed()
	default:
		return false
	}
}

// limitLocked names the limit one more fetch for host would exceed — its
// bucket (when host != "") or the global in-flight cap — or nil when it fits.
// Callers hold mu.
func (g *actorFetchGate) limitLocked(host string) error {
	if b := g.bucketLocked(host); b != nil && b.tokens < 1 {
		return errInboundHostBudget
	}
	if g.inflight >= g.maxInflight {
		return errInboundGlobalCap
	}
	return nil
}

// bucketLocked returns host's bucket, refilled to now, creating it on first
// use; nil for host == "". Callers hold mu.
func (g *actorFetchGate) bucketLocked(host string) *hostBudget {
	if host == "" {
		return nil
	}
	now := g.now()
	b, ok := g.hosts[host]
	if !ok {
		if len(g.hosts) >= maxActorFetchEntries {
			g.trimHosts(now)
		}
		b = &hostBudget{tokens: inboundHostBurst, at: now}
		g.hosts[host] = b
	}
	b.tokens = min(inboundHostBurst, b.tokens+float64(now.Sub(b.at))/float64(inboundHostRefill))
	b.at = now
	return b
}

// trimHosts drops, in one batch, hosts whose bucket has refilled (forgetting
// them changes nothing), then arbitrary ones down to evictActorFetchTo.
// Callers hold mu.
func (g *actorFetchGate) trimHosts(now time.Time) {
	for h, b := range g.hosts {
		if now.Sub(b.at) >= inboundHostBurst*inboundHostRefill {
			delete(g.hosts, h)
		}
	}
	for h := range g.hosts {
		if len(g.hosts) <= evictActorFetchTo {
			break
		}
		delete(g.hosts, h)
	}
}

// WithInboundFetchRefusedCounter installs a counter of inbound-signature-path
// requests whose actor fetch was refused before the network, called with a
// fixed reason: host_budget, global_cap, followed_ceiling or blocked_host.
// Nil = not counted.
func WithInboundFetchRefusedCounter(f func(reason string)) Option {
	return func(s *Service) { s.onInboundFetchRefused = f }
}

// countInboundRefusal reports err to the refusal counter when it is one of the
// refusals the counter names.
func (s *Service) countInboundRefusal(err error) {
	if err == nil || s.onInboundFetchRefused == nil {
		return
	}
	switch {
	case errors.Is(err, errInboundHostBudget):
		s.onInboundFetchRefused("host_budget")
	case errors.Is(err, errInboundGlobalCap):
		s.onInboundFetchRefused("global_cap")
	case errors.Is(err, errInboundBypassCeiling):
		s.onInboundFetchRefused("followed_ceiling")
	case errors.Is(err, ErrActorHostBlocked):
		s.onInboundFetchRefused("blocked_host")
	}
}
