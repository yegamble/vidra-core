package qoe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory stand-in for the database. It exists so the rollup
// ARITHMETIC — the part that decides what an admin sees — is proven by `make ci`
// on a runner with no Postgres, rather than by an integration test that only
// runs when someone remembers to bring a database up.
type fakeRepo struct {
	events  []sqlcgen.ListQoEEventsForBucketPageRow
	rollups map[string]sqlcgen.UpsertQoERollupParams

	inserted    []sqlcgen.InsertQoEEventParams
	insertErr   error
	pruneEvents []sqlcgen.PruneQoEEventsParams
	pruneRolls  []sqlcgen.PruneQoERollupsParams
	// pruneRemaining models a table with more rows than one batch can take, so
	// the batch loop is exercised rather than assumed.
	pruneRemaining int64
	pageCalls      int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rollups: map[string]sqlcgen.UpsertQoERollupParams{}}
}

func (f *fakeRepo) InsertQoEEvent(_ context.Context, arg sqlcgen.InsertQoEEventParams) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, arg)
	return nil
}

func (f *fakeRepo) LatestQoERollupHour(context.Context) ([]time.Time, error) {
	var newest time.Time
	for _, r := range f.rollups {
		if r.HourBucket.After(newest) {
			newest = r.HourBucket
		}
	}
	if newest.IsZero() {
		return nil, nil
	}
	return []time.Time{newest}, nil
}

