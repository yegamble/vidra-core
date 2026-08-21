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
	"bytes"
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

// assertClaimIDsAscending fails when ids are not in ascending byte order. That
// is the order PostgreSQL sorts the uuid type in, so it is the order a claim
// whose rows all tie on created_at must return them in.
func assertClaimIDsAscending(t *testing.T, queue string, ids []uuid.UUID) {
	t.Helper()
	for i := 1; i < len(ids); i++ {
		if bytes.Compare(ids[i][:], ids[i-1][:]) < 0 {
			t.Errorf("%s: claim is not ordered by (created_at, id) — %s came back before %s",
				queue, ids[i-1], ids[i])
		}
	}
}

// assertTiedCreatedAt fails when the seeded rows did NOT end up sharing one
// created_at. Without the tie the ordering test proves nothing: created_at
// alone already orders rows that have distinct created_at values.
func assertTiedCreatedAt(t *testing.T, st *Store, query string, arg any) {
	t.Helper()
	var distinct int
	if err := st.Pool.QueryRow(context.Background(), query, arg).Scan(&distinct); err != nil {
		t.Fatalf("count distinct created_at: %v", err)
	}
	if distinct != 1 {
		t.Fatalf("seeded rows have %d distinct created_at values, want 1; the tie the test needs did not happen", distinct)
	}
}

// TestClaimReturnOrderIsATotalOrder covers the OTHER half of the claim
// contract: not just WHICH rows a claimer gets, but the order it gets them in.
//
// federation_deliveries and atproto_posts sorted the claim by created_at alone.
// created_at defaults to now(), and now() is TRANSACTION-fixed: every row a
// fan-out enqueues inside one transaction carries a byte-identical created_at,
// so ORDER BY created_at leaves them tied and PostgreSQL may emit tied rows in
// any order it likes — a different one on each claim. Both consumers apply what
// they are handed last-write-wins (a Delete overtaking its Update leaves the
// remote server showing a deleted object; the reverse resurrects it), so the
// return order IS the behaviour, and it has to be deterministic.
//
// The tiebreak is id, and it has to BE the tiebreak rather than the whole sort:
// unlike search_outbox's BIGSERIAL id, these two tables' ids are random
// uuid_generate_v4() values, so ORDER BY id alone would be a total order over
// an order that means nothing.
//
// A fake repository cannot prove this: which order a sort emits tied rows in is
// a database behaviour.
func TestClaimReturnOrderIsATotalOrder(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	const seeded = 8

	// --- federation_deliveries ---
	inbox := "https://remote.example/inbox/" + uuid.NewString()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM federation_deliveries WHERE inbox_url = $1`, inbox) })

	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for range seeded {
		if _, err := tx.Exec(ctx,
			`INSERT INTO federation_deliveries (inbox_url, payload) VALUES ($1, $2)`,
			inbox, []byte(`{"type":"Create"}`),
		); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("enqueue delivery in tx: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit deliveries: %v", err)
	}
	assertTiedCreatedAt(t, st,
		`SELECT count(DISTINCT created_at) FROM federation_deliveries WHERE inbox_url = $1`, inbox)

	rows, err := q.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{BatchSize: 500, LeaseSeconds: 300})
	if err != nil {
		t.Fatalf("ClaimDueDeliveries: %v", err)
	}
	deliveryIDs := make([]uuid.UUID, 0, seeded)
	for _, r := range rows {
		if r.InboxUrl == inbox {
			deliveryIDs = append(deliveryIDs, r.ID)
		}
	}
	// A concurrently-running package may have claimed some of ours; a subset of
	// an ordered sequence is still ordered, so the assertion holds either way.
	if len(deliveryIDs) < 2 {
		t.Fatalf("claimed %d of our %d deliveries; too few to prove an ordering", len(deliveryIDs), seeded)
	}
	if len(deliveryIDs) < seeded {
		t.Logf("claimed %d of %d deliveries (a concurrent claimer took the rest)", len(deliveryIDs), seeded)
	}
	assertClaimIDsAscending(t, "federation_deliveries", deliveryIDs)

	// --- atproto_posts: same tie, same tiebreak. Each queued post needs a real
	// video (video_id is UNIQUE and FKs to videos).
	_, channelID, cleanup := seedUserAndChannel(t, st)
	t.Cleanup(cleanup)

	videoIDs := make([]uuid.UUID, 0, seeded)
	tx, err = st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for range seeded {
		var videoID uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'claim-order', 'public', 'published') RETURNING id`,
			channelID,
		).Scan(&videoID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed video in tx: %v", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO atproto_posts (video_id) VALUES ($1)`, videoID); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("enqueue post in tx: %v", err)
		}
		videoIDs = append(videoIDs, videoID)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit posts: %v", err)
	}
	assertTiedCreatedAt(t, st,
		`SELECT count(DISTINCT created_at) FROM atproto_posts WHERE video_id = ANY($1)`, videoIDs)

	mineVideo := make(map[uuid.UUID]bool, len(videoIDs))
	for _, id := range videoIDs {
		mineVideo[id] = true
	}
	posts, err := q.ClaimDueATProtoPosts(ctx, sqlcgen.ClaimDueATProtoPostsParams{BatchSize: 500, LeaseSeconds: 300})
	if err != nil {
		t.Fatalf("ClaimDueATProtoPosts: %v", err)
	}
	postIDs := make([]uuid.UUID, 0, seeded)
	for _, r := range posts {
		if mineVideo[r.VideoID] {
			postIDs = append(postIDs, r.ID)
		}
	}
	if len(postIDs) < 2 {
		t.Fatalf("claimed %d of our %d posts; too few to prove an ordering", len(postIDs), seeded)
	}
	if len(postIDs) < seeded {
		t.Logf("claimed %d of %d posts (a concurrent claimer took the rest)", len(postIDs), seeded)
	}
	assertClaimIDsAscending(t, "atproto_posts", postIDs)
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

