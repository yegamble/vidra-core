package storyboardbackfill

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The fake repository is a small in-memory model of the catalogue, and its
// ListVideosNeedingStoryboard applies the SAME eligibility predicate the real
// query does. That is deliberate: it pins the CONTRACT the service is written
// against (a video with a sheet is never handed back; one without an original is
// never handed back; a given-up or not-yet-due one is never handed back), so a
// service change that started relying on something else fails here. It does not
// and cannot prove the SQL — store/storyboard_backfill_integration_test.go runs
// the real query against a real database for that.

type fakeVideo struct {
	id uuid.UUID
	// originalKey empty means the video has no kind='original' row: an HLS-only
	// video, which is not eligible.
	originalKey   string
	duration      int32
	hasStoryboard bool
}

type ledgerRow struct {
	attempts      int32
	nextAttemptAt time.Time
	givenUp       bool
	lastError     string
}

type fakeRepo struct {
	videos  []*fakeVideo
	ledger  map[uuid.UUID]*ledgerRow
	now     func() time.Time
	listErr error
}

func newFakeRepo(now func() time.Time) *fakeRepo {
	return &fakeRepo{ledger: map[uuid.UUID]*ledgerRow{}, now: now}
}

func (f *fakeRepo) add(v *fakeVideo) *fakeVideo {
	v.id = uuid.New()
	f.videos = append(f.videos, v)
	return v
}

func (f *fakeRepo) ListVideosNeedingStoryboard(_ context.Context, limit int32) ([]sqlcgen.ListVideosNeedingStoryboardRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []sqlcgen.ListVideosNeedingStoryboardRow
	for _, v := range f.videos {
		if v.hasStoryboard || v.originalKey == "" {
			continue
		}
		if l, ok := f.ledger[v.id]; ok {
			if l.givenUp || l.nextAttemptAt.After(f.now()) {
				continue
			}
		}
		if int32(len(out)) == limit {
			break
		}
		out = append(out, sqlcgen.ListVideosNeedingStoryboardRow{
			ID:              v.id,
			StorageKey:      v.originalKey,
			DurationSeconds: v.duration,
			Attempts:        f.attemptsOf(v.id),
		})
	}
	return out, nil
}

func (f *fakeRepo) attemptsOf(id uuid.UUID) int32 {
	if l, ok := f.ledger[id]; ok {
		return l.attempts
	}
	return 0
}

func (f *fakeRepo) RecordStoryboardAttemptFailure(_ context.Context, arg sqlcgen.RecordStoryboardAttemptFailureParams) (sqlcgen.RecordStoryboardAttemptFailureRow, error) {
	l, ok := f.ledger[arg.VideoID]
	if !ok {
		l = &ledgerRow{}
		f.ledger[arg.VideoID] = l
	}
	l.attempts++
	l.nextAttemptAt = arg.NextAttemptAt
	l.lastError = arg.LastError
	l.givenUp = l.attempts >= arg.MaxAttempts
	return sqlcgen.RecordStoryboardAttemptFailureRow{Attempts: l.attempts, GivenUp: l.givenUp}, nil
}

func (f *fakeRepo) GiveUpOnStoryboard(_ context.Context, arg sqlcgen.GiveUpOnStoryboardParams) error {
	l, ok := f.ledger[arg.VideoID]
	if !ok {
		l = &ledgerRow{}
		f.ledger[arg.VideoID] = l
	}
	l.attempts++
	l.lastError = arg.LastError
	l.givenUp = true
	return nil
}

func (f *fakeRepo) ClearStoryboardAttempt(_ context.Context, videoID uuid.UUID) error {
	delete(f.ledger, videoID)
	return nil
}

// fakeGen records what it was asked to do and answers with a scripted error. On
// success it flips the video's hasStoryboard, the way a real generation does by
// writing the video_files rows the scan then sees.
type fakeGen struct {
	repo  *fakeRepo
	err   error
	calls []uuid.UUID
	hints []int
}

func (g *fakeGen) GenerateStoryboard(_ context.Context, videoID uuid.UUID, _ string, durationHint int) error {
	g.calls = append(g.calls, videoID)
	g.hints = append(g.hints, durationHint)
	if g.err != nil {
		return g.err
	}
	for _, v := range g.repo.videos {
		if v.id == videoID {
			v.hasStoryboard = true
		}
	}
	return nil
}

// newFixture wires a service over a fake repo and generator with a clock the
// test drives, so backoff can be crossed without sleeping.
func newFixture(t *testing.T) (*Service, *fakeRepo, *fakeGen, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repo := newFakeRepo(func() time.Time { return clock })
	gen := &fakeGen{repo: repo}
	svc := NewService(repo, gen, nil)
	svc.now = func() time.Time { return clock }
	return svc, repo, gen, &clock
}