func (f *fakeRepo) ListQoERollupBuckets(_ context.Context, arg sqlcgen.ListQoERollupBucketsParams) ([]time.Time, error) {
	seen := map[time.Time]bool{}
	for _, e := range f.events {
		if e.ReceivedAt.Before(arg.AfterBucket) || !e.ReceivedAt.Before(arg.CompleteBefore) {
			continue
		}
		seen[HourBucket(e.ReceivedAt)] = true
	}
	out := make([]time.Time, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	if int32(len(out)) > arg.MaxBuckets {
		out = out[:arg.MaxBuckets]
	}
	return out, nil
}

func (f *fakeRepo) ListQoEEventsForBucketPage(_ context.Context, arg sqlcgen.ListQoEEventsForBucketPageParams) ([]sqlcgen.ListQoEEventsForBucketPageRow, error) {
	f.pageCalls++
	in := make([]sqlcgen.ListQoEEventsForBucketPageRow, 0, len(f.events))
	for _, e := range f.events {
		if e.ReceivedAt.Before(arg.BucketStart) || !e.ReceivedAt.Before(arg.BucketEnd) {
			continue
		}
		// The composite cursor, exactly as Postgres evaluates the row
		// comparison: strictly after (received_at, id).
		if e.ReceivedAt.Before(arg.AfterReceivedAt) {
			continue
		}
		if e.ReceivedAt.Equal(arg.AfterReceivedAt) && compareUUID(e.ID, arg.AfterID) <= 0 {
			continue
		}
		in = append(in, e)
	}
	sort.Slice(in, func(i, j int) bool {
		if !in[i].ReceivedAt.Equal(in[j].ReceivedAt) {
			return in[i].ReceivedAt.Before(in[j].ReceivedAt)
		}
		return compareUUID(in[i].ID, in[j].ID) < 0
	})
	if int32(len(in)) > arg.PageSize {
		in = in[:arg.PageSize]
	}
	return in, nil
}

func (f *fakeRepo) UpsertQoERollup(_ context.Context, arg sqlcgen.UpsertQoERollupParams) error {
	f.rollups[rollupKey(arg)] = arg
	return nil
}

func (f *fakeRepo) ListQoERollups(_ context.Context, arg sqlcgen.ListQoERollupsParams) ([]sqlcgen.QoeRollup, error) {
	out := []sqlcgen.QoeRollup{}
	for _, r := range f.rollups {
		if r.HourBucket.Before(arg.WindowStart) || !r.HourBucket.Before(arg.WindowEnd) {
			continue
		}
		out = append(out, sqlcgen.QoeRollup{
			HourBucket: r.HourBucket, DeliverySource: r.DeliverySource,
			Engine: r.Engine, PackagingFormat: r.PackagingFormat,
			EventCount: r.EventCount, StartCount: r.StartCount,
			RebufferCount: r.RebufferCount, BitrateSwitchCount: r.BitrateSwitchCount,
			ErrorCount: r.ErrorCount, VerifiedCount: r.VerifiedCount,
			TtffP50Ms: r.TtffP50Ms, TtffP95Ms: r.TtffP95Ms, TtffP99Ms: r.TtffP99Ms,
			RebufferP50Ms: r.RebufferP50Ms, RebufferP95Ms: r.RebufferP95Ms,
			RebufferP99Ms: r.RebufferP99Ms, RebufferTotalMs: r.RebufferTotalMs,
			HistogramVersion: r.HistogramVersion, TtffHistogram: r.TtffHistogram,
			RebufferHistogram: r.RebufferHistogram, ErrorCounts: r.ErrorCounts,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].HourBucket.Equal(out[j].HourBucket) {
			return out[i].HourBucket.Before(out[j].HourBucket)
		}
		return out[i].DeliverySource < out[j].DeliverySource
	})
	return out, nil
}

func (f *fakeRepo) CountQoERollups(_ context.Context, arg sqlcgen.CountQoERollupsParams) (int64, error) {
	rows, _ := f.ListQoERollups(context.Background(), sqlcgen.ListQoERollupsParams{
		WindowStart: arg.WindowStart, WindowEnd: arg.WindowEnd, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), nil
}

func (f *fakeRepo) PruneQoEEvents(_ context.Context, arg sqlcgen.PruneQoEEventsParams) (int64, error) {
	f.pruneEvents = append(f.pruneEvents, arg)
	take := int64(arg.BatchSize)
	if f.pruneRemaining < take {
		take = f.pruneRemaining
	}
	f.pruneRemaining -= take
	return take, nil
}

func (f *fakeRepo) PruneQoERollups(_ context.Context, arg sqlcgen.PruneQoERollupsParams) (int64, error) {
	f.pruneRolls = append(f.pruneRolls, arg)
	return 0, nil
}

func rollupKey(a sqlcgen.UpsertQoERollupParams) string {
	return a.HourBucket.Format(time.RFC3339) + "|" + a.DeliverySource + "|" + a.Engine + "|" + a.PackagingFormat
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

func quietService(repo Repository) *Service {
	return NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// orderedUUID builds ids that sort in a known order, so a test can prove the
// keyset cursor advances by (received_at, id) rather than by luck.
func orderedUUID(n byte) uuid.UUID {
	var u uuid.UUID
	u[0] = n
	u[15] = n
	return u
}

func rawEvent(at time.Time, id byte, typ EventType, source string, engine Engine, format PackagingFormat) sqlcgen.ListQoEEventsForBucketPageRow {
	return sqlcgen.ListQoEEventsForBucketPageRow{
		ID: orderedUUID(id), ReceivedAt: at, EventType: string(typ),
		DeliverySource: source, Engine: string(engine), PackagingFormat: string(format),
	}
}

var bucketStart = time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

// TestRollUpComputesPercentilesFromAKnownDistribution is the arithmetic proof.
// A thousand TTFF measurements of 1..1000 ms have p50 = 500, p95 = 950 and
// p99 = 990 by construction, so the rollup row is checked against a distribution
// whose answer needs no statistics.
func TestRollUpComputesPercentilesFromAKnownDistribution(t *testing.T) {
	repo := newFakeRepo()
	for i := 1; i <= 1000; i++ {
		e := rawEvent(bucketStart.Add(time.Duration(i)*time.Millisecond), byte(i%251+1), EventStart, "cdn", EngineHLSJS, FormatCMAF)
		v := int32(i)
		e.TtffMs = &v
		repo.events = append(repo.events, e)
	}
	svc := quietService(repo)

	hours, err := svc.RollUp(context.Background(), bucketStart.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	if hours != 1 {
		t.Fatalf("rolled %d hours, want 1", hours)
	}
	row, ok := repo.rollups[bucketStart.Format(time.RFC3339)+"|cdn|hls-js|cmaf"]
	if !ok {
		t.Fatalf("no rollup written; got keys %v", keysOf(repo.rollups))
	}
	if row.EventCount != 1000 || row.StartCount != 1000 {
		t.Errorf("event_count=%d start_count=%d, want 1000/1000", row.EventCount, row.StartCount)
	}
	assertNear(t, "ttff p50", row.TtffP50Ms, 500)
	assertNear(t, "ttff p95", row.TtffP95Ms, 950)
	assertNear(t, "ttff p99", row.TtffP99Ms, 990)

	// Rebuffer had no measurements at all, so its percentiles must be NULL and
	// not 0 — an admin reading 0 ms would read it as flawless delivery.
	if row.RebufferP50Ms != nil || row.RebufferP95Ms != nil || row.RebufferP99Ms != nil {
		t.Error("rebuffer percentiles are non-nil for a bucket with no rebuffers")
	}
	if row.HistogramVersion != HistogramVersion {
		t.Errorf("histogram_version = %d, want %d", row.HistogramVersion, HistogramVersion)
	}
}

// TestRollUpSeparatesGroups: the rollup key is (hour, source, engine, format),
// and mixing two sources' measurements into one row would make the exit
// criterion — percentiles PER SOURCE — unanswerable.
func TestRollUpSeparatesGroups(t *testing.T) {
	repo := newFakeRepo()
	add := func(id byte, source string, ttff int32) {
		e := rawEvent(bucketStart.Add(time.Duration(id)*time.Second), id, EventStart, source, EngineHLSJS, FormatCMAF)
		v := ttff
		e.TtffMs = &v
		repo.events = append(repo.events, e)
	}
	for i := byte(1); i <= 20; i++ {
		add(i, "cdn", 200)
	}
	for i := byte(21); i <= 40; i++ {
		add(i, "api-proxy", 2000)
	}
	if _, err := quietService(repo).RollUp(context.Background(), bucketStart.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	if len(repo.rollups) != 2 {
		t.Fatalf("wrote %d rollup rows, want 2 (one per source): %v", len(repo.rollups), keysOf(repo.rollups))
	}
	cdn := repo.rollups[bucketStart.Format(time.RFC3339)+"|cdn|hls-js|cmaf"]
	origin := repo.rollups[bucketStart.Format(time.RFC3339)+"|api-proxy|hls-js|cmaf"]
	assertNear(t, "cdn p50", cdn.TtffP50Ms, 200)
	assertNear(t, "api-proxy p50", origin.TtffP50Ms, 2000)
}

// TestRollUpCountsPerTypeAndSession proves the counters an admin reads — and in
// particular verified_count, which is the honest answer to "can I trust these
// session ids".
func TestRollUpCountsPerTypeAndSession(t *testing.T) {
	repo := newFakeRepo()
	at := bucketStart
	push := func(id byte, typ EventType, verified bool, mutate func(*sqlcgen.ListQoEEventsForBucketPageRow)) {
		e := rawEvent(at.Add(time.Duration(id)*time.Second), id, typ, "cdn", EngineHLSJS, FormatCMAF)
		e.SessionVerified = verified
		if mutate != nil {
			mutate(&e)
		}
		repo.events = append(repo.events, e)
	}
	push(1, EventStart, true, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { v := int32(300); e.TtffMs = &v })
	push(2, EventStart, false, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { v := int32(500); e.TtffMs = &v })
	push(3, EventRebuffer, false, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { v := int32(1200); e.RebufferMs = &v })
	push(4, EventRebuffer, false, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { v := int32(800); e.RebufferMs = &v })
	push(5, EventBitrateSwitch, false, nil)
	push(6, EventError, false, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { c := string(ErrorNetwork); e.ErrorClass = &c })
	push(7, EventError, false, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { c := string(ErrorNetwork); e.ErrorClass = &c })
	push(8, EventError, false, func(e *sqlcgen.ListQoEEventsForBucketPageRow) { c := string(ErrorMedia); e.ErrorClass = &c })

	if _, err := quietService(repo).RollUp(context.Background(), bucketStart.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	row := repo.rollups[bucketStart.Format(time.RFC3339)+"|cdn|hls-js|cmaf"]
	if row.EventCount != 8 || row.StartCount != 2 || row.RebufferCount != 2 ||
		row.BitrateSwitchCount != 1 || row.ErrorCount != 3 {
		t.Errorf("counts = event %d start %d rebuffer %d switch %d error %d, want 8/2/2/1/3",
			row.EventCount, row.StartCount, row.RebufferCount, row.BitrateSwitchCount, row.ErrorCount)
	}
	if row.VerifiedCount != 1 {
		t.Errorf("verified_count = %d, want 1 — the rest of the session ids are client-asserted and the row must say so", row.VerifiedCount)
	}
	if row.RebufferTotalMs != 2000 {
		t.Errorf("rebuffer_total_ms = %d, want 2000", row.RebufferTotalMs)
	}
	var classes map[string]int64
	if err := json.Unmarshal(row.ErrorCounts, &classes); err != nil {
		t.Fatalf("error_counts is not JSON: %v", err)
	}
	if classes["network"] != 2 || classes["media"] != 1 {
		t.Errorf("error_counts = %v, want network=2 media=1", classes)
	}
}

// TestRollUpPagesWithACompositeCursor: the raw table is UUID-keyed, so a cursor
// on id alone would page in an order unrelated to time and drop or repeat rows.
// This drives more events through than one page holds and checks nothing is
// lost.
func TestRollUpPagesWithACompositeCursor(t *testing.T) {
	repo := newFakeRepo()
	const n = 12000 // more than two rollupPageSize pages
	for i := 0; i < n; i++ {
		// Deliberately reuse a small id space and repeat timestamps, so a cursor
		// that used only one of the two columns would collide.
		e := rawEvent(bucketStart.Add(time.Duration(i/4)*time.Second), byte(i%251+1), EventStart, "cdn", EngineHLSJS, FormatCMAF)
		v := int32(i%1000 + 1)
		e.TtffMs = &v
		e.ID = uuid.New()
		repo.events = append(repo.events, e)
	}
	if _, err := quietService(repo).RollUp(context.Background(), bucketStart.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	row := repo.rollups[bucketStart.Format(time.RFC3339)+"|cdn|hls-js|cmaf"]
	if row.EventCount != n {
		t.Errorf("event_count = %d, want %d — the keyset page lost or repeated rows", row.EventCount, n)
	}
	if repo.pageCalls < 3 {
		t.Errorf("pageCalls = %d; the test did not actually exercise paging", repo.pageCalls)
	}
}

// TestRollUpLeavesTheOpenHourAlone: an hour that has not closed (plus the grace
// window) must not be rolled up, or its row would be a partial hour that never
// gets corrected.
func TestRollUpLeavesTheOpenHourAlone(t *testing.T) {
	repo := newFakeRepo()
	e := rawEvent(bucketStart.Add(30*time.Minute), 1, EventStart, "cdn", EngineHLSJS, FormatCMAF)
	v := int32(400)
	e.TtffMs = &v
	repo.events = append(repo.events, e)

	// "Now" is inside the same hour.
	hours, err := quietService(repo).RollUp(context.Background(), bucketStart.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	if hours != 0 || len(repo.rollups) != 0 {
		t.Errorf("rolled up the still-open hour (hours=%d rows=%d)", hours, len(repo.rollups))
	}
	// Just after the hour closes, the grace window still holds it back.
	if hours, _ := quietService(repo).RollUp(context.Background(), bucketStart.Add(time.Hour+time.Minute)); hours != 0 {
		t.Error("rolled up inside the grace window")
	}
	// Past the grace window it lands.
	if hours, _ := quietService(repo).RollUp(context.Background(), bucketStart.Add(time.Hour+10*time.Minute)); hours != 1 {
		t.Errorf("rolled %d hours after the grace window, want 1", hours)
	}
}

// TestRollUpIsIdempotent: leadership can move mid-sweep, so recomputing a bucket
// must land on the same numbers rather than doubling them.
func TestRollUpIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	for i := byte(1); i <= 10; i++ {
		e := rawEvent(bucketStart.Add(time.Duration(i)*time.Second), i, EventStart, "cdn", EngineHLSJS, FormatCMAF)
		v := int32(100 * int32(i))
		e.TtffMs = &v
		repo.events = append(repo.events, e)
	}
	svc := quietService(repo)
	now := bucketStart.Add(2 * time.Hour)
	if _, err := svc.RollUp(context.Background(), now); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	first := repo.rollups[bucketStart.Format(time.RFC3339)+"|cdn|hls-js|cmaf"]

	// Force a recomputation the way a leadership handover would: clear the
	// watermark's effect by re-rolling the same bucket directly.
	if err := svc.rollUpBucket(context.Background(), bucketStart); err != nil {
		t.Fatalf("rollUpBucket: %v", err)
	}
	second := repo.rollups[bucketStart.Format(time.RFC3339)+"|cdn|hls-js|cmaf"]
	if first.EventCount != second.EventCount || first.StartCount != second.StartCount {
		t.Errorf("recomputation changed the counts: %d/%d then %d/%d",
			first.EventCount, first.StartCount, second.EventCount, second.StartCount)
	}
}

// TestRollUpResumesFromTheWatermark: a second sweep must not redo work, which is
// what keeps an hourly worker from rescanning the whole retention window.
func TestRollUpResumesFromTheWatermark(t *testing.T) {
	repo := newFakeRepo()
	for h := 0; h < 3; h++ {
		e := rawEvent(bucketStart.Add(time.Duration(h)*time.Hour+time.Minute), byte(h+1), EventStart, "cdn", EngineHLSJS, FormatCMAF)
		v := int32(300)
		e.TtffMs = &v
		repo.events = append(repo.events, e)
	}
	svc := quietService(repo)
	now := bucketStart.Add(4 * time.Hour)
	if hours, err := svc.RollUp(context.Background(), now); err != nil || hours != 3 {
		t.Fatalf("first sweep rolled %d hours (err %v), want 3", hours, err)
	}
	if hours, err := svc.RollUp(context.Background(), now); err != nil || hours != 0 {
		t.Errorf("second sweep rolled %d hours (err %v), want 0 — the watermark did not hold", hours, err)
	}
}

// TestPruneWindowsAndBatching pins the retention contract: 7 days for raw
// measurements, 90 for rollups, 10k per batch, and batches repeated until the
// table is drained rather than one batch per tick.
func TestPruneWindowsAndBatching(t *testing.T) {
	repo := newFakeRepo()
	repo.pruneRemaining = 25000 // two full batches plus a short one
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	events, rollups, err := quietService(repo).Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if events != 25000 || rollups != 0 {
		t.Errorf("pruned events=%d rollups=%d, want 25000/0", events, rollups)
	}
	if len(repo.pruneEvents) != 3 {
		t.Fatalf("ran %d event batches, want 3 (10k + 10k + 5k)", len(repo.pruneEvents))
	}
	for i, call := range repo.pruneEvents {
		if call.BatchSize != pruneBatchSize {
			t.Errorf("batch %d size = %d, want %d", i, call.BatchSize, pruneBatchSize)
		}
		if want := now.Add(-RawRetention); !call.Cutoff.Equal(want) {
			t.Errorf("batch %d cutoff = %v, want %v (7 days)", i, call.Cutoff, want)
		}
	}
	if len(repo.pruneRolls) != 1 {
		t.Fatalf("ran %d rollup batches, want 1 (the first came back short)", len(repo.pruneRolls))
	}
	if want := now.Add(-RollupRetention); !repo.pruneRolls[0].Cutoff.Equal(want) {
		t.Errorf("rollup cutoff = %v, want %v (90 days)", repo.pruneRolls[0].Cutoff, want)
	}
}

// TestPruneIsCappedPerSweep: a catch-up sweep must not turn into an unbounded
// transaction storm on an instance that has been down.
func TestPruneIsCappedPerSweep(t *testing.T) {
	repo := newFakeRepo()
	repo.pruneRemaining = int64(pruneBatchSize) * (pruneMaxBatches + 50)
	events, _, err := quietService(repo).Prune(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := int64(pruneBatchSize) * pruneMaxBatches; events != want {
		t.Errorf("pruned %d rows in one sweep, want the cap of %d", events, want)
	}
}

// TestRecordSwallowsWriteFailures is the contract that keeps telemetry from
// becoming a cause of bad playback.
func TestRecordSwallowsWriteFailures(t *testing.T) {
	repo := newFakeRepo()
	repo.insertErr = errors.New("connection refused")
	counted := &countingMetrics{}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), WithMetrics(counted))

	// No panic, no return value to check: the only observable is the meter.
	svc.Record(context.Background(), validStart())
	if counted.drops["playback.start"] != 1 {
		t.Errorf("drops = %v, want one playback.start", counted.drops)
	}

	// A nil service and a service with no repository are both valid receivers,
	// so call sites can be wired unconditionally.
	var nilSvc *Service
	nilSvc.Record(context.Background(), validStart())
	NewService(nil, nil).Record(context.Background(), validStart())
}

// TestRecordStoresNoRawViewerIdentity: the row carries the keyed digest and
// nothing else that could name a person.
func TestRecordStoresNoRawViewerIdentity(t *testing.T) {
	repo := newFakeRepo()
	e := validStart()
	e.ViewerDigest = "deadbeef"
	e.Metadata = map[string]any{"network": "4g", "segment_url": "https://s3.example.net/k?X-Amz-Signature=x"}
	quietService(repo).Record(context.Background(), e)

	if len(repo.inserted) != 1 {
		t.Fatalf("inserted %d rows, want 1", len(repo.inserted))
	}
	got := repo.inserted[0]
	if got.ViewerDigest != "deadbeef" {
		t.Errorf("viewer_digest = %q", got.ViewerDigest)
	}
	if string(got.Metadata) == "" || bytesContains(got.Metadata, "X-Amz-Signature") || bytesContains(got.Metadata, "segment_url") {
		t.Errorf("metadata kept a disallowed key: %s", got.Metadata)
	}
	if !bytesContains(got.Metadata, "network") {
		t.Errorf("metadata dropped the allowlisted key: %s", got.Metadata)
	}
}

// TestUnavailableWithoutARepository: the workers must report a clear error
// rather than panicking on an install that wired no database.
func TestUnavailableWithoutARepository(t *testing.T) {
	svc := NewService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := svc.RollUp(context.Background(), time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("RollUp err = %v, want ErrUnavailable", err)
	}
	if _, _, err := svc.Prune(context.Background(), time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Prune err = %v, want ErrUnavailable", err)
	}
	if _, err := svc.PlaybackHealth(context.Background(), time.Now().Add(-time.Hour), time.Now(), 10, 0); !errors.Is(err, ErrUnavailable) {
		t.Errorf("PlaybackHealth err = %v, want ErrUnavailable", err)
	}
}

type countingMetrics struct{ drops map[string]int }

func (c *countingMetrics) IncQoEDrop(eventType string) {
	if c.drops == nil {
		c.drops = map[string]int{}
	}
	c.drops[eventType]++
}

func assertNear(t *testing.T, label string, got *int32, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want ~%v", label, want)
		return
	}
	if rel := abs(float64(*got)-want) / want; rel > relTolerance {
		t.Errorf("%s = %d, want ~%v", label, *got, want)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func bytesContains(b []byte, sub string) bool {
	return len(b) > 0 && len(sub) > 0 && indexOf(string(b), sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func keysOf(m map[string]sqlcgen.UpsertQoERollupParams) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
