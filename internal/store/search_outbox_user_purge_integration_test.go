//go:build integration

// Integration test: per-user search_outbox erasure (PurgeUserSearchOutbox)
// against a REAL PostgreSQL with migrations applied. Every rule being proven
// here is a SQL rule — which rows the subject predicate selects, which event
// types the exclusion spares, and whether migration 0123's partial index is
// actually usable by the query — so none of them can be proven against a fake.
// Run via:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/store/...
package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/store"
)

// seedOutboxPayload inserts one outbox row with an explicit event_type and raw
// JSONB payload, returning its id. Unlike seedOutboxRow (the retention suite's
// helper) the payload is the point here, not the age.
func seedOutboxPayload(t *testing.T, st *store.Store, eventType, payload string) int64 {
	t.Helper()
	var id int64
	if err := st.Pool.QueryRow(context.Background(),
		`INSERT INTO search_outbox (event_type, payload) VALUES ($1, $2::jsonb) RETURNING id`,
		eventType, payload,
	).Scan(&id); err != nil {
		t.Fatalf("seed %s row: %v", eventType, err)
	}
	return id
}

// TestPurgeUserSearchOutboxErasesOnlyThatUsersData is the privacy limb and its
// blast radius in one test: the erasure must actually remove the requester's
// data-bearing rows from core's PRIMARY database (the defect: "This permanently
// removes every search you have made on this instance" was proxied to
// vidra-search and left core's copy of the raw query text untouched), and it
// must remove NOTHING belonging to anyone else. A purge that over-deletes is
// worse than one that under-deletes: the second is a broken promise, the first
// is silent data loss for an uninvolved user.
func TestPurgeUserSearchOutboxErasesOnlyThatUsersData(t *testing.T) {
	st := pruneStore(t)
	q := st.Queries()
	ctx := context.Background()

	subject := uuid.New()
	other := uuid.New()
	marker := "purge-scope-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM search_outbox WHERE payload->>'marker' = $1`, marker)
	})

	// The subject's own behavioural rows — these carry the raw query text.
	mine := seedOutboxPayload(t, st, searchevents.TypeSearchSubmitted,
		`{"marker":"`+marker+`","query":"a query only the subject typed","user_id":"`+subject.String()+`"}`)
	mineImpression := seedOutboxPayload(t, st, searchevents.TypeVideoImpression,
		`{"marker":"`+marker+`","video_id":"`+uuid.NewString()+`","user_id":"`+subject.String()+`"}`)
	mineWatch := seedOutboxPayload(t, st, searchevents.TypeVideoWatchProg,
		`{"marker":"`+marker+`","position_seconds":12,"user_id":"`+subject.String()+`"}`)

	// Another signed-in user's identical-shaped row.
	theirs := seedOutboxPayload(t, st, searchevents.TypeSearchSubmitted,
		`{"marker":"`+marker+`","query":"someone else's search","user_id":"`+other.String()+`"}`)
	// An anonymous row: no user_id at all, only the derived aggregation subject.
	anon := seedOutboxPayload(t, st, searchevents.TypeSearchSubmitted,
		`{"marker":"`+marker+`","query":"anonymous search","subject_id":"abc123"}`)
	// A catalogue document naming the subject as owner_id NESTED under "video".
	// Erasing a creator's search documents because they cleared their own search
	// history would silently deindex their videos.
	doc := seedOutboxPayload(t, st, searchevents.TypeVideoUpsert,
		`{"marker":"`+marker+`","video":{"id":"`+uuid.NewString()+`","owner_id":"`+subject.String()+`"}}`)

	n, err := q.PurgeUserSearchOutbox(ctx, subject.String())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 3 {
		t.Errorf("purge deleted %d rows, want exactly the subject's 3 data-bearing rows", n)
	}
	for _, id := range []int64{mine, mineImpression, mineWatch} {
		if outboxRowExists(t, st, id) {
			t.Errorf("row %d survived: the subject's search data is still in core's primary database after an erasure", id)
		}
	}
	if !outboxRowExists(t, st, theirs) {
		t.Error("ANOTHER USER's search row was deleted by this user's erasure")
	}
	if !outboxRowExists(t, st, anon) {
		t.Error("an anonymous row (no user_id) was deleted by a per-user erasure")
	}
	if !outboxRowExists(t, st, doc) {
		t.Error("a video.upsert naming the subject as nested owner_id was deleted — clearing search history must not deindex the user's videos")
	}
}

