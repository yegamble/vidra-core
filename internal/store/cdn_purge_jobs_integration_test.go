//go:build integration

// Integration test: the CDN purge queue's SQL against a REAL PostgreSQL with
// migration 0137 applied.
//
// Three of its properties are database behaviours that no fake can prove, and
// each one is load-bearing rather than incidental:
//
//   - THE PARTIAL UNIQUE INDEX AND ITS ON CONFLICT INFERENCE. Enqueueing the
//     downloads-revocation walk relies on Postgres matching
//     `ON CONFLICT (kind) WHERE kind = 'downloads_revoked' AND state IN
//     ('pending','running')` to the index of the same predicate. If the
//     inference ever failed to match, the statement would ERROR rather than
//     no-op — turning an admin's second click on the downloads toggle into a
//     500 — and a fake repository would keep passing.
//   - THE CLAIM ADMITS AN EXPIRED 'running' ROW. next_attempt_at IS the lease
//     here, which is what makes a catalogue walk resume after the worker
//     holding it was killed. That is one UPDATE ... WHERE state IN
//     ('pending','running') AND next_attempt_at <= now(), and it is the one
//     difference from the account_exports queue this table is otherwise a copy
//     of.
//   - FOR UPDATE SKIP LOCKED. Two concurrent claimers must never receive the
//     same row, or two instances issue the same purge fan-out at the same edge.
//
// Run via `make test-integration`:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/store/...
package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// purgeStore opens the shared integration database and clears the queue, so a
// re-run of the suite starts from a known depth. The table is process-wide
// operational state with no per-test scoping available, which is why it is
// truncated rather than filtered.
func purgeStore(t *testing.T) *Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(context.Background(), "DELETE FROM cdn_purge_jobs"); err != nil {
		t.Fatalf("clear cdn_purge_jobs: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), "DELETE FROM cdn_purge_jobs")
	})
	return st
}

func purgeURLs(t *testing.T, paths ...string) []byte {
	t.Helper()
	blob, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// TestCDNPurgeWalkIsSingletonWhileActive proves the ON CONFLICT inference lands
// on 0137's partial unique index: a second enqueue while one walk is pending or
// running returns pgx.ErrNoRows (the DO NOTHING no-op) rather than erroring or
// inserting a duplicate.
func TestCDNPurgeWalkIsSingletonWhileActive(t *testing.T) {
	st := purgeStore(t)
	q := st.Queries()
	ctx := context.Background()

	first, err := q.EnqueueCDNPurgeDownloadsRevoked(ctx)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	if _, err := q.EnqueueCDNPurgeDownloadsRevoked(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second enqueue while pending = %v, want pgx.ErrNoRows (the DO NOTHING no-op)", err)
	}

	// Claiming makes it 'running', which the index still covers.
	claimed, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 600, ClaimLimit: 10})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != first {
		t.Fatalf("claimed %d rows, want the walk %s", len(claimed), first)
	}
	if _, err := q.EnqueueCDNPurgeDownloadsRevoked(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("enqueue while running = %v, want pgx.ErrNoRows", err)
	}

	// Once it is done the index no longer covers it, and a NEW revocation may
	// start — which is the behaviour an operator who reopens and recloses
	// downloads depends on.
	if err := q.CompleteCDNPurgeJob(ctx, sqlcgen.CompleteCDNPurgeJobParams{ID: first, Purged: 0}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	second, err := q.EnqueueCDNPurgeDownloadsRevoked(ctx)
	if err != nil {
		t.Fatalf("enqueue after completion: %v", err)
	}
	if second == first {
		t.Fatal("the second walk reused the finished row's id")
	}
}