func TestBackfillGeneratesAMissingStoryboard(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, _ := newFixture(t)
	v := repo.add(&fakeVideo{originalKey: "web-videos/a.mp4", duration: 300})

	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 1 || res.Generated != 1 || res.Retrying != 0 || res.GaveUp != 0 {
		t.Fatalf("result = %+v, want one generated", res)
	}
	if len(gen.hints) != 1 || gen.hints[0] != 300 {
		t.Errorf("duration hints = %v, want the recorded 300s passed through", gen.hints)
	}
	if _, booked := repo.ledger[v.id]; booked {
		t.Error("a successful generation left a ledger row behind")
	}
	// And it is finished: the sheet is what keeps it out of the scan from now on.
	res, err = svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("second BackfillOnce: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d after a success, want 0", res.Scanned)
	}
}

// A retryable failure parks the video behind a backoff — NOT back into the next
// tick, which for a full video decode is the expensive mistake.
func TestBackfillSchedulesARetryAfterAFailure(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, clock := newFixture(t)
	gen.err = errors.New("object store unavailable")
	v := repo.add(&fakeVideo{originalKey: "web-videos/a.mp4", duration: 300})

	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce returned an error for a per-video failure: %v", err)
	}
	if res.Retrying != 1 || res.GaveUp != 0 || res.Generated != 0 {
		t.Fatalf("result = %+v, want one retry scheduled", res)
	}
	l := repo.ledger[v.id]
	if l == nil || l.attempts != 1 || l.givenUp {
		t.Fatalf("ledger = %+v, want one attempt and not given up", l)
	}
	if want := clock.Add(backoffBase); !l.nextAttemptAt.Equal(want) {
		t.Errorf("next attempt at %v, want %v (one backoffBase out)", l.nextAttemptAt, want)
	}

	// The very next tick must not touch it again.
	if res, err = svc.BackfillOnce(ctx, 25); err != nil || res.Scanned != 0 {
		t.Fatalf("BackfillOnce during the backoff: scanned %d (err %v), want 0", res.Scanned, err)
	}
	if len(gen.calls) != 1 {
		t.Fatalf("generator called %d times, want 1: a backed-off video was re-decoded", len(gen.calls))
	}

	// Once the backoff elapses it is a candidate again.
	*clock = clock.Add(backoffBase)
	if res, err = svc.BackfillOnce(ctx, 25); err != nil || res.Scanned != 1 {
		t.Fatalf("BackfillOnce after the backoff: scanned %d (err %v), want 1", res.Scanned, err)
	}
}

// The budget is bounded, and the give-up is permanent: after MaxAttempts the
// video is never handed to the generator again, however many ticks pass.
func TestBackfillGivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, clock := newFixture(t)
	gen.err = errors.New("ffmpeg: invalid data found when processing input")
	v := repo.add(&fakeVideo{originalKey: "web-videos/broken.mp4", duration: 300})

	for i := 1; i <= MaxAttempts; i++ {
		res, err := svc.BackfillOnce(ctx, 25)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if res.Scanned != 1 {
			t.Fatalf("attempt %d: scanned %d, want 1", i, res.Scanned)
		}
		last := i == MaxAttempts
		if last && res.GaveUp != 1 {
			t.Fatalf("attempt %d (the last): result = %+v, want a give-up", i, res)
		}
		if !last && res.Retrying != 1 {
			t.Fatalf("attempt %d: result = %+v, want a retry", i, res)
		}
		*clock = clock.Add(backoffMax)
	}
	l := repo.ledger[v.id]
	if l == nil || !l.givenUp || l.attempts != MaxAttempts {
		t.Fatalf("ledger = %+v, want given up after %d attempts", l, MaxAttempts)
	}
	if l.lastError == "" {
		t.Error("the give-up recorded no reason; an operator has nothing to read")
	}

	// Terminal: not a candidate again, ever.
	*clock = clock.Add(30 * 24 * time.Hour)
	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce after the give-up: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d a month after giving up, want 0", res.Scanned)
	}
	if len(gen.calls) != MaxAttempts {
		t.Errorf("generator called %d times, want exactly %d", len(gen.calls), MaxAttempts)
	}
}

// An unmeasurable duration is the one provably permanent failure: the sprite
// layout is computed from the duration and nothing else. It must skip the retry
// budget entirely rather than spending four more full decodes to reach the same
// answer.
func TestBackfillGivesUpImmediatelyOnAnUnmeasurableDuration(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, clock := newFixture(t)
	gen.err = fmt.Errorf("%w: %q", media.ErrNoMeasurableDuration, "web-videos/silent.mp4")
	v := repo.add(&fakeVideo{originalKey: "web-videos/silent.mp4"})

	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.GaveUp != 1 || res.Retrying != 0 {
		t.Fatalf("result = %+v, want an immediate give-up and no retry", res)
	}
	l := repo.ledger[v.id]
	if l == nil || !l.givenUp || l.attempts != 1 {
		t.Fatalf("ledger = %+v, want given up on the first attempt", l)
	}

	*clock = clock.Add(365 * 24 * time.Hour)
	if res, err = svc.BackfillOnce(ctx, 25); err != nil || res.Scanned != 0 {
		t.Fatalf("BackfillOnce a year later: scanned %d (err %v), want 0", res.Scanned, err)
	}
	if len(gen.calls) != 1 {
		t.Errorf("generator called %d times, want exactly 1", len(gen.calls))
	}
}

