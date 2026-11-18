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
	// domains, domainInflight and global are the across-host limits.
	domains        map[string]*hostBudget
	domainInflight map[string]int
	global         *hostBudget
	// refreshed is when each cached actor's key was last re-fetched.
	refreshed map[string]time.Time
	// failed is when each actor URL last failed to resolve.
	failed map[string]time.Time
}

func newActorFetchGate() *actorFetchGate {
	return &actorFetchGate{
		now:            time.Now,
		maxInflight:    inboundMaxInflight,
		hosts:          map[string]*hostBudget{},
		lookups:        make(chan struct{}, maxFollowedLookups),
		domains:        map[string]*hostBudget{},
		domainInflight: map[string]int{},
		refreshed:      map[string]time.Time{},
		failed:         map[string]time.Time{},
	}
}

// refreshDue reports, without recording anything, whether a cached actor's
// key is outside its refresh cooldown.
func (g *actorFetchGate) refreshDue(actorURL string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.refreshed[actorURL]
	return !ok || g.now().Sub(at) >= actorFetchCooldown
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
// fetches run at once across all hosts. An actor this instance follows (an
// accepted follow of that exact actor — an instance follow of its host does
// not count) is admitted past these limits, which a flood elsewhere must not
// starve it of, but held to the bypass ceiling below. Key refreshes of cached
// actors spend a host and a domain token and take a slot under both in-flight
// caps like a miss, but no global-rate token (that bucket is left to misses),
// and are also limited to one per actor per actorFetchCooldown. No fetch a
// local user's action causes is counted.
//
// Like the rest of the gate this is per process: N replicas, N budgets.
const (
	inboundHostBurst   = 10
	inboundHostRefill  = 30 * time.Second
	inboundMaxInflight = 32
	// Across hosts: a bucket per registrable domain (eTLD+1; an IPv4 /24 or
	// IPv6 /48 for an IP literal) of inboundDomainBurst refilled one token per
	// inboundDomainRefill, at most inboundDomainMaxInflight fetches in flight
	// per registrable domain, and one global bucket of inboundGlobalBurst
	// refilled one token per inboundGlobalRefill (5/s) for misses.
	inboundDomainBurst       = 20
	inboundDomainRefill      = 15 * time.Second
	inboundDomainMaxInflight = 4
	inboundGlobalBurst       = 64
	inboundGlobalRefill      = 200 * time.Millisecond
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

// errInboundFetchBudget: one of the inbound-signature path's fetch limits is
// spent. Nothing is fetched; the inbox answers "later" and the sender retries.
var errInboundFetchBudget = errors.New("federation: inbound actor fetch budget spent")

// IsInboundFetchRefused reports whether err is an inbound-path actor fetch
// refused by one of the gate's limits (any errInboundFetchBudget). Nothing was
// fetched and nothing about the signature was judged, so the inbox answers
// "later", not "bad signature". A blocked host, a failed fetch and a signature
// that does not verify are not this.
func IsInboundFetchRefused(err error) bool { return errors.Is(err, errInboundFetchBudget) }

// errInboundHostUnkeyed: the actor URL's host has no key the per-host budget
// can count under (NormalizeFollowHost refuses it, e.g. an IPv6 literal), so
// it is not fetched from this path at all. Deliberately NOT a budget error: no
// retry can succeed, so the sender is told "unverifiable", not "later".
var errInboundHostUnkeyed = errors.New("federation: inbound actor host cannot be budgeted")

// errInboundBypassCeiling: a followed actor's fetch is past the bypass ceiling.
var errInboundBypassCeiling = fmt.Errorf("%w: followed ceiling", errInboundFetchBudget)

// The across-host limits.
var (
	errInboundDomainBudget = fmt.Errorf("%w: domain budget", errInboundFetchBudget)
	errInboundDomainCap    = fmt.Errorf("%w: domain in-flight cap", errInboundFetchBudget)
	errInboundGlobalRate   = fmt.Errorf("%w: global rate", errInboundFetchBudget)
)

// The two limits behind errInboundFetchBudget, told apart for the counter.
var (
	errInboundHostBudget = fmt.Errorf("%w: host budget", errInboundFetchBudget)
	errInboundGlobalCap  = fmt.Errorf("%w: in-flight cap", errInboundFetchBudget)
)

type hostBudget struct {
	tokens float64
	at     time.Time
}

// admitInbound admits one inbound-path miss for host (domain is its
// registrable-domain key): a token from each of the host, domain and global
// buckets, and a slot under the domain and global in-flight caps. When a limit
// would refuse, followed is asked (outside the lock, under the lookup
// semaphore); a followed actor is admitted past the limits but held to the
// bypass ceiling. Everything is re-read once the lock is retaken.
func (g *actorFetchGate) admitInbound(host, domain string, followed func() bool) (release func(), err error) {
	return g.admit(host, domain, true, followed)
}

// admitRefresh admits one key refresh of a cached actor: the host and domain
// buckets and both in-flight caps, with the same followed bypass. The global
// bucket is left to misses; a refresh is also limited per actor.
func (g *actorFetchGate) admitRefresh(host, domain string, followed func() bool) (release func(), err error) {
	return g.admit(host, domain, false, followed)
}

// admit is the three admissions above. Buckets are only read while checking,
// and created or spent only on admission, so refused requests for new hosts
// can neither evict a drained bucket nor grow the maps.
func (g *actorFetchGate) admit(host, domain string, global bool, followed func() bool) (func(), error) {
	g.mu.Lock()
	bypassed := false
	if refusal := g.limitLocked(host, domain, global); refusal != nil {
		g.mu.Unlock()
		if !g.lookup(followed) {
			return nil, refusal
		}
		g.mu.Lock()
		now := g.now()
		if g.bypass == nil {
			g.bypass = &hostBudget{tokens: inboundBypassBurst, at: now}
		}
		if refill(g.bypass, inboundBypassBurst, inboundBypassRefill, now).tokens < 1 || g.bypassInflight >= inboundBypassMaxInflight {
			g.mu.Unlock()
			return nil, errInboundBypassCeiling
		}
		g.bypass.tokens--
		g.bypassInflight++
		bypassed = true
	}
	now := g.now()
	spend := func(b *hostBudget) {
		if b.tokens >= 1 {
			b.tokens--
		}
	}
	if host != "" {
		spend(g.createLocked(g.hosts, host, inboundHostBurst, inboundHostRefill, now))
	}
	if domain != "" {
		spend(g.createLocked(g.domains, domain, inboundDomainBurst, inboundDomainRefill, now))
		g.domainInflight[domain]++
	}
	if global {
		spend(g.globalLocked(now))
	}
	g.inflight++
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		g.inflight--
		if bypassed {
			g.bypassInflight--
		}
		if domain != "" {
			if g.domainInflight[domain]--; g.domainInflight[domain] <= 0 {
				delete(g.domainInflight, domain)
			}
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

// limitLocked names the limit one more fetch would exceed, or nil when it
// fits. A bucket not yet created reads as full. Callers hold mu.
func (g *actorFetchGate) limitLocked(host, domain string, global bool) error {
	now := g.now()
	if b, ok := g.hosts[host]; ok && host != "" && refill(b, inboundHostBurst, inboundHostRefill, now).tokens < 1 {
		return errInboundHostBudget
	}
	if b, ok := g.domains[domain]; ok && domain != "" && refill(b, inboundDomainBurst, inboundDomainRefill, now).tokens < 1 {
		return errInboundDomainBudget
	}
	if global && g.globalLocked(now).tokens < 1 {
		return errInboundGlobalRate
	}
	if domain != "" && g.domainInflight[domain] >= inboundDomainMaxInflight {
		return errInboundDomainCap
	}
	if g.inflight >= g.maxInflight {
		return errInboundGlobalCap
	}
	return nil
}

// globalLocked returns the global bucket, refilled to now. Callers hold mu.
func (g *actorFetchGate) globalLocked(now time.Time) *hostBudget {
	if g.global == nil {
		g.global = &hostBudget{tokens: inboundGlobalBurst, at: now}
	}
	return refill(g.global, inboundGlobalBurst, inboundGlobalRefill, now)
}

// createLocked returns m[key] refilled to now, creating a full bucket on
// first use and trimming m in one batch when it is at its cap. Callers hold mu.
func (g *actorFetchGate) createLocked(m map[string]*hostBudget, key string, burst float64, every time.Duration, now time.Time) *hostBudget {
	b, ok := m[key]
	if !ok {
		if len(m) >= maxActorFetchEntries {
			trimBuckets(m, time.Duration(burst)*every, now)
		}
		b = &hostBudget{tokens: burst, at: now}
		m[key] = b
	}
	return refill(b, burst, every, now)
}

// bucketLocked is host's bucket, created if missing (tests). Callers hold mu.
func (g *actorFetchGate) bucketLocked(host string) *hostBudget {
	return g.createLocked(g.hosts, host, inboundHostBurst, inboundHostRefill, g.now())
}

// refill tops b up for the time since it was last read.
func refill(b *hostBudget, burst float64, every time.Duration, now time.Time) *hostBudget {
	b.tokens = min(burst, b.tokens+float64(now.Sub(b.at))/float64(every))
	b.at = now
	return b
}

// trimBuckets drops, in one batch, keys whose bucket has refilled (forgetting
// them changes nothing), then arbitrary ones down to evictActorFetchTo.
// Callers hold the gate's mu.
func trimBuckets(m map[string]*hostBudget, fullAfter time.Duration, now time.Time) {
	for k, b := range m {
		if now.Sub(b.at) >= fullAfter {
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

// WithInboundFetchRefusedCounter installs a counter of inbound-signature-path
// requests whose actor fetch was refused before the network, called with a
// fixed reason: host_budget, domain_budget, global_rate, domain_cap,
// global_cap, followed_ceiling or blocked_host.
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
	case errors.Is(err, errInboundDomainBudget):
		s.onInboundFetchRefused("domain_budget")
	case errors.Is(err, errInboundDomainCap):
		s.onInboundFetchRefused("domain_cap")
	case errors.Is(err, errInboundGlobalRate):
		s.onInboundFetchRefused("global_rate")
	case errors.Is(err, ErrActorHostBlocked):
		s.onInboundFetchRefused("blocked_host")
	}
}
