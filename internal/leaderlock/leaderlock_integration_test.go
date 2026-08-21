//go:build integration

// Integration test: leader election against a REAL PostgreSQL.
//
// None of this can be tested with a fake. The whole mechanism IS a PostgreSQL
// behaviour: a session-scoped advisory lock that exactly one session can hold
// and that the server releases by itself when that session ends.
//
// Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/leaderlock/
package leaderlock

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// Room for the pinned leader connection plus the test's own queries.
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testKey gives each test its own object id so they can run in parallel against
// one database without fighting over the same lock.
func testKey(t *testing.T) int32 {
	t.Helper()
	var h int32 = 17
	for _, c := range t.Name() {
		h = h*31 + int32(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, why string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

func newFastElector(t *testing.T, pool *pgxpool.Pool, key int32) *Elector {
	t.Helper()
	e := New(pool, SingletonCronClass, key, t.Name(), quietLogger())
	// The production interval is 15s; tests would take minutes at that pace.
	e.interval = 50 * time.Millisecond
	return e
}

// TestExactlyOneInstanceLeads is the contract: run several Electors against one
// database and exactly one of them may believe it is the leader. Without this,
// media garbage collection — which deletes objects — runs on every instance.
func TestExactlyOneInstanceLeads(t *testing.T) {
	pool := testPool(t)
	key := testKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const instances = 4
	electors := make([]*Elector, instances)
	for i := range electors {
		electors[i] = newFastElector(t, pool, key)
		go electors[i].Run(ctx)
	}

	countLeaders := func() int {
		n := 0
		for _, e := range electors {
			if e.IsLeader() {
				n++
			}
		}
		return n
	}
	waitFor(t, "one instance to be elected", 5*time.Second, func() bool { return countLeaders() == 1 })

	// And it must STAY one — a follower must never talk itself into leadership on
	// a later tick.
	for range 20 {
		if n := countLeaders(); n != 1 {
			t.Fatalf("%d instances believe they are leader; exactly one may", n)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestLeadershipMovesWhenTheLeaderStops proves the takeover half. A leader that
// goes away must not hold the singleton sweeps hostage.
func TestLeadershipMovesWhenTheLeaderStops(t *testing.T) {
	pool := testPool(t)
	key := testKey(t)

	leaderCtx, stopLeader := context.WithCancel(context.Background())
	first := newFastElector(t, pool, key)
	go first.Run(leaderCtx)
	waitFor(t, "the first instance to lead", 5*time.Second, first.IsLeader)

	followerCtx, cancelFollower := context.WithCancel(context.Background())
	defer cancelFollower()
	second := newFastElector(t, pool, key)
	go second.Run(followerCtx)

	// While the first leads, the second must not.
	time.Sleep(300 * time.Millisecond)
	if second.IsLeader() {
		t.Fatal("a second instance became leader while the first still held the lock")
	}

	// The first shuts down cleanly, which unlocks explicitly rather than waiting
	// for PostgreSQL to notice the session is gone.
	stopLeader()
	waitFor(t, "the second instance to take over", 5*time.Second, second.IsLeader)
	if first.IsLeader() {
		t.Error("the stopped instance still reports itself as leader")
	}
}

// TestFollowersDoNotPinConnections guards a resource leak that would only show
// up under scale-out: if a failed acquisition kept its connection, every
// follower would permanently consume one pooled connection for nothing.
//
// Deliberately synchronous. Driving this through Run() and sampling the pool
// races against followers that legitimately hold a connection for the duration
// of one attempt, which measures the sampling instant rather than the property.
func TestFollowersDoNotPinConnections(t *testing.T) {
	pool := testPool(t)
	key := testKey(t)
	ctx := context.Background()

	leader := newFastElector(t, pool, key)
	leader.tryAcquire(ctx)
	if !leader.IsLeader() {
		t.Fatal("the first acquisition did not take the lock on a clean key")
	}
	t.Cleanup(func() { leader.release(context.Background()) })

	base := pool.Stat().AcquiredConns()
	if base != 1 {
		t.Fatalf("the leader holds %d connections, want exactly 1", base)
	}

	// Several followers each attempt and fail. None may keep its connection.
	for i := range 5 {
		f := newFastElector(t, pool, key)
		f.tryAcquire(ctx)
		if f.IsLeader() {
			t.Fatalf("follower %d took a lock the leader already holds", i)
		}
		if got := pool.Stat().AcquiredConns(); got != base {
			t.Fatalf("after follower %d failed to acquire, %d connections are checked out, want %d — "+
				"a follower is pinning a connection it does not need", i, got, base)
		}
	}
}

// TestNilElectorLeadsSoSingleInstanceIsUnaffected pins the degradation path: a
// deployment that never wires election must keep running its sweeps, not stop
// running them.
func TestNilElectorLeadsSoSingleInstanceIsUnaffected(t *testing.T) {
	var e *Elector
	if !e.IsLeader() {
		t.Error("a nil Elector must report leadership so an unwired deployment keeps sweeping")
	}
	e = New(nil, SingletonCronClass, 1, "no-pool", quietLogger())
	// A pool-less Elector never acquires, and Run must return rather than spin.
	done := make(chan struct{})
	go func() { defer close(done); e.Run(context.Background()) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return for a pool-less Elector")
	}
}

// TestTwoIntLockSpaceIsSeparateFromTheOneBigintForm verifies the assumption the
// key choice rests on. golang-migrate takes its migration lock with
// pg_advisory_lock(bigint); this package uses the two-int form specifically
// because PostgreSQL keeps those in different lock spaces. If that were wrong,
// leader election could block a migration — or a migration could silently
// prevent election — and the failure would be baffling.
func TestTwoIntLockSpaceIsSeparateFromTheOneBigintForm(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// The two-int (class, key) whose bit pattern equals the one-bigint value.
	const class, key int32 = 0x7669_6472, 1
	bigint := int64(class)<<32 | int64(key)

	a, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	defer a.Release()
	b, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	defer b.Release()

	var gotBigint bool
	if err := a.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, bigint).Scan(&gotBigint); err != nil {
		t.Fatalf("bigint lock: %v", err)
	}
	if !gotBigint {
		t.Fatal("could not take the one-bigint lock on a clean database")
	}
	defer func() { _, _ = a.Exec(ctx, `SELECT pg_advisory_unlock($1)`, bigint) }()

	var gotTwoInt bool
	if err := b.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, class, key).Scan(&gotTwoInt); err != nil {
		t.Fatalf("two-int lock: %v", err)
	}
	if !gotTwoInt {
		t.Fatal("the two-int lock collided with the one-bigint lock of the same bit pattern; " +
			"the key choice in this package assumes they are separate lock spaces, and a " +
			"golang-migrate migration could therefore block leader election")
	}
	_, _ = b.Exec(ctx, `SELECT pg_advisory_unlock($1, $2)`, class, key)
}
