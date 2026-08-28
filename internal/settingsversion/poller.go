// Package settingsversion carries the cross-replica invalidation signal for the
// three admin-editable stores every api process holds IN MEMORY: the
// instance-settings overlay (internal/instancesettings), the instance documents
// (internal/instancedocs — ToS/privacy links, homepage markdown, custom
// CSS/JS), and the branding images (internal/profileimage).
//
// THE FAILURE MODE IT CLOSES. Those caches exist so that GET /instance — which
// every page load hits — and the public delivery routes never round-trip to the
// database. Each of them is filled once at boot and refreshed only by the
// process that served the write. With one api process that is correct. With N
// behind a load balancer, an admin change takes effect on exactly ONE replica
// and the other N-1 keep answering with the value they booted with, until they
// are restarted. Nothing errors and nothing logs; the admin's own read has a
// 1/N chance of confirming the change, so the symptom is an intermittently
// wrong instance rather than an obviously broken one. Turning registrations off
// during an abuse wave and having them stay on for two thirds of arriving users
// is the shape of the bug.
//
// THE MECHANISM. One counter row (migration 0121). Every write to any of the
// three stores increments it in the same call that persisted the row; every
// replica re-reads it on a short jittered ticker and, when the number has
// moved, reloads all three caches. Staleness is bounded by the poll interval
// and nothing new has to be deployed to get it.
//
// WHY POLLING AND NOT A PUSH. LISTEN/NOTIFY needs a permanently pinned
// connection per replica — a listening connection cannot be returned to the
// pool — on a pool whose size is a scarce, operator-tuned resource, and it
// silently drops notifications across a reconnect, so a poller would still be
// required as the backstop. Redis pub/sub would put a hard dependency on Redis
// on a CORRECTNESS path; everything Vidra does with Redis today fails open
// (rate limiters degrade, /readyz treats a Redis blip as degraded rather than
// fatal), and a Redis outage must not be able to fork the fleet's view of its
// own configuration.
package settingsversion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/vidra/vidra-core/internal/jobloop"
)

// DefaultInterval is how often a replica re-reads the counter. The read is a
// single-row primary-key lookup, so the cost is one trivial query per replica
// per interval; ten seconds is the middle of the 5–15s band the phase-5
// statelessness audit settled on — short enough that an admin does not
// experience it as "the change did not save", long enough to be invisible on
// the database. jobloop's start jitter spreads a fleet's phases so the reads
// trickle instead of arriving together after every rolling deploy.
const DefaultInterval = 10 * time.Second

// Repository is the counter access the poller needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	GetSettingsVersion(ctx context.Context) (int64, error)
	BumpSettingsVersion(ctx context.Context) (int64, error)
}

// Bumper is the write half of Repository, on its own so a caller that only
// advances the counter does not have to hold a reader.
type Bumper interface {
	BumpSettingsVersion(ctx context.Context) (int64, error)
}

// BumpFunc adapts a Bumper to the plain func the three cache-owning services
// take, so none of them imports this package (or knows a counter exists — they
// know only that a write has to announce itself). A nil Bumper yields a nil
// func, which those services treat as "not wired": single-process installs and
// the existing unit fakes are unaffected.
func BumpFunc(b Bumper) func(context.Context) error {
	if b == nil {
		return nil
	}
	return func(ctx context.Context) error {
		_, err := b.BumpSettingsVersion(ctx)
		return err
	}
}

// Cache is one in-memory store the counter guards. Name appears in the failure
// log; Reload is the store's existing boot loader, reused verbatim.
type Cache struct {
	Name   string
	Reload func(ctx context.Context) error
}

// Poller re-reads the counter on a ticker and reloads every Cache when it has
// moved. Safe for concurrent use.
type Poller struct {
	repo     Repository
	caches   []Cache
	interval time.Duration
	// known is the counter value this replica has already acted on. It is the
	// whole state of the poller: reload-on-change, never on every tick.
	known atomic.Int64
}

// New builds a poller. A non-positive interval takes DefaultInterval.
func New(repo Repository, interval time.Duration, caches ...Cache) *Poller {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Poller{repo: repo, caches: caches, interval: interval}
}

// Prime records the counter as it stands right now, without reloading anything.
// Call it at boot AFTER the initial cache loads: those loads already read the
// current state, so the replica starts in agreement with the database and the
// first tick is a no-op.
//
// A failed Prime is NOT fatal and the caller should log it and carry on. The
// token stays at zero, so the first successful tick sees a difference and
// reloads once — the caches are reloaded with the same queries boot used, so a
// redundant reload is harmless.
func (p *Poller) Prime(ctx context.Context) error {
	v, err := p.repo.GetSettingsVersion(ctx)
	if err != nil {
		return err
	}
	p.known.Store(v)
	return nil
}

// Known reports the counter value this replica has acted on (tests, logging).
func (p *Poller) Known() int64 { return p.known.Load() }

// Tick performs one poll. It reports whether the caches were reloaded.
//
// Reload-on-change only: an unchanged counter costs one primary-key read and
// nothing else. Reloading on every tick would rebuild three maps per replica
// every interval forever, and would turn a settings read into a moving target
// for no benefit.
//
// This deliberately does NOT coordinate with the local reload the writing
// replica already performs. Both paths call the same idempotent loaders against
// the same rows, so the worst case — the writer polls its own bump and reloads
// a second time — is a duplicated read of three small tables.
func (p *Poller) Tick(ctx context.Context) (bool, error) {
	v, err := p.repo.GetSettingsVersion(ctx)
	if err != nil {
		// Bounded staleness, never a crash: keep the last known value so a
		// change written during the outage is still detected once the database
		// answers again.
		return false, err
	}
	if v == p.known.Load() {
		return false, nil
	}
	var failures []error
	for _, c := range p.caches {
		if err := c.Reload(ctx); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", c.Name, err))
		}
	}
	if len(failures) > 0 {
		// Do NOT advance the token. A half-reloaded replica is a stale replica,
		// and recording the new value here would mean the next tick sees no
		// change and the write is lost until restart — the very bug this
		// package exists to remove.
		return false, errors.Join(failures...)
	}
	p.known.Store(v)
	return true, nil
}

// Run polls until ctx is canceled. It blocks; call it in a goroutine.
//
// Deliberately NOT leader-gated, unlike most of the loops in cmd/api: this is
// the one job where EVERY replica must act, because the whole point is that
// each of them holds its own copy of the state.
func (p *Poller) Run(ctx context.Context, logger *slog.Logger) {
	jobloop.Loop{
		Interval: p.interval,
		Jitter:   true,
		Passes: []jobloop.Pass{{
			FailMsg: "settings version poll failed; keeping the last known instance state",
			DoneMsg: "reloaded instance settings/documents/branding after a change on another replica",
			Run: func(ctx context.Context, _ time.Time) (int, error) {
				changed, err := p.Tick(ctx)
				if err != nil {
					return 0, err
				}
				if changed {
					return 1, nil
				}
				return 0, nil
			},
		}},
	}.Run(ctx, logger)
}
