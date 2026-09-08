package cdnpurge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory cdn_purge_jobs, faithful to the parts of 0137 the
// service depends on: the claim admits pending AND running past their lease,
// and the walk's partial unique index refuses a second active walk.
type fakeRepo struct {
	rows map[uuid.UUID]*jobRow
	// order preserves insertion so a claim is deterministic in tests.
	order []uuid.UUID

	videoIDs []uuid.UUID
	facts    map[uuid.UUID]sqlcgen.ListVideoDownloadFactsRow

	now func() time.Time

	claimErr   error
	listErr    error
	factsErr   map[uuid.UUID]error
	enqueueErr error
}

type jobRow struct {
	id             uuid.UUID
	kind           string
	reason         string
	urls           []string
	cursor         *uuid.UUID
	urlSetComplete bool
	purged         int32
	attempts       int32
	state          string
	nextAttemptAt  time.Time
	lastError      string
}

func newFakeRepo(now func() time.Time) *fakeRepo {
	return &fakeRepo{
		rows:  map[uuid.UUID]*jobRow{},
		facts: map[uuid.UUID]sqlcgen.ListVideoDownloadFactsRow{},
		now:   now,
	}
}

func (f *fakeRepo) EnqueueCDNPurgeURLs(_ context.Context, arg sqlcgen.EnqueueCDNPurgeURLsParams) (uuid.UUID, error) {
	if f.enqueueErr != nil {
		return uuid.Nil, f.enqueueErr
	}
	var paths []string
	if err := json.Unmarshal(arg.Urls, &paths); err != nil {
		return uuid.Nil, err
	}
	id := uuid.New()
	f.rows[id] = &jobRow{
		id: id, kind: kindURLs, reason: arg.Reason, urls: paths,
		urlSetComplete: arg.UrlSetComplete, attempts: arg.Attempts,
		state: "pending", nextAttemptAt: arg.NextAttemptAt,
	}
	f.order = append(f.order, id)
	return id, nil
}

func (f *fakeRepo) EnqueueCDNPurgeDownloadsRevoked(_ context.Context) (uuid.UUID, error) {
	if f.enqueueErr != nil {
		return uuid.Nil, f.enqueueErr
	}
	for _, r := range f.rows {
		if r.kind == kindDownloadsRevoked && (r.state == "pending" || r.state == "running") {
			// The partial unique index: ON CONFLICT DO NOTHING returns no row.
			return uuid.Nil, pgx.ErrNoRows
		}
	}
	id := uuid.New()
	f.rows[id] = &jobRow{
		id: id, kind: kindDownloadsRevoked, reason: ReasonDownloadsRevoked,
		urlSetComplete: true, state: "pending", nextAttemptAt: f.now(),
	}
	f.order = append(f.order, id)
	return id, nil
}

