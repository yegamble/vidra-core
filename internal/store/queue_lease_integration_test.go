//go:build integration

// Integration test: the durable queues' claim semantics against a REAL
// PostgreSQL with migrations applied.
//
// The property under test is the one that makes running more than one
// vidra-core instance safe: TWO CONCURRENT CLAIMERS MUST NEVER RECEIVE THE SAME
// ROW. Before the lease retrofit, federation delivery, ATProto cross-post and
// the search outbox were plain SELECTs — nothing marked a row as taken, so two
// nodes would both read it and both act on it. For federation and ATProto that
// is externally visible (a duplicate activity delivered to every remote server;
// a second Bluesky post on a user's public feed) and a retry cannot undo it.
//
// A fake repository cannot prove this: the guarantee comes from
// FOR UPDATE SKIP LOCKED inside one statement, which is a database behaviour.
//
// Run via `make test-integration`:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/store/...
package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// leaseStore opens the shared integration database.
func leaseStore(t *testing.T) *Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// claimRace runs two claims concurrently and reports the IDs each returned. The
// claims run on SEPARATE connections, because two claims on one connection are
// serialised by the connection itself and would pass regardless.
func claimRace[T comparable](t *testing.T, claim func(context.Context) ([]T, error)) (a, b []T) {
	t.Helper()
	ctx := context.Background()
	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	go func() { defer wg.Done(); a, errA = claim(ctx) }()
	go func() { defer wg.Done(); b, errB = claim(ctx) }()
	wg.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("concurrent claims errored: %v / %v", errA, errB)
	}
	return a, b
}

// assertDisjoint fails when the same id was handed to both claimers.
func assertDisjoint[T comparable](t *testing.T, queue string, a, b []T) {
	t.Helper()
	seen := make(map[T]bool, len(a))
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if seen[id] {
			t.Fatalf("%s: id %v was claimed by BOTH workers — two instances would each act on it", queue, id)
		}
	}
	if len(a)+len(b) == 0 {
		t.Fatalf("%s: neither claimer got anything; the test proved nothing", queue)
	}
}

// TestFederationDeliveryClaimIsExclusive is the sharpest of the three: a
// duplicate delivery is visible to every remote server that receives it.
func TestFederationDeliveryClaimIsExclusive(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	inbox := "https://remote.example/inbox/" + uuid.NewString()
	for range 6 {
		if err := q.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
			InboxUrl: inbox,
			Payload:  []byte(`{"type":"Create"}`),
		}); err != nil {
			t.Fatalf("EnqueueDelivery: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM federation_deliveries WHERE inbox_url = $1`, inbox) })

	claim := func(ctx context.Context) ([]uuid.UUID, error) {
		rows, err := q.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{
			BatchSize: 10, LeaseSeconds: 300,
		})
		if err != nil {
			return nil, err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			if r.InboxUrl == inbox {
				ids = append(ids, r.ID)
			}
		}
		return ids, nil
	}
	a, b := claimRace(t, claim)
	assertDisjoint(t, "federation_deliveries", a, b)

	// And the lease actually holds: an immediate third claim sees nothing of
	// ours, because every row's next_attempt_at was pushed forward.
	again, err := claim(ctx)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a claim immediately after leasing returned %d rows; the lease is not holding", len(again))
	}
}

// TestATProtoPostClaimIsExclusive: a duplicate here is a second post on the
// user's public Bluesky feed.
func TestATProtoPostClaimIsExclusive(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	// Each post row needs a real video, which needs a channel and a user.
	_, channelID, cleanup := seedUserAndChannel(t, st)
	t.Cleanup(cleanup)
	wanted := map[uuid.UUID]bool{}
	for i := range 6 {
		var videoID uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', 'published') RETURNING id`,
			channelID, "lease-race",
		).Scan(&videoID); err != nil {
			t.Fatalf("seed video %d: %v", i, err)
		}
		if err := q.EnqueueATProtoPost(ctx, videoID); err != nil {
			t.Fatalf("EnqueueATProtoPost: %v", err)
		}
		wanted[videoID] = true
	}

	claim := func(ctx context.Context) ([]uuid.UUID, error) {
		rows, err := q.ClaimDueATProtoPosts(ctx, sqlcgen.ClaimDueATProtoPostsParams{
			BatchSize: 10, LeaseSeconds: 300,
		})
		if err != nil {
			return nil, err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			if wanted[r.VideoID] {
				ids = append(ids, r.ID)
			}
		}
		return ids, nil
	}
	a, b := claimRace(t, claim)
	assertDisjoint(t, "atproto_posts", a, b)
}

