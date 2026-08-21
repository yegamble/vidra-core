// Package leaderlock elects exactly one instance to run the singleton
// background sweeps.
//
// # The problem
//
// vidra-core's background workers come in two kinds. The ones that CLAIM rows
// from a durable queue are safe to run everywhere — that is the entire point of
// the lease retrofit, and more instances mean more throughput. The ones that
// SWEEP are not: media garbage collection, the scheduled-publish sweeper, the
// upload sweeper, the search and IPFS reconcilers and the rest each walk a table
// (or a bucket) and act on whatever they find. Running them on every instance
// duplicates the work at best, and media GC is destructive.
//
// # Why an advisory lock rather than a lease
//
// A PostgreSQL advisory lock is held by a SESSION, so it is released
// automatically when that session ends — including when the process holding it
// is killed, OOMed or partitioned away and its connection drops. There is no
// lease to renew, no expiry to tune, and no window in which two instances both
// believe they are the leader because a heartbeat was late.
//
// That property depends on holding the lock on a DEDICATED connection.
// pg_advisory_unlock must run on the same session that took the lock, and a
// pooled connection is handed back after every query — so an Elector checks one
// connection out of the pool and keeps it for as long as it is the leader. One
// pinned connection per instance is the price.
//
// # The one caveat worth knowing
//
// A leader that is network-partitioned rather than dead still holds the lock
// until PostgreSQL notices the TCP session is gone (tcp_keepalives_*, minutes by
// default). During that window the singleton sweeps do not run anywhere. That is
// the correct failure to have: sweeps are periodic maintenance, and pausing them
// is safe in a way that running media GC from two instances is not.
package leaderlock

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SingletonCronClass is the advisory-lock class shared by every singleton-cron
// lock. It uses the TWO-INT form of pg_try_advisory_lock deliberately:
// golang-migrate takes its migration lock with the one-bigint form, and
// PostgreSQL keeps those two forms in separate lock spaces, so a hand-picked key
// here can never collide with a migration in progress.
const SingletonCronClass int32 = 0x7669_6472 // "vidr"

// SingletonCronsKey is the object id for the single lock guarding all singleton
// sweeps. They are elected together rather than individually: the sweeps are
// cheap and periodic, so spreading them across instances buys nothing and would
// cost one pinned connection per lock.
const SingletonCronsKey int32 = 1

// DefaultRetryInterval is how often a follower tries to become leader, and how
// often a leader checks that its connection is still alive. It bounds the gap
// between a leader dying and another instance taking over.
const DefaultRetryInterval = 15 * time.Second

// Elector holds a session-scoped advisory lock for as long as this process is
// the leader. The zero value is not usable; call New.
type Elector struct {
	pool     *pgxpool.Pool
	class    int32
	key      int32
	name     string
	interval time.Duration
	logger   *slog.Logger

	mu     sync.RWMutex
	leader bool
	conn   *pgxpool.Conn
}

// New builds an Elector for one lock. name appears in logs. A nil pool yields an
// Elector that is never the leader, which is what a deployment with no database
// wiring should get rather than a panic.
func New(pool *pgxpool.Pool, class, key int32, name string, logger *slog.Logger) *Elector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Elector{
		pool: pool, class: class, key: key, name: name,
		interval: DefaultRetryInterval, logger: logger,
	}
}

// IsLeader reports whether this instance currently holds the lock. Workers call
// it once per tick; a false answer means "skip this tick", never "shut down",
// because leadership can move to this instance at any time.
func (e *Elector) IsLeader() bool {
	if e == nil {
		// A nil Elector means leader election was never wired — single-instance
		// deployments and tests. Everything runs, which is the pre-election
		// behaviour.
		return true
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leader
}

// Run drives the election until ctx is cancelled. It blocks; call it in a
// goroutine. It attempts acquisition immediately, so an instance starting alone
// becomes leader at once rather than after one interval.
func (e *Elector) Run(ctx context.Context) {
	if e == nil || e.pool == nil {
		return
	}
	defer e.release(context.WithoutCancel(ctx))

	t := time.NewTicker(e.interval)
	defer t.Stop()
	e.step(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.step(ctx)
		}
	}
}

// step is one election tick: a follower tries to take the lock, a leader
// verifies it still has one.
func (e *Elector) step(ctx context.Context) {
	if e.IsLeader() {
		e.verify(ctx)
		return
	}
	e.tryAcquire(ctx)
}

func (e *Elector) tryAcquire(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		e.logger.WarnContext(ctx, "leader election: could not take a connection", "lock", e.name, "error", err.Error())
		return
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, e.class, e.key).Scan(&got); err != nil {
		conn.Release()
		e.logger.WarnContext(ctx, "leader election: lock attempt failed", "lock", e.name, "error", err.Error())
		return
	}
	if !got {
		// Someone else leads. Give the connection straight back — holding it
		// would pin one pooled connection per follower for nothing.
		conn.Release()
		return
	}
	e.mu.Lock()
	e.leader = true
	e.conn = conn
	e.mu.Unlock()
	e.logger.InfoContext(ctx, "elected leader for the singleton background sweeps", "lock", e.name)
}

// verify checks the held connection is still usable. If it is not, leadership is
// dropped locally so the next tick re-acquires — and PostgreSQL will have
// released the lock itself when the session died.
func (e *Elector) verify(ctx context.Context) {
	e.mu.RLock()
	conn := e.conn
	e.mu.RUnlock()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		e.logger.WarnContext(ctx, "leader election: lost the connection holding the lock; standing down",
			"lock", e.name, "error", err.Error())
		e.stepDown()
	}
}

// stepDown drops leadership locally and returns the connection to the pool
// WITHOUT unlocking: this path exists precisely because the session is broken,
// so the lock is already gone server-side.
func (e *Elector) stepDown() {
	e.mu.Lock()
	conn := e.conn
	e.conn = nil
	e.leader = false
	e.mu.Unlock()
	if conn != nil {
		// Destroy rather than Release: a connection that failed a ping must not
		// go back into rotation.
		conn.Conn().Close(context.Background())
		conn.Release()
	}
}

// release gives up leadership cleanly on shutdown. Unlocking explicitly rather
// than relying on the connection closing hands leadership over in seconds
// instead of whenever PostgreSQL notices the session is gone.
func (e *Elector) release(ctx context.Context) {
	e.mu.Lock()
	conn := e.conn
	wasLeader := e.leader
	e.conn = nil
	e.leader = false
	e.mu.Unlock()
	if conn == nil {
		return
	}
	if wasLeader {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1, $2)`, e.class, e.key); err != nil {
			e.logger.WarnContext(ctx, "leader election: clean unlock failed; the lock will clear when the session ends",
				"lock", e.name, "error", err.Error())
		}
	}
	conn.Release()
}
