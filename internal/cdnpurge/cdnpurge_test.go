package cdnpurge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
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

// TestPurgeReplacedAssetNamesTheServedURL is SC1's URL assertion: the poster and
// the storyboard sprite are invalidated at the exact route a viewer requests
// them from, which is what an edge holds its entry under.
func TestPurgeReplacedAssetNamesTheServedURL(t *testing.T) {
	clock := time.Now()
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	edge := &recordingEdge{}
	svc := newTestService(t, repo, edge, now)
	id := uuid.MustParse("00000000-0000-0000-0000-0000000000cd")

	if got := ReplacedAssetPath(id, "thumbnail"); got != "/api/v1/videos/"+id.String()+"/thumbnail" {
		t.Fatalf("thumbnail path = %q", got)
	}
	if got := ReplacedAssetPath(id, "storyboard"); got != "/api/v1/videos/"+id.String()+"/storyboard.jpg" {
		t.Fatalf("storyboard path = %q", got)
	}

	// The synchronous body, so the assertion does not race the goroutine.
	if n := svc.PurgeNow(context.Background(), []string{ReplacedAssetPath(id, "storyboard")}, true); n != 1 {
		t.Fatalf("PurgeNow purged %d, want 1", n)
	}
	if len(edge.calls) != 1 || edge.calls[0] != "/api/v1/videos/"+id.String()+"/storyboard.jpg" {
		t.Fatalf("edge saw %v", edge.calls)
	}
}

// TestReplacedAssetRefusalIsQueuedForRetry: the poster replacement is exactly
// the case where an edge that says no keeps showing the face the creator just
// changed, so it must not be a single shot either.
func TestReplacedAssetRefusalIsQueuedForRetry(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	repo := newFakeRepo(now)
	id := uuid.MustParse("00000000-0000-0000-0000-0000000000ce")
	path := ReplacedAssetPath(id, "thumbnail")
	edge := &recordingEdge{reject: map[string]bool{path: true}}

	var recorded [3]int
	svc := newTestService(t, repo, edge, now, WithRecorder(func(purged, failed int, complete bool) {
		recorded[0], recorded[1] = purged, failed
		if complete {
			recorded[2] = 1
		}
	}))

	svc.PurgeNow(context.Background(), []string{path}, true)
	if recorded[0] != 0 || recorded[1] != 1 || recorded[2] != 1 {
		t.Fatalf("counters = %v, want one failed complete-set run", recorded)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("%d queued jobs, want 1 retry", len(repo.rows))
	}
	for _, r := range repo.rows {
		if r.reason != ReasonRetry || len(r.urls) != 1 || r.urls[0] != path {
			t.Fatalf("queued job = %+v", r)
		}
		if r.attempts != 1 {
			t.Fatalf("attempts = %d, want 1 — the immediate pass counts", r.attempts)
		}
		if !r.nextAttemptAt.After(clock) {
			t.Fatal("the retry is due immediately; the backoff was not applied")
		}
	}
}

// --- the durable record of a job's outcome (A33 rehearsal finding 6) ---------

// fakeAuditRepo is an in-memory audit_log. It exists so the assertions below go
// through the REAL audit.Service: an unallowed metadata key or a malformed
// action name is refused by the envelope validator, and that refusal has to be
// a test failure here rather than a row that silently never appears.
type fakeAuditRepo struct {
	rows []sqlcgen.InsertAuditLogParams
}

func (f *fakeAuditRepo) InsertAuditLog(_ context.Context, arg sqlcgen.InsertAuditLogParams) error {
	f.rows = append(f.rows, arg)
	return nil
}

func (f *fakeAuditRepo) ListAuditLog(context.Context, sqlcgen.ListAuditLogParams) ([]sqlcgen.ListAuditLogRow, error) {
	return nil, nil
}
func (f *fakeAuditRepo) CountAuditLog(context.Context, *string) (int64, error) { return 0, nil }
func (f *fakeAuditRepo) PruneAuditLog(context.Context, sqlcgen.PruneAuditLogParams) (int64, error) {
	return 0, nil
}

// metadata decodes one audit row's allow-listed fields.
func (f *fakeAuditRepo) metadata(t *testing.T, i int) map[string]string {
	t.Helper()
	out := map[string]string{}
	if len(f.rows[i].Metadata) == 0 {
		return out
	}
	if err := json.Unmarshal(f.rows[i].Metadata, &out); err != nil {
		t.Fatalf("row %d metadata is not a JSON object: %v", i, err)
	}
	return out
}