func (f *fakeRepo) ClaimDueCDNPurgeJobs(_ context.Context, arg sqlcgen.ClaimDueCDNPurgeJobsParams) ([]sqlcgen.ClaimDueCDNPurgeJobsRow, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	var out []sqlcgen.ClaimDueCDNPurgeJobsRow
	for _, id := range f.order {
		r, ok := f.rows[id]
		if !ok || len(out) >= int(arg.ClaimLimit) {
			continue
		}
		if r.state != "pending" && r.state != "running" {
			continue
		}
		if r.nextAttemptAt.After(f.now()) {
			continue
		}
		r.state = "running"
		r.nextAttemptAt = f.now().Add(time.Duration(arg.LeaseSeconds) * time.Second)
		blob, _ := json.Marshal(r.urls)
		row := sqlcgen.ClaimDueCDNPurgeJobsRow{
			ID: r.id, Kind: r.kind, Reason: r.reason, Urls: blob,
			UrlSetComplete: r.urlSetComplete, Purged: r.purged, Attempts: r.attempts,
		}
		if r.cursor != nil {
			row.CursorVideoID = pgtype.UUID{Bytes: *r.cursor, Valid: true}
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeRepo) CompleteCDNPurgeJob(_ context.Context, arg sqlcgen.CompleteCDNPurgeJobParams) error {
	r := f.rows[arg.ID]
	r.state, r.urls, r.purged, r.lastError = "done", nil, arg.Purged, ""
	return nil
}

func (f *fakeRepo) AdvanceCDNPurgeWalk(_ context.Context, arg sqlcgen.AdvanceCDNPurgeWalkParams) error {
	r := f.rows[arg.ID]
	cur := uuid.UUID(arg.CursorVideoID.Bytes)
	r.cursor, r.purged, r.state, r.nextAttemptAt = &cur, arg.Purged, "pending", f.now()
	r.urlSetComplete = r.urlSetComplete && arg.BatchComplete
	return nil
}

func (f *fakeRepo) RescheduleCDNPurgeJob(_ context.Context, arg sqlcgen.RescheduleCDNPurgeJobParams) error {
	r := f.rows[arg.ID]
	var paths []string
	_ = json.Unmarshal(arg.Urls, &paths)
	r.urls, r.purged, r.state = paths, arg.Purged, "pending"
	r.attempts++
	r.nextAttemptAt, r.lastError = arg.NextAttemptAt, arg.LastError
	return nil
}

func (f *fakeRepo) FailCDNPurgeJob(_ context.Context, arg sqlcgen.FailCDNPurgeJobParams) error {
	r := f.rows[arg.ID]
	var paths []string
	_ = json.Unmarshal(arg.Urls, &paths)
	r.urls, r.purged, r.state = paths, arg.Purged, "failed"
	r.attempts++
	r.lastError = arg.LastError
	return nil
}

func (f *fakeRepo) DeleteFinishedCDNPurgeJobs(_ context.Context, _ sqlcgen.DeleteFinishedCDNPurgeJobsParams) (int64, error) {
	return 0, nil
}

func (f *fakeRepo) ListPublicDownloadableVideoIDs(_ context.Context, arg sqlcgen.ListPublicDownloadableVideoIDsParams) ([]uuid.UUID, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []uuid.UUID
	started := arg.After == uuid.Nil
	for _, id := range f.videoIDs {
		if !started {
			if id == arg.After {
				started = true
			}
			continue
		}
		out = append(out, id)
		if len(out) >= int(arg.PageLimit) {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) ListVideoDownloadFacts(_ context.Context, videoID uuid.UUID) (sqlcgen.ListVideoDownloadFactsRow, error) {
	if err := f.factsErr[videoID]; err != nil {
		return sqlcgen.ListVideoDownloadFactsRow{}, err
	}
	return f.facts[videoID], nil
}

func (f *fakeRepo) CDNPurgeJobStats(_ context.Context) (sqlcgen.CDNPurgeJobStatsRow, error) {
	var row sqlcgen.CDNPurgeJobStatsRow
	oldest := time.Time{}
	for _, r := range f.rows {
		switch r.state {
		case "pending":
			row.Pending++
		case "running":
			row.Running++
		case "done":
			row.Done++
		case "failed":
			row.Failed++
		}
		if r.state == "pending" || r.state == "running" {
			if oldest.IsZero() {
				oldest = f.now().Add(-90 * time.Second)
			}
		}
	}
	if !oldest.IsZero() {
		row.OldestPendingAgeSeconds = int64(f.now().Sub(oldest) / time.Second)
	}
	return row, nil
}

// recordingEdge is the fake CDN edge: it records every purge it is asked for and
// can be told to reject a set of paths, which is how the retry ladder is
// observed without waiting on wall-clock time.
type recordingEdge struct {
	calls  []string
	reject map[string]bool
	err    error
}

func (e *recordingEdge) purge(_ context.Context, mediaPath string) error {
	e.calls = append(e.calls, mediaPath)
	if e.reject[mediaPath] {
		if e.err != nil {
			return e.err
		}
		return errors.New("cdn: purge rejected with status 500")
	}
	return nil
}

func (e *recordingEdge) count(path string) int {
	n := 0
	for _, c := range e.calls {
		if c == path {
			n++
		}
	}
	return n
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestService(t *testing.T, repo *fakeRepo, edge *recordingEdge, now func() time.Time, opts ...Option) *Service {
	t.Helper()
	base := []Option{WithLogger(quietLogger()), WithClock(now)}
	svc := NewService(repo, edge.purge, append(base, opts...)...)
	if svc == nil {
		t.Fatal("NewService returned nil with a repo and a purger")
	}
	return svc
}

func TestNewServiceIsNilWithoutACDN(t *testing.T) {
	// The free-by-default gate: with no purger there is provably no shared copy
	// to invalidate, so there must be no queue to drain either.
	if svc := NewService(newFakeRepo(time.Now), nil); svc != nil {
		t.Fatal("NewService with a nil purger = non-nil, want nil (no CDN configured)")
	}
	// And every method is nil-safe, so callers never branch.
	var svc *Service
	svc.EnqueueSnapshot(context.Background(), ReasonAccountDelete, []string{"/api/v1/videos/x/thumbnail"}, true)
	svc.EnqueueDownloadsRevoked(context.Background())
	if n, err := svc.DrainOnce(context.Background(), 5); n != 0 || err != nil {
		t.Fatalf("nil DrainOnce = (%d, %v), want (0, nil)", n, err)
	}
	if st, err := svc.Stats(context.Background()); err != nil || st != (Stats{}) {
		t.Fatalf("nil Stats = (%+v, %v), want (zero, nil)", st, err)
	}
}

func TestSnapshotJobPurgesEveryURLOnce(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	edge := &recordingEdge{}
	svc := newTestService(t, repo, edge, now)

	paths := []string{"/api/v1/videos/a/thumbnail", "/api/v1/videos/a/original", "/api/v1/videos/a/thumbnail"}
	svc.EnqueueSnapshot(context.Background(), ReasonAccountDelete, paths, true)

	n, err := svc.DrainOnce(context.Background(), 10)
	if err != nil || n != 1 {
		t.Fatalf("DrainOnce = (%d, %v), want (1, nil)", n, err)
	}
	if len(edge.calls) != 2 {
		t.Fatalf("edge saw %d purges (%v), want 2 — the repeated path must be deduped", len(edge.calls), edge.calls)
	}
	for _, r := range repo.rows {
		if r.state != "done" {
			t.Fatalf("job state = %q, want done", r.state)
		}
		if r.purged != 2 {
			t.Fatalf("purged = %d, want 2", r.purged)
		}
	}
}

// TestFailedPurgeRetriesWithBackoffThenDeadLetters is the SC4 assertion: a
// rejected purge is retried on a doubling schedule, only the rejected URL is
// re-issued, and the run dead-letters after the cap with its URLs intact.
func TestFailedPurgeRetriesWithBackoffThenDeadLetters(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	const bad = "/api/v1/videos/a/thumbnail"
	const good = "/api/v1/videos/a/original"
	edge := &recordingEdge{reject: map[string]bool{bad: true}}
	// base 1s, cap 4s, 4 attempts: the immediate pass plus three persisted ones.
	svc := newTestService(t, repo, edge, now, WithRetry(time.Second, 4*time.Second, 4))

	// The immediate pass already happened (attemptsSpent=1) and left one URL.
	svc.EnqueueRetry(context.Background(), []string{bad, good}, true, 1)

	var row *jobRow
	for _, r := range repo.rows {
		row = r
	}
	if got := row.nextAttemptAt.Sub(clock); got != time.Second {
		t.Fatalf("first retry scheduled in %v, want 1s (base × 2^0)", got)
	}

	// Nothing is due yet — the backoff is real, not decorative.
	if n, _ := svc.DrainOnce(context.Background(), 10); n != 0 {
		t.Fatalf("DrainOnce before the delay handled %d jobs, want 0", n)
	}

	wantDelays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	for i, want := range wantDelays {
		clock = row.nextAttemptAt
		if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
			t.Fatalf("attempt %d: DrainOnce handled 0 jobs", i+2)
		}
		if i < len(wantDelays)-1 {
			if row.state != "pending" {
				t.Fatalf("attempt %d: state = %q, want pending", i+2, row.state)
			}
			if got := row.nextAttemptAt.Sub(clock); got != wantDelays[i+1] {
				t.Fatalf("attempt %d: next retry in %v, want %v", i+2, got, wantDelays[i+1])
			}
		}
		_ = want
	}

	if row.state != "failed" {
		t.Fatalf("after the attempt cap state = %q, want failed (dead letter)", row.state)
	}
	if len(row.urls) != 1 || row.urls[0] != bad {
		t.Fatalf("dead letter urls = %v, want exactly the unpurged one — it is the operator's to-do list", row.urls)
	}
	if row.lastError == "" {
		t.Fatal("dead letter carries no cause")
	}
	// The URL that DID land is never re-issued: one call, on the first attempt.
	if got := edge.count(good); got != 1 {
		t.Fatalf("the already-purged URL was re-issued %d times, want 1", got)
	}
	if got := edge.count(bad); got != 3 {
		t.Fatalf("the rejected URL was tried %d times, want 3 persisted retries", got)
	}
}

// TestSuccessfulRetryClearsTheRecord is the other half of SC4: once the edge
// accepts, the job completes and nothing is left pending.
func TestSuccessfulRetryClearsTheRecord(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	const bad = "/api/v1/videos/a/thumbnail"
	edge := &recordingEdge{reject: map[string]bool{bad: true}}
	svc := newTestService(t, repo, edge, now, WithRetry(time.Second, time.Second, 5))

	svc.EnqueueRetry(context.Background(), []string{bad}, true, 1)
	var row *jobRow
	for _, r := range repo.rows {
		row = r
	}
	clock = row.nextAttemptAt
	if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the retry did not run")
	}
	if row.state != "pending" {
		t.Fatalf("state = %q, want pending after a rejection", row.state)
	}

	// The edge recovers.
	edge.reject = nil
	clock = row.nextAttemptAt
	if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the recovered retry did not run")
	}
	if row.state != "done" {
		t.Fatalf("state = %q, want done once the edge accepted", row.state)
	}
	if len(row.urls) != 0 {
		t.Fatalf("completed job still carries %v, want nothing pending", row.urls)
	}
}

// TestDownloadsWalkIsResumable is SC3: the walk covers the catalogue a page at a
// time, persists a cursor, and picks up where it left off after a restart.
func TestDownloadsWalkIsResumable(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	edge := &recordingEdge{}

	// Three videos, id-ordered as the query is.
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		uuid.MustParse("00000000-0000-0000-0000-000000000003"),
	}
	repo.videoIDs = ids
	for _, id := range ids {
		repo.facts[id] = sqlcgen.ListVideoDownloadFactsRow{
			HasOriginal: true, HasPlaylist: true, RenditionHeights: []int32{720},
		}
	}
	svc := newTestService(t, repo, edge, now, WithWalkPage(2))
	svc.EnqueueDownloadsRevoked(context.Background())

	// A second flip while one walk is live adds nothing.
	svc.EnqueueDownloadsRevoked(context.Background())
	if len(repo.rows) != 1 {
		t.Fatalf("%d walk rows, want 1 — a second flip must not start a second walk", len(repo.rows))
	}
	var row *jobRow
	for _, r := range repo.rows {
		row = r
	}

	// First page: two videos × (original + audio + 720 with and without audio).
	if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the walk did not run")
	}
	if len(edge.calls) != 8 {
		t.Fatalf("first page issued %d purges (%v), want 8", len(edge.calls), edge.calls)
	}
	if row.cursor == nil || *row.cursor != ids[1] {
		t.Fatalf("cursor = %v, want %v — progress must be persisted", row.cursor, ids[1])
	}
	if row.state != "pending" {
		t.Fatalf("state after a page = %q, want pending (immediately claimable)", row.state)
	}

	// A RESTART: a brand-new service over the same rows resumes from the cursor
	// rather than replaying the catalogue.
	edge2 := &recordingEdge{}
	svc2 := newTestService(t, repo, edge2, now, WithWalkPage(2))
	if n, _ := svc2.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the walk did not resume after a restart")
	}
	if len(edge2.calls) != 4 {
		t.Fatalf("resumed page issued %d purges (%v), want 4 — only the third video was left", len(edge2.calls), edge2.calls)
	}
	for _, c := range edge2.calls {
		if c[:len("/api/v1/videos/"+ids[2].String())] != "/api/v1/videos/"+ids[2].String() {
			t.Fatalf("resumed page re-purged %q; the cursor was not honoured", c)
		}
	}

	// The final page finds nothing and closes the job.
	if n, _ := svc2.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the walk did not run its terminating page")
	}
	if row.state != "done" {
		t.Fatalf("state = %q, want done once the catalogue is exhausted", row.state)
	}
	if row.purged != 12 {
		t.Fatalf("purged = %d, want 12 across the whole walk", row.purged)
	}
}