// A video with only an HLS tree has no single object to decode. It is not a
// failure and must not be booked as one — it is simply out of scope.
func TestBackfillSkipsVideosWithNoOriginal(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, _ := newFixture(t)
	v := repo.add(&fakeVideo{originalKey: "", duration: 300})

	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d, want 0 for a video with no original", res.Scanned)
	}
	if len(gen.calls) != 0 {
		t.Error("a video with no original was handed to the generator")
	}
	if _, booked := repo.ledger[v.id]; booked {
		t.Error("a video with no original was booked as a failure")
	}
}

func TestBackfillNeverSelectsAVideoThatAlreadyHasAStoryboard(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, _ := newFixture(t)
	repo.add(&fakeVideo{originalKey: "web-videos/done.mp4", duration: 300, hasStoryboard: true})

	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d, want 0 for a video that already has a sheet", res.Scanned)
	}
	if len(gen.calls) != 0 {
		t.Error("a video that already has a storyboard was re-generated")
	}
}

// One bad video must not stall the rest of the catalogue.
func TestBackfillContinuesPastAPerVideoFailure(t *testing.T) {
	ctx := context.Background()
	svc, repo, gen, _ := newFixture(t)
	repo.add(&fakeVideo{originalKey: "web-videos/a.mp4", duration: 300})
	repo.add(&fakeVideo{originalKey: "web-videos/b.mp4", duration: 300})
	// Fail the first video only: swap the scripted error away after one call.
	failing := &failFirstGen{inner: gen}
	svc.gen = failing

	res, err := svc.BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 2 || res.Generated != 1 || res.Retrying != 1 {
		t.Fatalf("result = %+v, want the second video generated despite the first failing", res)
	}
}

type failFirstGen struct {
	inner *fakeGen
	n     int
}

func (g *failFirstGen) GenerateStoryboard(ctx context.Context, videoID uuid.UUID, key string, hint int) error {
	g.n++
	if g.n == 1 {
		return errors.New("ffmpeg boom")
	}
	return g.inner.GenerateStoryboard(ctx, videoID, key, hint)
}

// The drained-for-now state — what the worker's edge-triggered log is keyed on.
func TestBackfillReportsNothingDue(t *testing.T) {
	svc, _, _, _ := newFixture(t)
	res, err := svc.BackfillOnce(context.Background(), 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d on an empty backlog, want 0", res.Scanned)
	}
}

// A database error is the one thing that aborts the pass: a pass whose scan
// failed has established nothing about the catalogue.
func TestBackfillReturnsScanErrors(t *testing.T) {
	svc, repo, _, _ := newFixture(t)
	repo.listErr = errors.New("connection reset")
	if _, err := svc.BackfillOnce(context.Background(), 25); err == nil {
		t.Fatal("BackfillOnce swallowed a scan error")
	}
}

// A canceled context is the worker shutting down, not the video's fault: it must
// not spend one of the video's five attempts.
func TestBackfillDoesNotBookAFailureOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	svc, repo, gen, _ := newFixture(t)
	v := repo.add(&fakeVideo{originalKey: "web-videos/a.mp4", duration: 300})
	gen.err = context.Canceled
	svc.gen = cancelingGen{cancel: cancel, inner: gen}

	if _, err := svc.BackfillOnce(ctx, 25); !errors.Is(err, context.Canceled) {
		t.Fatalf("BackfillOnce error = %v, want context.Canceled", err)
	}
	if _, booked := repo.ledger[v.id]; booked {
		t.Error("a shutdown was booked against the video as a failed attempt")
	}
}

type cancelingGen struct {
	cancel context.CancelFunc
	inner  *fakeGen
}

func (g cancelingGen) GenerateStoryboard(ctx context.Context, videoID uuid.UUID, key string, hint int) error {
	g.cancel()
	return g.inner.GenerateStoryboard(ctx, videoID, key, hint)
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	if got := backoffFor(0); got != backoffBase {
		t.Errorf("backoffFor(0) = %v, want %v", got, backoffBase)
	}
	if got := backoffFor(1); got != 2*backoffBase {
		t.Errorf("backoffFor(1) = %v, want %v", got, 2*backoffBase)
	}
	for _, n := range []int{5, 10, 100} {
		if got := backoffFor(n); got != backoffMax {
			t.Errorf("backoffFor(%d) = %v, want the %v cap", n, got, backoffMax)
		}
	}
}