// TestSearchOutboxClaimIsExclusive: the least harmful of the three (the search
// service dedupes on event_id) but still double the requests for no benefit.
func TestSearchOutboxClaimIsExclusive(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	marker := uuid.NewString()
	for range 6 {
		if err := q.EnqueueSearchEvent(ctx, sqlcgen.EnqueueSearchEventParams{
			EventType: "lease-race-" + marker,
			Payload:   []byte(`{}`),
		}); err != nil {
			t.Fatalf("EnqueueSearchEvent: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM search_outbox WHERE event_type = $1`, "lease-race-"+marker)
	})

	claim := func(ctx context.Context) ([]int64, error) {
		rows, err := q.ClaimDueSearchEvents(ctx, sqlcgen.ClaimDueSearchEventsParams{
			BatchSize: 20, LeaseSeconds: 300,
		})
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			if r.EventType == "lease-race-"+marker {
				ids = append(ids, r.ID)
			}
		}
		return ids, nil
	}
	a, b := claimRace(t, claim)
	assertDisjoint(t, "search_outbox", a, b)
}

// TestExpiredLeaseBecomesClaimableAgain is the recovery half of the contract.
// The lease IS the crash recovery: a worker that dies mid-job leaves a row whose
// next_attempt_at eventually elapses, and it becomes due again on its own. No
// boot-time sweep, and no assumption about which other instances are alive.
func TestExpiredLeaseBecomesClaimableAgain(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	inbox := "https://remote.example/inbox/" + uuid.NewString()
	if err := q.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl: inbox,
		Payload:  []byte(`{"type":"Create"}`),
	}); err != nil {
		t.Fatalf("EnqueueDelivery: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM federation_deliveries WHERE inbox_url = $1`, inbox) })

	mine := func(rows []sqlcgen.ClaimDueDeliveriesRow) int {
		n := 0
		for _, r := range rows {
			if r.InboxUrl == inbox {
				n++
			}
		}
		return n
	}

	// A one-second lease stands in for a worker that claimed and then died.
	rows, err := q.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{BatchSize: 10, LeaseSeconds: 1})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if mine(rows) != 1 {
		t.Fatalf("first claim got %d of our rows, want 1", mine(rows))
	}
	// Immediately: still leased.
	rows, err = q.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{BatchSize: 10, LeaseSeconds: 300})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if mine(rows) != 0 {
		t.Fatalf("the row was re-claimed while its lease was live")
	}
	// After the lease elapses: due again, with no sweeper involved.
	time.Sleep(1200 * time.Millisecond)
	rows, err = q.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{BatchSize: 10, LeaseSeconds: 300})
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if mine(rows) != 1 {
		t.Fatalf("the row did not become claimable after its lease expired; a crashed worker would strand it")
	}
}

// TestStateFlipClaimsAreExclusive covers the six queues that claim by flipping a
// row to 'running' rather than by leasing. Those were `UPDATE ... WHERE id IN
// (SELECT ... LIMIT n)` with no row locking — the classic queue anti-pattern,
// where the ids are chosen before any lock is taken so two concurrent claimers
// can select the same row. FOR UPDATE SKIP LOCKED is what makes them safe.
//
// transcode_jobs stands in for the set: they are the same statement shape, and
// this one is the most expensive to get wrong (two workers encoding the same
// video into the same output prefix, corrupting each other's tree).
func TestStateFlipClaimsAreExclusive(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	_, channelID, cleanup := seedUserAndChannel(t, st)
	t.Cleanup(cleanup)

	wanted := map[uuid.UUID]bool{}
	for i := range 6 {
		var videoID uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'claim-race', 'public', 'published') RETURNING id`,
			channelID,
		).Scan(&videoID); err != nil {
			t.Fatalf("seed video %d: %v", i, err)
		}
		if err := q.EnqueueTranscodeJob(ctx, sqlcgen.EnqueueTranscodeJobParams{
			VideoID:       videoID,
			SourceKey:     "web-videos/" + videoID.String() + ".mp4",
			TranscodeType: "all",
		}); err != nil {
			t.Fatalf("EnqueueTranscodeJob: %v", err)
		}
		wanted[videoID] = true
	}

	claim := func(ctx context.Context) ([]uuid.UUID, error) {
		rows, err := q.ClaimDueTranscodeJobs(ctx, 10)
		if err != nil {
			return nil, err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			if wanted[r.VideoID] {
				ids = append(ids, r.ID)
			}
		}
		return ids, nil
	}
	a, b := claimRace(t, claim)
	assertDisjoint(t, "transcode_jobs", a, b)

	// Every one of our jobs must now be 'running' exactly once — no row left
	// pending because two claimers fought over it, and none claimed twice.
	var running int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM transcode_jobs j JOIN videos v ON v.id = j.video_id
		 WHERE v.channel_id = $1 AND j.state = 'running'`, channelID).Scan(&running); err != nil {
		t.Fatalf("count running: %v", err)
	}
	if running != len(wanted) {
		t.Errorf("%d jobs are running, want all %d claimed exactly once", running, len(wanted))
	}
}