// TestWalkRejectionsBecomeTheirOwnRetryAndDoNotStallTheWalk pins the ruling that
// a walk is not a transaction: one video the edge keeps refusing must not hold
// the rest of the catalogue.
func TestWalkRejectionsBecomeTheirOwnRetryAndDoNotStallTheWalk(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	repo.videoIDs = []uuid.UUID{first, second}
	repo.facts[first] = sqlcgen.ListVideoDownloadFactsRow{HasOriginal: true}
	repo.facts[second] = sqlcgen.ListVideoDownloadFactsRow{HasOriginal: true}
	badPath := "/api/v1/videos/" + first.String() + "/download/original"
	edge := &recordingEdge{reject: map[string]bool{badPath: true}}
	svc := newTestService(t, repo, edge, now, WithWalkPage(10), WithRetry(time.Second, time.Second, 5))

	svc.EnqueueDownloadsRevoked(context.Background())
	if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the walk did not run")
	}

	var walk, retryJob *jobRow
	for _, r := range repo.rows {
		if r.kind == kindDownloadsRevoked {
			walk = r
		} else {
			retryJob = r
		}
	}
	if walk.cursor == nil || *walk.cursor != second {
		t.Fatalf("cursor = %v, want %v — the walk must advance past a rejection", walk.cursor, second)
	}
	if walk.urlSetComplete {
		t.Fatal("url_set_complete stayed true after a rejected batch; the walk must report incompleteness")
	}
	if retryJob == nil {
		t.Fatal("a rejected batch produced no retry job")
	}
	if retryJob.reason != ReasonRetry || len(retryJob.urls) != 1 || retryJob.urls[0] != badPath {
		t.Fatalf("retry job = %+v, want exactly the rejected URL under reason %q", retryJob, ReasonRetry)
	}
}

