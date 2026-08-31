//go:build integration

// Integration test: search_outbox retention (PruneSearchOutbox) against a REAL
// PostgreSQL with migrations applied. The rules being proven are SQL rules — the
// state guard that makes an undelivered row undeletable, and the batch bound —
// so they are proven here rather than against a fake. Run via:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/store/...
package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func pruneStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// seedOutboxRow inserts one row with an explicit state and age. created_at is
// backdated far past any cutoff a concurrent test could use, so this test's
// assertions are unaffected by rows other suites leave behind (theirs are
// created at now()).
func seedOutboxRow(t *testing.T, st *store.Store, eventType, state string, age time.Duration) int64 {
	t.Helper()
	var id int64
	err := st.Pool.QueryRow(context.Background(),
		`INSERT INTO search_outbox (event_type, payload, state, created_at)
		 VALUES ($1, '{}'::jsonb, $2, now() - $3::interval) RETURNING id`,
		eventType, state, age.String(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed %s row: %v", state, err)
	}
	return id
}

func outboxRowExists(t *testing.T, st *store.Store, id int64) bool {
	t.Helper()
	var n int
	if err := st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM search_outbox WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("exists(%d): %v", id, err)
	}
	return n > 0
}

const (
	pruneTestOld     = 400 * 24 * time.Hour // well past the cutoff
	pruneTestAncient = 4000 * 24 * time.Hour
	pruneTestFresh   = 10 * 24 * time.Hour // well inside the cutoff
)

// TestSearchOutboxPruneRetention proves the state-aware window: delivered rows
// past the cutoff go, rows inside it stay, and a pending row is undeletable at
// ANY age — deleting one would silently lose an index mutation or a
// privacy-critical purge event that has not been delivered yet.
func TestSearchOutboxPruneRetention(t *testing.T) {
	st := pruneStore(t)
	q := st.Queries()
	ctx := context.Background()

	marker := "prune-retention-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM search_outbox WHERE event_type = $1`, marker)
	})

	oldDelivered := seedOutboxRow(t, st, marker, "delivered", pruneTestOld)
	freshDelivered := seedOutboxRow(t, st, marker, "delivered", pruneTestFresh)
	oldPending := seedOutboxRow(t, st, marker, "pending", pruneTestOld)
	ancientPending := seedOutboxRow(t, st, marker, "pending", pruneTestAncient)
	oldDead := seedOutboxRow(t, st, marker, "dead", pruneTestOld)

	cutoff := time.Now().UTC().Add(-365 * 24 * time.Hour)

	n, err := q.PruneSearchOutbox(ctx, sqlcgen.PruneSearchOutboxParams{
		State: "delivered", Cutoff: cutoff, BatchSize: 100,
	})
	if err != nil {
		t.Fatalf("prune delivered: %v", err)
	}
	if n < 1 {
		t.Errorf("prune delivered deleted %d rows, want at least our expired one", n)
	}
	if outboxRowExists(t, st, oldDelivered) {
		t.Error("expired delivered row survived the prune")
	}
	if !outboxRowExists(t, st, freshDelivered) {
		t.Error("delivered row INSIDE the retention window was deleted")
	}
	if !outboxRowExists(t, st, oldPending) || !outboxRowExists(t, st, ancientPending) {
		t.Error("a pending row was deleted by the delivered prune")
	}
	if !outboxRowExists(t, st, oldDead) {
		t.Error("a dead row was deleted by the delivered prune")
	}

	// The dead window is a SEPARATE call with its own cutoff (the forensic
	// floor); with the same cutoff the expired dead row goes too.
	if _, err := q.PruneSearchOutbox(ctx, sqlcgen.PruneSearchOutboxParams{
		State: "dead", Cutoff: cutoff, BatchSize: 100,
	}); err != nil {
		t.Fatalf("prune dead: %v", err)
	}
	if outboxRowExists(t, st, oldDead) {
		t.Error("expired dead row survived the dead prune")
	}
	if !outboxRowExists(t, st, oldPending) || !outboxRowExists(t, st, ancientPending) {
		t.Error("a pending row was deleted by the dead prune")
	}
}

// TestSearchOutboxPrunePendingIsUndeletable is the structural half of the same
// guarantee: even a caller that explicitly asks to prune 'pending' deletes
// nothing, because the query carries `AND state <> 'pending'`. The Go worker
// never asks — this proves the SQL would refuse if it did.
func TestSearchOutboxPrunePendingIsUndeletable(t *testing.T) {
	st := pruneStore(t)
	q := st.Queries()
	ctx := context.Background()

	marker := "prune-pending-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM search_outbox WHERE event_type = $1`, marker)
	})
	ancient := seedOutboxRow(t, st, marker, "pending", pruneTestAncient)

	n, err := q.PruneSearchOutbox(ctx, sqlcgen.PruneSearchOutboxParams{
		State: "pending", Cutoff: time.Now().UTC().Add(time.Hour), BatchSize: 100,
	})
	if err != nil {
		t.Fatalf("prune pending: %v", err)
	}
	if n != 0 {
		t.Errorf("prune deleted %d pending rows, want 0 at any age", n)
	}
	if !outboxRowExists(t, st, ancient) {
		t.Fatal("an undelivered outbox row was deleted: an index mutation or purge event is now lost")
	}
}

// TestSearchOutboxPruneIsBatched: the query deletes at most batch_size rows, so
// a years-old backlog converges over repeated bounded deletes instead of one
// unbounded DELETE that locks the queue against the drainer. A pass after
// convergence is a no-op.
func TestSearchOutboxPruneIsBatched(t *testing.T) {
	st := pruneStore(t)
	q := st.Queries()
	ctx := context.Background()

	marker := "prune-batch-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM search_outbox WHERE event_type = $1`, marker)
	})
	const seeded = 25
	for i := 0; i < seeded; i++ {
		seedOutboxRow(t, st, marker, "delivered", pruneTestOld)
	}

	cutoff := time.Now().UTC().Add(-365 * 24 * time.Hour)
	var passes []int64
	total := int64(0)
	for i := 0; i < 10; i++ {
		n, err := q.PruneSearchOutbox(ctx, sqlcgen.PruneSearchOutboxParams{
			State: "delivered", Cutoff: cutoff, BatchSize: 10,
		})
		if err != nil {
			t.Fatalf("prune pass %d: %v", i, err)
		}
		passes = append(passes, n)
		total += n
		if n == 0 {
			break
		}
		if n > 10 {
			t.Fatalf("pass %d deleted %d rows, want at most the batch size 10", i, n)
		}
	}
	if total < seeded {
		t.Errorf("converged after deleting %d of %d seeded rows (passes: %v)", total, seeded, passes)
	}
	if last := passes[len(passes)-1]; last != 0 {
		t.Errorf("prune never converged: last pass deleted %d (passes: %v)", last, passes)
	}

	var left int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM search_outbox WHERE event_type = $1`, marker).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d seeded rows survived the batched prune", left)
	}
}