// TestCDNPurgeClaimReclaimsAnExpiredLease is the resumability guarantee: a
// worker killed mid-walk leaves a 'running' row, and the next claim must take it
// back with its cursor intact rather than leaving it stranded forever.
func TestCDNPurgeClaimReclaimsAnExpiredLease(t *testing.T) {
	st := purgeStore(t)
	q := st.Queries()
	ctx := context.Background()

	id, err := q.EnqueueCDNPurgeDownloadsRevoked(ctx)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// A one-second lease stands in for the shipped ten minutes.
	claimed, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 1, ClaimLimit: 10})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = (%d rows, %v)", len(claimed), err)
	}
	// Still leased: a second claimer must not take it.
	again, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 1, ClaimLimit: 10})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("a live lease was claimable by a second worker (%d rows)", len(again))
	}

	// A page finished, so the row records its progress. It goes back to
	// 'pending' here — that is the ordinary path — but the reclaim below is
	// about the OTHER path, where the worker never got this far.
	cursor := uuid.New()
	if err := q.AdvanceCDNPurgeWalk(ctx, sqlcgen.AdvanceCDNPurgeWalkParams{
		ID: id, CursorVideoID: pgUUIDVal(cursor), Purged: 7, BatchComplete: false,
	}); err != nil {
		t.Fatalf("advance: %v", err)
	}

	// THE WORKER DIES MID-PAGE: claim it again (leaving it 'running') and never
	// finish. Only the lease can bring it back, which is the property this whole
	// table's one deviation from account_exports exists for.
	held, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 1, ClaimLimit: 10})
	if err != nil || len(held) != 1 {
		t.Fatalf("re-claim = (%d rows, %v)", len(held), err)
	}
	if stranded, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 1, ClaimLimit: 10}); err != nil || len(stranded) != 0 {
		t.Fatalf("a 'running' row inside its lease was claimable (%d rows, %v)", len(stranded), err)
	}
	time.Sleep(1200 * time.Millisecond)
	resumed, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 60, ClaimLimit: 10})
	if err != nil || len(resumed) != 1 {
		t.Fatalf("a 'running' row past its lease was NOT reclaimed = (%d rows, %v) — the walk would be stranded forever", len(resumed), err)
	}
	if !resumed[0].CursorVideoID.Valid || uuid.UUID(resumed[0].CursorVideoID.Bytes) != cursor {
		t.Fatalf("resumed cursor = %v, want %s — progress must survive the reclaim", resumed[0].CursorVideoID, cursor)
	}
	if resumed[0].Purged != 7 {
		t.Errorf("resumed purged = %d, want 7", resumed[0].Purged)
	}
	if resumed[0].UrlSetComplete {
		t.Error("url_set_complete stayed true through a batch that reported itself incomplete")
	}
}

// TestCDNPurgeCatalogueWalkQueriesRun exercises the two reads the walk derives
// its work from. They have no fake anywhere — internal/cdnpurge's tests supply
// the rows directly — so this is the only place their SQL is ever parsed by a
// real planner, and a typo in either would otherwise surface as a walk that
// fails every batch on a production instance.
func TestCDNPurgeCatalogueWalkQueriesRun(t *testing.T) {
	st := purgeStore(t)
	q := st.Queries()
	ctx := context.Background()

	ids, err := q.ListPublicDownloadableVideoIDs(ctx, sqlcgen.ListPublicDownloadableVideoIDsParams{
		After: uuid.Nil, PageLimit: 100,
	})
	if err != nil {
		t.Fatalf("ListPublicDownloadableVideoIDs: %v", err)
	}
	// Whatever the shared integration database happens to hold, the walk must
	// be able to page it, and the second page must start after the first id.
	if len(ids) > 0 {
		if _, err := q.ListPublicDownloadableVideoIDs(ctx, sqlcgen.ListPublicDownloadableVideoIDsParams{
			After: ids[0], PageLimit: 100,
		}); err != nil {
			t.Fatalf("ListPublicDownloadableVideoIDs (second page): %v", err)
		}
	}

	// The per-video facts read answers for an id that does not exist too: every
	// EXISTS is false and the rendition array is empty rather than NULL, which
	// is what keeps a deleted-mid-walk video from failing the batch.
	facts, err := q.ListVideoDownloadFacts(ctx, uuid.New())
	if err != nil {
		t.Fatalf("ListVideoDownloadFacts for an unknown video: %v", err)
	}
	if facts.HasOriginal || facts.HasWebm || facts.HasPlaylist || len(facts.RenditionHeights) != 0 {
		t.Fatalf("facts for an unknown video = %+v, want all-false and no renditions", facts)
	}
}

