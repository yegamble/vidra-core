//go:build integration

// Integration test: the search_outbox durable queue (migration 0092, search-
// service W4) against a REAL PostgreSQL with migrations applied. Proves the
// enqueue → claim-due → mark-delivered / reschedule / dead-letter cycle and the
// partial-index claim ordering. Run via `make test-integration`:
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

	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func connectOutbox(t *testing.T) *sqlcgen.Queries {
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
	return st.Queries()
}

func TestSearchOutboxRoundTrip(t *testing.T) {
	q := connectOutbox(t)
	ctx := context.Background()

	// Enqueue three pending events.
	for i := 0; i < 3; i++ {
		if err := q.EnqueueSearchEvent(ctx, sqlcgen.EnqueueSearchEventParams{
			EventType: "video.upsert",
			Payload:   []byte(`{"marker":"outbox_roundtrip"}`),
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	// Claim due events (oldest first). event_id defaults are populated by the DB.
	due, err := q.ClaimDueSearchEvents(ctx, 100)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var mine []sqlcgen.ClaimDueSearchEventsRow
	for _, r := range due {
		if string(r.Payload) == `{"marker": "outbox_roundtrip"}` || string(r.Payload) == `{"marker":"outbox_roundtrip"}` {
			mine = append(mine, r)
		}
	}
	if len(mine) < 3 {
		t.Fatalf("claimed %d of our events, want >=3", len(mine))
	}
	for _, r := range mine {
		if r.EventID.String() == "00000000-0000-0000-0000-000000000000" {
			t.Errorf("event_id not defaulted for row %d", r.ID)
		}
	}
	// Claim ordering is by id ascending.
	for i := 1; i < len(mine); i++ {
		if mine[i].ID < mine[i-1].ID {
			t.Errorf("claim not id-ordered: %d before %d", mine[i-1].ID, mine[i].ID)
		}
	}

	// Deliver the first, reschedule the second (into the future → not due),
	// dead-letter the third.
	if err := q.MarkSearchEventDelivered(ctx, mine[0].ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	if err := q.RescheduleSearchEvent(ctx, sqlcgen.RescheduleSearchEventParams{
		ID: mine[1].ID, NextAttemptAt: time.Now().Add(time.Hour), LastError: "retry later",
	}); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if err := q.DeadLetterSearchEvent(ctx, sqlcgen.DeadLetterSearchEventParams{
		ID: mine[2].ID, LastError: "gave up",
	}); err != nil {
		t.Fatalf("dead-letter: %v", err)
	}

	// A fresh claim must not re-return the delivered, rescheduled, or dead rows.
	after, err := q.ClaimDueSearchEvents(ctx, 100)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	seen := map[int64]bool{}
	for _, r := range after {
		seen[r.ID] = true
	}
	if seen[mine[0].ID] || seen[mine[1].ID] || seen[mine[2].ID] {
		t.Errorf("delivered/rescheduled/dead row re-claimed: %v", seen)
	}

	// Depth reports at least our dead row.
	depths, err := q.SearchOutboxDepth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	var haveDead bool
	for _, d := range depths {
		if d.State == "dead" && d.Depth >= 1 {
			haveDead = true
		}
	}
	if !haveDead {
		t.Errorf("depth did not report a dead row: %+v", depths)
	}
}