// TestPurgeUserSearchOutboxSparesThePurgeEvents is the subtle one. The outbox
// carries two KINDS of row naming a user at the top level: data ABOUT them, and
// the user.suppress / user.history_deleted events whose DELIVERY performs the
// erasure downstream in vidra-search. Purging the second kind alongside the
// first cancels the erasure it was queued to complete — silently, because a
// pending outbox row has no second copy and nothing downstream notices one that
// vanished.
//
// The events are seeded PENDING here on purpose: that is the state in which
// deleting one loses real work, and it is the state an event queued moments
// earlier by the same clear (or by a previous clear that has not drained) is in.
func TestPurgeUserSearchOutboxSparesThePurgeEvents(t *testing.T) {
	st := pruneStore(t)
	q := st.Queries()
	ctx := context.Background()

	subject := uuid.New()
	marker := "purge-events-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM search_outbox WHERE payload->>'marker' = $1`, marker)
	})

	data := seedOutboxPayload(t, st, searchevents.TypeSearchSubmitted,
		`{"marker":"`+marker+`","query":"erase me","user_id":"`+subject.String()+`"}`)

	// Driven off the Go constants, not string literals: renaming a constant
	// without updating the SQL exclusion list has to fail here rather than
	// silently start cancelling erasures in production.
	spared := map[string]int64{}
	for _, et := range []string{searchevents.TypeUserSuppress, searchevents.TypeUserHistoryDel} {
		spared[et] = seedOutboxPayload(t, st, et,
			`{"marker":"`+marker+`","user_id":"`+subject.String()+`","scope":"search","unlisted":true}`)
	}

	if _, err := q.PurgeUserSearchOutbox(ctx, subject.String()); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if outboxRowExists(t, st, data) {
		t.Error("the subject's data row survived the purge")
	}
	for et, id := range spared {
		if !outboxRowExists(t, st, id) {
			t.Errorf("%s (row %d) was deleted by the purge: the pending instruction that performs this very erasure in vidra-search is now lost, and nothing downstream will ever notice", et, id)
		}
	}
	// Twice must be safe: the second run finds no data rows and STILL leaves the
	// instructions alone. An erasure is retried whenever a request is retried.
	n, err := q.PurgeUserSearchOutbox(ctx, subject.String())
	if err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if n != 0 {
		t.Errorf("second purge deleted %d rows, want 0 (it must be idempotent)", n)
	}
	for et, id := range spared {
		if !outboxRowExists(t, st, id) {
			t.Errorf("%s (row %d) was deleted by the SECOND purge", et, id)
		}
	}
}

// TestPurgeUserSearchOutboxUsesTheUserIndex checks the claim migration 0123's
// comment makes: PostgreSQL proves `payload->>'user_id' = $1` implies
// `payload->>'user_id' IS NOT NULL` from the strictness of `=`, so the query's
// plain equality matches the PARTIAL index without the query restating the
// predicate. If that implication did not hold the index would be dead weight on
// the insert path and the erasure would be a sequential scan over ninety days of
// behavioural events on the request path.
//
// enable_seqscan is disabled for the probe because the test table is small
// enough that a scan is genuinely cheaper; the question being asked is whether
// the planner CAN use the index, not whether it prefers to on ten rows.
func TestPurgeUserSearchOutboxUsesTheUserIndex(t *testing.T) {
	st := pruneStore(t)
	ctx := context.Background()

	// The probe runs inside an explicit transaction that is rolled back: SET
	// LOCAL is scoped to a transaction, and pgx wraps a bare Exec in an implicit
	// one that ends immediately — so setting it outside a Begin silently does
	// nothing and the plan reverts to whatever the planner prefers on a table
	// this small.
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	rows, err := tx.Query(ctx,
		`EXPLAIN DELETE FROM search_outbox
		 WHERE payload->>'user_id' = $1
		   AND event_type <> 'user.suppress'
		   AND event_type <> 'user.history_deleted'`, uuid.NewString())
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	if !strings.Contains(plan.String(), "search_outbox_user_purge_idx") {
		t.Errorf("the erasure cannot use migration 0123's partial index; plan was:\n%s", plan.String())
	}
}
