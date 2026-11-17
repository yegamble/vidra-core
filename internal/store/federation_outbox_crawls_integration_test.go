//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The outbox-read queue (0190): queueing is idempotent and honours the
// cooldown, a claim leases the row, and a retry comes due at its own time
// carrying its attempts.
func TestOutboxCrawlQueueLeasesAndCoolsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	actor := "https://crawl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + ".example/video-channels/films"
	enqueue := func(cooldownSeconds int32) {
		t.Helper()
		if err := q.EnqueueOutboxCrawl(ctx, sqlcgen.EnqueueOutboxCrawlParams{ActorUrl: actor, CooldownSeconds: cooldownSeconds}); err != nil {
			t.Fatalf("EnqueueOutboxCrawl: %v", err)
		}
	}
	// claim returns this test's row when it was among the due reads claimed.
	claim := func() (sqlcgen.ClaimDueOutboxCrawlsRow, bool) {
		t.Helper()
		rows, err := q.ClaimDueOutboxCrawls(ctx, sqlcgen.ClaimDueOutboxCrawlsParams{BatchSize: 1000, LeaseSeconds: 600})
		if err != nil {
			t.Fatalf("ClaimDueOutboxCrawls: %v", err)
		}
		for _, r := range rows {
			if r.ActorUrl == actor {
				return r, true
			}
		}
		return sqlcgen.ClaimDueOutboxCrawlsRow{}, false
	}
	settle := func(state string, next time.Time) {
		t.Helper()
		if err := q.SettleOutboxCrawl(ctx, sqlcgen.SettleOutboxCrawlParams{ActorUrl: actor, State: state, NextAttemptAt: next, LastError: "boom"}); err != nil {
			t.Fatalf("SettleOutboxCrawl: %v", err)
		}
	}

	enqueue(3600)
	enqueue(3600) // a second follower of the same channel
	if r, ok := claim(); !ok || r.Attempts != 0 {
		t.Fatalf("first claim = %+v, %v; want the queued read", r, ok)
	}
	if _, ok := claim(); ok {
		t.Fatal("a leased read was claimed again")
	}

	settle("done", time.Now())
	enqueue(3600)
	if _, ok := claim(); ok {
		t.Fatal("a read that finished inside the cooldown was queued again")
	}
	enqueue(0)
	if r, ok := claim(); !ok || r.Attempts != 0 {
		t.Fatalf("claim after the cooldown = %+v, %v; want a fresh read", r, ok)
	}

	settle("pending", time.Now().Add(time.Hour))
	if _, ok := claim(); ok {
		t.Fatal("a retry was claimed before its time")
	}
	settle("pending", time.Now().Add(-time.Second))
	if r, ok := claim(); !ok || r.Attempts != 2 {
		t.Fatalf("claim of a due retry = %+v, %v; want it with 2 attempts", r, ok)
	}
}
