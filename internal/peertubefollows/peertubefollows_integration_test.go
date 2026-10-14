//go:build integration

package peertubefollows

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store"
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
	if n, err := svc.Drain(ctx); err != nil || n != 4 {
		t.Fatalf("drain: %d %v", n, err)
	}
	s1, err := svc.Status(ctx, me, *q.JobID)
	if err != nil || s1.Total != 4 || s1.Done != 1 || s1.SkippedBlocked != 1 || s1.SkippedInvalid != 1 || s1.Pending != 1 {
		t.Fatalf("after first drain: %+v %v", s1, err)
	}
	// down@c is backed off an hour: not due yet.
	if n, _ := svc.Drain(ctx); n != 0 {
		t.Fatalf("backed-off target re-claimed: %d", n)
	}
	// Pull it due; the second failure hits MaxAttempts and dead-letters.
	if _, err := st.Pool.Exec(ctx, `UPDATE peertube_follow_import_targets SET next_attempt_at = now() WHERE import_id = $1`, *q.JobID); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.Drain(ctx); n != 1 {
		t.Fatalf("retry drain: %d", n)
	}
	s2, _ := svc.Status(ctx, me, *q.JobID)
	if s2.Failed != 1 || s2.Pending != 0 {
		t.Fatalf("after retry: %+v", s2)
	}
	if _, err := svc.Status(ctx, other, *q.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user's job must be not-found: %v", err)
	}

	// Federation off: nothing queued, everything counted.
	off := &Service{Repo: st.Queries(), Enabled: func() bool { return false }}
	oq, err := off.Enqueue(ctx, me, []string{"x@d.example", "y@d.example"})
	if err != nil || oq.JobID != nil || oq.SkippedFederation != 2 {
		t.Fatalf("disabled: %+v %v", oq, err)
	}
}