func (f *fakeAuditRepo) assertRow(t *testing.T, i int, action, result string, want map[string]string) {
	t.Helper()
	if len(f.rows) <= i {
		t.Fatalf("want an audit row at %d, got %d rows", i, len(f.rows))
	}
	row := f.rows[i]
	if row.Action != action {
		t.Errorf("row %d action = %q, want %q", i, row.Action, action)
	}
	if row.Result != result {
		t.Errorf("row %d result = %q, want %q", i, row.Result, result)
	}
	// A ticker is not a person: nobody asked for this attempt.
	if row.ActorKind != "system" || row.ActorID.Valid {
		t.Errorf("row %d actor = (%q, valid=%v), want a system actor with no user id", i, row.ActorKind, row.ActorID.Valid)
	}
	if row.Reason != "" {
		t.Errorf("row %d carries prose in reason (%q); audit_log carries counts, never text", i, row.Reason)
	}
	got := f.metadata(t, i)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("row %d metadata[%q] = %q, want %q (all: %v)", i, k, got[k], v, got)
		}
	}
	// A media path can carry the operator's own purge-API credential in the
	// template it was built from. Nothing URL-shaped may reach this table.
	for k, v := range got {
		if strings.Contains(v, "/") {
			t.Errorf("row %d metadata[%q] = %q looks like a path; only counts belong here", i, k, v)
		}
	}
}

// TestPurgeJobOutcomeIsAudited is the defect the A33 rehearsal recorded as
// finding 6: "there is no audit_log action for a CDN purge at all", so the only
// durable trace of a takedown reaching the edge was a queue row a retention
// sweep eventually deletes plus one log line.
//
// Both outcomes are covered, and so is the silence in between: the retry ladder
// writes nothing, because a flapping edge must not become the bulk of a quiet
// instance's security trail.
func TestPurgeJobOutcomeIsAudited(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	t.Run("a completed job", func(t *testing.T) {
		repo := newFakeRepo(now)
		edge := &recordingEdge{}
		trail := &fakeAuditRepo{}
		svc := newTestService(t, repo, edge, now, WithAuditor(audit.NewService(trail)))

		svc.EnqueueSnapshot(context.Background(), ReasonAccountDelete,
			[]string{"/api/v1/videos/a/thumbnail", "/api/v1/videos/a/original"}, true)
		if n, err := svc.DrainOnce(context.Background(), 10); n != 1 || err != nil {
			t.Fatalf("DrainOnce = (%d, %v), want (1, nil)", n, err)
		}

		if len(trail.rows) != 1 {
			t.Fatalf("audit rows = %d, want exactly 1 per job outcome", len(trail.rows))
		}
		trail.assertRow(t, 0, observability.ActionCDNPurgeCompleted, "success", map[string]string{
			"reason_code": ReasonAccountDelete,
			"url_count":   "2",
			"purged":      "2",
			"failed":      "0",
			"attempts":    "1",
		})
		// The job id is on the row, so an operator reading the trail can find
		// the queue row (and its URL list) while it is still there.
		var jobID uuid.UUID
		for id := range repo.rows {
			jobID = id
		}
		if !trail.rows[0].JobID.Valid || uuid.UUID(trail.rows[0].JobID.Bytes) != jobID {
			t.Errorf("audit row job_id = %v, want the queue row's id %s", trail.rows[0].JobID, jobID)
		}
	})

	t.Run("a dead letter, and silence on the way there", func(t *testing.T) {
		repo := newFakeRepo(now)
		const bad = "/api/v1/videos/a/thumbnail"
		const good = "/api/v1/videos/a/original"
		edge := &recordingEdge{reject: map[string]bool{bad: true}}
		trail := &fakeAuditRepo{}
		// base 1s, cap 4s, 3 attempts: the immediate pass plus two persisted.
		svc := newTestService(t, repo, edge, now, WithRetry(time.Second, 4*time.Second, 3),
			WithAuditor(audit.NewService(trail)))

		svc.EnqueueRetry(context.Background(), []string{bad, good}, true, 1)
		var row *jobRow
		for _, r := range repo.rows {
			row = r
		}

		clock = row.nextAttemptAt
		if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
			t.Fatal("attempt 2 did not run")
		}
		if row.state != "pending" {
			t.Fatalf("state after attempt 2 = %q, want pending", row.state)
		}
		if len(trail.rows) != 0 {
			t.Fatalf("a scheduled retry wrote %d audit rows, want 0 — the queue row and the admin jobs page already carry attempts", len(trail.rows))
		}

		clock = row.nextAttemptAt
		if n, _ := svc.DrainOnce(context.Background(), 10); n != 1 {
			t.Fatal("attempt 3 did not run")
		}
		if row.state != "failed" {
			t.Fatalf("state = %q, want failed", row.state)
		}
		if len(trail.rows) != 1 {
			t.Fatalf("audit rows = %d, want exactly 1 for the dead letter", len(trail.rows))
		}
		trail.assertRow(t, 0, observability.ActionCDNPurgeDeadLettered, "failure", map[string]string{
			"reason_code": ReasonRetry,
			"url_count":   "1",
			"purged":      "1",
			"failed":      "1",
			"attempts":    "3",
		})
	})

	t.Run("no auditor wired is not an error", func(t *testing.T) {
		repo := newFakeRepo(now)
		svc := newTestService(t, repo, &recordingEdge{}, now)
		svc.EnqueueSnapshot(context.Background(), ReasonAccountDelete, []string{"/api/v1/videos/a/thumbnail"}, true)
		if n, err := svc.DrainOnce(context.Background(), 10); n != 1 || err != nil {
			t.Fatalf("DrainOnce with no auditor = (%d, %v), want (1, nil)", n, err)
		}
	})
}
