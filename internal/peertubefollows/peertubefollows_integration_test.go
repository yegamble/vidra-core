//go:build integration

package peertubefollows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type scriptedFollower struct{ errs map[string]error }

func (f scriptedFollower) FollowRemoteChannel(_ context.Context, _ uuid.UUID, target string) (federation.RemoteFollow, error) {
	return federation.RemoteFollow{}, f.errs[target]
}

func TestQueueLifecycleOnPostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var me, other uuid.UUID
	for _, u := range []*uuid.UUID{&me, &other} {
		n := uuid.NewString()[:8]
		if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
			"pf-"+n, "pf-"+n+"@example.test").Scan(u); err != nil {
			t.Fatal(err)
		}
		defer func(id uuid.UUID) { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) }(*u)
	}
	svc := &Service{
		Repo: st.Queries(), MaxAttempts: 2, BackoffBase: time.Hour,
		Follower: scriptedFollower{errs: map[string]error{
			"ok@a.example": nil, "blocked@b.example": federation.ErrBlockedFollowTarget,
			"local@me.example": federation.ErrLocalFollowTarget, "down@c.example": federation.ErrRemoteUnresolvable,
		}},
	}
	q, err := svc.Enqueue(ctx, me, []string{"@OK@a.example", "ok@a.example", "blocked@b.example", "local@me.example", "down@c.example"})
	if err != nil || q.JobID == nil || q.Queued != 4 {
		t.Fatalf("enqueue: %+v %v", q, err)
	}
	// One user gets perUserClaim rows per claim, so four targets take two passes.
	for pass, want := range []int{perUserClaim, 4 - perUserClaim} {
		if n, err := svc.Drain(ctx); err != nil || n != want {
			t.Fatalf("drain %d: %d %v", pass, n, err)
		}
	}
	s1, err := svc.Status(ctx, me, *q.JobID)
	if err != nil || s1.Total != 4 || s1.Done != 1 || s1.Failed != 0 || s1.SkippedInvalid != 1 || s1.Pending != 2 {
		t.Fatalf("after first drain: %+v %v", s1, err)
	}
	// down@c and blocked@b are backed off an hour: not due yet.
	if n, _ := svc.Drain(ctx); n != 0 {
		t.Fatalf("backed-off target re-claimed: %d", n)
	}
	// Pull it due; the second failure hits MaxAttempts and dead-letters.
	if _, err := st.Pool.Exec(ctx, `UPDATE peertube_follow_import_targets SET next_attempt_at = now() WHERE import_id = $1`, *q.JobID); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.Drain(ctx); n != 2 { // blocked and unresolvable retry alike
		t.Fatalf("retry drain: %d", n)
	}
	s2, _ := svc.Status(ctx, me, *q.JobID)
	if s2.Failed != 2 || s2.Pending != 0 {
		t.Fatalf("after retry: %+v", s2)
	}
	if _, err := svc.Status(ctx, other, *q.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user's job must be not-found: %v", err)
	}

	// Re-importing the same handles queues nothing new while any is pending,
	// and a target the user already follows is not queued at all.
	again, err := svc.Enqueue(ctx, me, []string{"down@c.example"})
	if err != nil || again.Queued != 1 {
		t.Fatalf("terminal rows must not block a new import: %+v %v", again, err)
	}
	dup, err := svc.Enqueue(ctx, me, []string{"down@c.example"})
	if err != nil || dup.Queued != 0 || dup.JobID != nil {
		t.Fatalf("re-import queued a duplicate pending row: %+v %v", dup, err)
	}
	var rows int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM peertube_follow_import_targets WHERE user_id=$1 AND target='down@c.example' AND state='pending'`, me).Scan(&rows)
	if rows != 1 {
		t.Fatalf("pending rows = %d", rows)
	}

	// Prune: a finished import older than the retention goes, a pending one stays.
	if _, err := st.Pool.Exec(ctx, `UPDATE peertube_follow_imports SET created_at = now() - interval '40 days' WHERE user_id = $1`, me); err != nil {
		t.Fatal(err)
	}
	// Scoped to this test's own rows: the shared database may hold other
	// tests' imports, and a no-op re-import leaves an empty finished import.
	if _, err := svc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM peertube_follow_imports WHERE user_id = $1`, me).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("imports left for the user = %d, want only the one with a pending target", left)
	}
	if _, err := svc.Status(ctx, me, *q.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the finished old import survived prune: %v", err)
	}

	// Federation off: nothing queued, everything counted.
	off := &Service{Repo: st.Queries(), Enabled: func() bool { return false }}
	oq, err := off.Enqueue(ctx, me, []string{"x@d.example", "y@d.example"})
	if err != nil || oq.JobID != nil || oq.SkippedFederation != 2 {
		t.Fatalf("disabled: %+v %v", oq, err)
	}
}

// blockingFollower holds every target of the slow user until released and
// records which targets were attempted.
type blockingFollower struct {
	slow    uuid.UUID
	release chan struct{}
	done    chan string
}

func (f *blockingFollower) FollowRemoteChannel(ctx context.Context, u uuid.UUID, target string) (federation.RemoteFollow, error) {
	if u == f.slow {
		select {
		case <-f.release:
		case <-ctx.Done():
		}
		return federation.RemoteFollow{}, nil
	}
	f.done <- target
	return federation.RemoteFollow{}, nil
}