// TestCDNPurgeConcurrentClaimsAreDisjoint is the multi-instance guarantee. Two
// nodes claiming at the same moment must split the queue, not duplicate it: a
// doubled fan-out is a doubled call volume at a rate-limited purge API.
func TestCDNPurgeConcurrentClaimsAreDisjoint(t *testing.T) {
	st := purgeStore(t)
	q := st.Queries()
	ctx := context.Background()

	const jobs = 6
	for i := 0; i < jobs; i++ {
		if _, err := q.EnqueueCDNPurgeURLs(ctx, sqlcgen.EnqueueCDNPurgeURLsParams{
			Reason:         "retry",
			Urls:           purgeURLs(t, "/api/v1/videos/"+uuid.NewString()+"/thumbnail"),
			UrlSetComplete: true,
			NextAttemptAt:  time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	a, b := claimRace(t, func(ctx context.Context) ([]uuid.UUID, error) {
		rows, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 600, ClaimLimit: 3})
		if err != nil {
			return nil, err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids, nil
	})
	seen := map[uuid.UUID]bool{}
	for _, id := range append(append([]uuid.UUID{}, a...), b...) {
		if seen[id] {
			t.Fatalf("job %s was claimed by BOTH workers", id)
		}
		seen[id] = true
	}
	if len(seen) != jobs {
		t.Fatalf("two claimers took %d of %d due jobs", len(seen), jobs)
	}
}

// TestCDNPurgeRetryRewritesTheRemainingURLs proves the round trip through the
// jsonb column: the retry carries only what is still unpurged, and the dead
// letter keeps that list because it IS the operator's manual-invalidation
// to-do list.
func TestCDNPurgeRetryRewritesTheRemainingURLs(t *testing.T) {
	st := purgeStore(t)
	q := st.Queries()
	ctx := context.Background()

	id, err := q.EnqueueCDNPurgeURLs(ctx, sqlcgen.EnqueueCDNPurgeURLsParams{
		Reason:         "account_delete",
		Urls:           purgeURLs(t, "/api/v1/videos/a/thumbnail", "/api/v1/videos/a/original"),
		UrlSetComplete: true,
		NextAttemptAt:  time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.RescheduleCDNPurgeJob(ctx, sqlcgen.RescheduleCDNPurgeJobParams{
		ID:            id,
		Urls:          purgeURLs(t, "/api/v1/videos/a/original"),
		Purged:        1,
		NextAttemptAt: time.Now().Add(-time.Second),
		LastError:     "1 urls were not invalidated: cdn: purge rejected with status 500",
	}); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	rows, err := q.ClaimDueCDNPurgeJobs(ctx, sqlcgen.ClaimDueCDNPurgeJobsParams{LeaseSeconds: 60, ClaimLimit: 5})
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim = (%d rows, %v)", len(rows), err)
	}
	var got []string
	if err := json.Unmarshal(rows[0].Urls, &got); err != nil {
		t.Fatalf("decode urls: %v", err)
	}
	if len(got) != 1 || got[0] != "/api/v1/videos/a/original" {
		t.Fatalf("retry carries %v, want only the unpurged URL", got)
	}
	if rows[0].Attempts != 1 || rows[0].Purged != 1 {
		t.Errorf("attempts=%d purged=%d, want 1 and 1", rows[0].Attempts, rows[0].Purged)
	}

	if err := q.FailCDNPurgeJob(ctx, sqlcgen.FailCDNPurgeJobParams{
		ID: id, Urls: rows[0].Urls, Purged: 1, LastError: "gave up",
	}); err != nil {
		t.Fatalf("fail: %v", err)
	}
	stats, err := q.CDNPurgeJobStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Failed != 1 || stats.Pending != 0 || stats.Running != 0 {
		t.Fatalf("stats = %+v, want exactly one dead letter", stats)
	}
	fails, err := q.CDNPurgeRecentFailures(ctx, 10)
	if err != nil || len(fails) != 1 {
		t.Fatalf("recent failures = (%d rows, %v)", len(fails), err)
	}
	if fails[0].ID != id || fails[0].Attempts != 2 {
		t.Errorf("failure row = %+v, want the job at 2 attempts", fails[0])
	}
}

// TestCDNPurgeSweepKeepsDeadLettersLongerThanSuccesses pins the retention
// asymmetry: a success is a fact about the past, a dead letter is a to-do list.
func TestCDNPurgeSweepKeepsDeadLettersLongerThanSuccesses(t *testing.T) {
	st := purgeStore(t)
	q := st.Queries()
	ctx := context.Background()

	done, err := q.EnqueueCDNPurgeURLs(ctx, sqlcgen.EnqueueCDNPurgeURLsParams{
		Reason: "retry", Urls: purgeURLs(t, "/api/v1/videos/a/thumbnail"),
		UrlSetComplete: true, NextAttemptAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dead, err := q.EnqueueCDNPurgeURLs(ctx, sqlcgen.EnqueueCDNPurgeURLsParams{
		Reason: "retry", Urls: purgeURLs(t, "/api/v1/videos/b/thumbnail"),
		UrlSetComplete: true, NextAttemptAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CompleteCDNPurgeJob(ctx, sqlcgen.CompleteCDNPurgeJobParams{ID: done, Purged: 1}); err != nil {
		t.Fatal(err)
	}
	if err := q.FailCDNPurgeJob(ctx, sqlcgen.FailCDNPurgeJobParams{ID: dead, Urls: purgeURLs(t, "/api/v1/videos/b/thumbnail"), LastError: "gave up"}); err != nil {
		t.Fatal(err)
	}

	// A TTL of zero for successes and a long one for dead letters: exactly the
	// asymmetry the shipped 7d/14d pair encodes, compressed for the test.
	n, err := q.DeleteFinishedCDNPurgeJobs(ctx, sqlcgen.DeleteFinishedCDNPurgeJobsParams{
		DoneTtlSeconds: 0, FailedTtlSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("sweep removed %d rows, want 1 (the success only)", n)
	}
	stats, err := q.CDNPurgeJobStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Failed != 1 || stats.Done != 0 {
		t.Fatalf("stats after the sweep = %+v, want the dead letter kept and the success gone", stats)
	}
}