// TestUnreadableCatalogueKeepsTheCursor: a database read failure is not a purge
// failure, and must not advance past videos that were never visited.
func TestUnreadableCatalogueKeepsTheCursor(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	repo.listErr = errors.New("connection refused")
	edge := &recordingEdge{}
	svc := newTestService(t, repo, edge, now, WithRetry(time.Second, time.Second, 5))
	svc.EnqueueDownloadsRevoked(context.Background())

	if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
		t.Fatal("the walk did not run")
	}
	var walk *jobRow
	for _, r := range repo.rows {
		walk = r
	}
	if walk.cursor != nil {
		t.Fatalf("cursor = %v after an unreadable catalogue, want nil", walk.cursor)
	}
	if walk.state != "pending" {
		t.Fatalf("state = %q, want pending (rescheduled)", walk.state)
	}
	if len(edge.calls) != 0 {
		t.Fatalf("edge was called %d times on an unreadable catalogue", len(edge.calls))
	}
}

// TestDownloadPathsAreTheRoutesTheEdgeHolds pins the URL set one video
// contributes to the walk. It is the same grammar the request handlers use.
func TestDownloadPathsAreTheRoutesTheEdgeHolds(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-0000000000ab")
	base := "/api/v1/videos/" + id.String()

	got := downloadPaths(id, sqlcgen.ListVideoDownloadFactsRow{
		HasOriginal: true, HasWebm: true, HasPlaylist: true, RenditionHeights: []int32{360, 720},
	})
	want := []string{
		base + "/download/original",
		base + "/download/webm",
		base + "/download/audio",
		base + "/download/hls/360",
		base + "/download/hls/360?audio=false",
		base + "/download/hls/720",
		base + "/download/hls/720?audio=false",
	}
	if len(got) != len(want) {
		t.Fatalf("downloadPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("downloadPaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// NO PLAYBACK ROUTE IS HERE. /original and /webm are fenced on visibility
	// alone; purging them to enforce a download gate would cold-start every
	// viewer's progressive fallback.
	for _, p := range got {
		if p == base+"/original" || p == base+"/webm" || p == base+"/thumbnail" {
			t.Fatalf("downloadPaths named the playback route %q", p)
		}
	}

	// With no finalized ladder the derived download routes 404, so the edge can
	// hold nothing for them.
	got = downloadPaths(id, sqlcgen.ListVideoDownloadFactsRow{HasOriginal: true, RenditionHeights: []int32{720}})
	if len(got) != 1 || got[0] != base+"/download/original" {
		t.Fatalf("downloadPaths without a playlist = %v, want only the original", got)
	}
}

func TestEnqueueIsFreeWhenThereIsNothingToPurge(t *testing.T) {
	clock := time.Now()
	repo := newFakeRepo(func() time.Time { return clock })
	edge := &recordingEdge{}
	svc := newTestService(t, repo, edge, func() time.Time { return clock })

	svc.EnqueueSnapshot(context.Background(), ReasonAccountDelete, nil, true)
	svc.EnqueueSnapshot(context.Background(), ReasonAccountDelete, []string{"", ""}, true)
	if len(repo.rows) != 0 {
		t.Fatalf("%d rows queued for an empty snapshot, want 0", len(repo.rows))
	}
}

func TestClaimErrorSurfaces(t *testing.T) {
	clock := time.Now()
	repo := newFakeRepo(func() time.Time { return clock })
	repo.claimErr = errors.New("boom")
	edge := &recordingEdge{}
	svc := newTestService(t, repo, edge, func() time.Time { return clock })
	if _, err := svc.DrainOnce(context.Background(), 5); err == nil {
		t.Fatal("DrainOnce swallowed a claim failure")
	}
}

func TestStatsReportsPendingAndDeadLetters(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	const bad = "/api/v1/videos/a/thumbnail"
	edge := &recordingEdge{reject: map[string]bool{bad: true}}
	svc := newTestService(t, repo, edge, now, WithRetry(time.Second, time.Second, 2))

	svc.EnqueueRetry(context.Background(), []string{bad}, true, 1)
	st, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending != 1 || st.DeadLettered != 0 {
		t.Fatalf("stats = %+v, want one pending and no dead letters", st)
	}
	if st.OldestPendingAge <= 0 {
		t.Fatal("oldest pending age = 0 with work outstanding")
	}

	var row *jobRow
	for _, r := range repo.rows {
		row = r
	}
	clock = row.nextAttemptAt
	if _, err := svc.DrainOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Stats(context.Background())
	if st.DeadLettered != 1 {
		t.Fatalf("stats = %+v, want one dead letter after the cap", st)
	}
}