// TestLeaseRenewAndSweepCycle is the crash-recovery contract that replaced the
// boot-time blanket requeue.
//
// The old shape requeued EVERY row in 'running' at start-up, which was safe only
// while the process doing it was the deployment's only worker. A second instance
// booting would have requeued jobs the first was actively running. The lease
// makes the distinction real:
//
//   - a worker that keeps renewing keeps its job (nobody may take it);
//   - a worker that stops renewing loses it to the sweep (anybody may take it).
//
// Both halves are asserted, because only having the second is how you build a
// recovery mechanism that steals live work.
func TestLeaseRenewAndSweepCycle(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	_, channelID, cleanup := seedUserAndChannel(t, st)
	t.Cleanup(cleanup)

	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'lease-cycle', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if err := q.EnqueueTranscodeJob(ctx, sqlcgen.EnqueueTranscodeJobParams{
		VideoID: videoID, SourceKey: "web-videos/x.mp4", TranscodeType: "all",
	}); err != nil {
		t.Fatalf("EnqueueTranscodeJob: %v", err)
	}

	rows, err := q.ClaimDueTranscodeJobs(ctx, 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var jobID uuid.UUID
	for _, r := range rows {
		if r.VideoID == videoID {
			jobID = r.ID
		}
	}
	if jobID == uuid.Nil {
		t.Fatal("our job was not claimed")
	}

	state := func() (string, time.Time) {
		t.Helper()
		var s string
		var next time.Time
		if err := st.Pool.QueryRow(ctx,
			`SELECT state, next_attempt_at FROM transcode_jobs WHERE id = $1`, jobID).Scan(&s, &next); err != nil {
			t.Fatalf("read job: %v", err)
		}
		return s, next
	}

	// The claim must have taken a lease: a running row is not due.
	gotState, leaseUntil := state()
	if gotState != "running" {
		t.Fatalf("state after claim = %q, want running", gotState)
	}
	if !leaseUntil.After(time.Now()) {
		t.Fatal("claim did not push next_attempt_at forward; the row is immediately sweepable")
	}

	// A live worker's job survives a sweep.
	if _, err := q.SweepExpiredTranscodeJobs(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if gotState, _ = state(); gotState != "running" {
		t.Fatalf("the sweep requeued a job whose lease is still live (state=%q) — "+
			"that is exactly the multi-node bug the lease exists to prevent", gotState)
	}

	// Renewal pushes the lease further out.
	if err := q.RenewTranscodeJobLease(ctx, jobID); err != nil {
		t.Fatalf("renew: %v", err)
	}
	_, renewed := state()
	if !renewed.After(leaseUntil) {
		t.Errorf("renew did not extend the lease (%v -> %v)", leaseUntil, renewed)
	}

	// Simulate the worker dying: expire the lease by hand, then sweep.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE transcode_jobs SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	n, err := q.SweepExpiredTranscodeJobs(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n < 1 {
		t.Fatal("the sweep returned nothing for an expired lease; a crashed worker would strand the job forever")
	}
	if gotState, _ = state(); gotState != "pending" {
		t.Errorf("state after sweep = %q, want pending", gotState)
	}

	// And the attempt counter advanced, so a job that kills its worker every time
	// dead-letters through the normal path instead of looping forever.
	var attempts int32
	if err := st.Pool.QueryRow(ctx, `SELECT attempts FROM transcode_jobs WHERE id = $1`, jobID).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts < 1 {
		t.Errorf("attempts = %d after a sweep, want >= 1 so a poisonous job cannot loop forever", attempts)
	}
}

// TestRenewCannotReviveAFinishedJob guards the state predicate on the renew
// query. Without it a late heartbeat from a worker that has already completed
// (or dead-lettered) its job would push a terminal row's due time around.
func TestRenewCannotReviveAFinishedJob(t *testing.T) {
	st := leaseStore(t)
	q := st.Queries()
	ctx := context.Background()

	_, channelID, cleanup := seedUserAndChannel(t, st)
	t.Cleanup(cleanup)

	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'lease-revive', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if err := q.EnqueueTranscodeJob(ctx, sqlcgen.EnqueueTranscodeJobParams{
		VideoID: videoID, SourceKey: "web-videos/y.mp4", TranscodeType: "all",
	}); err != nil {
		t.Fatalf("EnqueueTranscodeJob: %v", err)
	}
	rows, err := q.ClaimDueTranscodeJobs(ctx, 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var jobID uuid.UUID
	for _, r := range rows {
		if r.VideoID == videoID {
			jobID = r.ID
		}
	}
	if jobID == uuid.Nil {
		t.Fatal("our job was not claimed")
	}
	if err := q.CompleteTranscodeJob(ctx, jobID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	var before time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT next_attempt_at FROM transcode_jobs WHERE id = $1`, jobID).Scan(&before); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := q.RenewTranscodeJobLease(ctx, jobID); err != nil {
		t.Fatalf("renew: %v", err)
	}
	var after time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT next_attempt_at FROM transcode_jobs WHERE id = $1`, jobID).Scan(&after); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !after.Equal(before) {
		t.Errorf("renew moved a completed job's lease (%v -> %v); it must only touch running rows", before, after)
	}
}