func TestClaimIsFairAcrossUsersAndLeasedOnce(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var slow, quick uuid.UUID
	for _, u := range []*uuid.UUID{&slow, &quick} {
		n := uuid.NewString()[:8]
		if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
			"pg-"+n, "pg-"+n+"@example.test").Scan(u); err != nil {
			t.Fatal(err)
		}
		defer func(id uuid.UUID) { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) }(*u)
	}
	f := &blockingFollower{slow: slow, release: make(chan struct{}), done: make(chan string, 8)}
	svc := &Service{Repo: st.Queries(), Follower: f}
	var many []string
	for i := 0; i < 12; i++ {
		many = append(many, fmt.Sprintf("s%d@slow%d.example", i, i))
	}
	if _, err := svc.Enqueue(ctx, slow, many); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enqueue(ctx, quick, []string{"q@fast.example"}); err != nil {
		t.Fatal(err)
	}
	// The slow user's rows were queued first and are older, yet one claim takes
	// at most perUserClaim of them, so the other user's row is in the batch and
	// completes while the slow ones are still held.
	drained := make(chan struct{})
	go func() { _, _ = svc.Drain(ctx); close(drained) }()
	select {
	case got := <-f.done:
		if got != "q@fast.example" {
			t.Fatalf("unexpected target %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the other user's target was starved by a slow user's backlog")
	}
	// While those are held (leased), a second claim must not re-take them.
	rows, err := st.Queries().ClaimDuePeerTubeFollowTargets(ctx, sqlcgen.ClaimDuePeerTubeFollowTargetsParams{LeaseSeconds: 60, PerUserLimit: 100, ClaimLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(many)-perUserClaim {
		t.Fatalf("second claim took %d rows, want %d (the %d in flight are leased)", len(rows), len(many)-perUserClaim, perUserClaim)
	}
	close(f.release)
	<-drained
}

func statusLine(t *testing.T, ctx context.Context, svc *Service, user, job uuid.UUID) string {
	t.Helper()
	s, err := svc.Status(ctx, user, job)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("pending=%d failed=%d done=%d", s.Pending, s.Failed, s.Done)
}

// A blocked target and an unresolvable one must be indistinguishable through
// the status API, attempt for attempt.
func TestBlockedAndUnresolvableLookIdentical(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var a, b uuid.UUID
	for _, u := range []*uuid.UUID{&a, &b} {
		n := uuid.NewString()[:8]
		if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
			"pi-"+n, "pi-"+n+"@example.test").Scan(u); err != nil {
			t.Fatal(err)
		}
		defer func(id uuid.UUID) { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) }(*u)
	}
	svc := &Service{Repo: st.Queries(), MaxAttempts: 3, BackoffBase: time.Millisecond,
		Follower: scriptedFollower{errs: map[string]error{
			"x@blocked.example": federation.ErrBlockedFollowTarget, "x@down.example": federation.ErrRemoteUnresolvable}}}
	qa, _ := svc.Enqueue(ctx, a, []string{"x@blocked.example"})
	qb, _ := svc.Enqueue(ctx, b, []string{"x@down.example"})
	var sa, sb []string
	for i := 0; i < 5; i++ {
		if _, err := st.Pool.Exec(ctx, `UPDATE peertube_follow_import_targets SET next_attempt_at = now() WHERE user_id IN ($1, $2)`, a, b); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		sa = append(sa, statusLine(t, ctx, svc, a, *qa.JobID))
		sb = append(sb, statusLine(t, ctx, svc, b, *qb.JobID))
	}
	for i := range sa {
		if sa[i] != sb[i] {
			t.Fatalf("status sequences differ:\nblocked      %v\nunresolvable %v", sa, sb)
		}
	}
	if sa[len(sa)-1] != "pending=0 failed=1 done=0" || sa[0] != "pending=1 failed=0 done=0" || sa[1] != "pending=1 failed=0 done=0" {
		t.Fatalf("unexpected sequence %v", sa)
	}
}

// The list stores admin entries lower-cased only; a probe must still match an
// IDN entry in either spelling and a host:port entry by its bare host.
func TestBlockListMatchesEquivalentForms(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sfx := uuid.NewString()[:6]
	koelnPuny, _ := federation.NormalizeFollowHost("köln" + sfx + ".example")
	entries := []string{"bücher" + sfx + ".example", koelnPuny, "evil" + sfx + ".example:8443"}
	for _, e := range entries {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO blocked_instances (domain) VALUES ($1)`, e); err != nil {
			t.Fatal(err)
		}
		defer func(d string) {
			_, _ = st.Pool.Exec(context.Background(), `DELETE FROM blocked_instances WHERE domain=$1`, d)
		}(e)
	}
	punyOfFirst, _ := federation.NormalizeFollowHost(entries[0])
	for _, probe := range []string{punyOfFirst, "köln" + sfx + ".example", "evil" + sfx + ".example"} {
		if ok, err := instancemod.BlockMatches(ctx, st.Queries().IsInstanceBlocked, probe); err != nil || !ok {
			t.Errorf("probe %q not blocked (%v)", probe, err)
		}
	}
	if ok, _ := instancemod.BlockMatches(ctx, st.Queries().IsInstanceBlocked, "fine"+sfx+".example"); ok {
		t.Error("unrelated host blocked")
	}
}
