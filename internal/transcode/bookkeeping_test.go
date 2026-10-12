package transcode

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The bookkeeping budget is a budget for the OUTCOME WRITES, not for the job.
//
// It used to be started before the transcode ran, which made it a silent 10s cap
// on the transcode itself: every real upload (minutes of ffmpeg) came back to an
// already-expired context, so the completion/reschedule write was rejected and
// the row stayed 'running' until the lease sweep ~30 minutes later. The partial
// unique index transcode_jobs_active_video_idx then reads that row as a live job:
// no re-enqueue, and admin re-transcode / video replace answer 409 for the whole
// window.
//
// The tests below pin that boundary without a real ten-second wait, by checking
// WHEN each write's budget was built relative to the end of a slow transcode.

// slowTranscoder takes longer than the bookkeeping budget, the way any
// real-world encode does. It ignores ctx: the transcode SUCCEEDS (or fails on
// its own terms) — what is under test is the write that records the outcome.
type slowTranscoder struct {
	delay time.Duration
	res   media.HLSResult
	err   error

	mu         sync.Mutex
	finishedAt time.Time
}

func (s *slowTranscoder) Transcode(_ context.Context, _ uuid.UUID, _ string) (media.HLSResult, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.finishedAt = time.Now()
	s.mu.Unlock()
	return s.res, s.err
}

func (s *slowTranscoder) finished() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishedAt
}

// budgetRepo records the deadline of the context each outcome write receives.
//
// The tests used to prove "the write got a live budget" by shrinking the budget
// to 2ms and letting the fake repo reject an expired context. That makes the
// assertion a race against the scheduler: the budget is wall-clock, so a
// goroutine descheduled for 2ms between building the context and reaching the
// write (routine on a loaded CI runner under -race) fails the write, and the
// job reads as stuck 'running'. The production budget is ten seconds and was
// never at fault; the 2ms was a test artefact.
//
// So the budget here is generous (never expires inside a test) and the property
// is asserted directly: each write's context was built AFTER the moment the
// work preceding it ended, i.e. deadline - budget >= that moment. The old
// placement (context built before runTarget) fails this by the whole transcode
// duration, independent of machine speed. ctxAwareRepo still rejects a
// cancelled context, so the detached-from-cancellation property is unchanged.
type budgetRepo struct {
	*ctxAwareRepo
	mu        sync.Mutex
	deadlines []time.Time
}

func (r *budgetRepo) note(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, _ := ctx.Deadline()
	r.deadlines = append(r.deadlines, d)
}

func (r *budgetRepo) CompleteTranscodeJob(ctx context.Context, id uuid.UUID) error {
	r.note(ctx)
	return r.ctxAwareRepo.CompleteTranscodeJob(ctx, id)
}

func (r *budgetRepo) RescheduleTranscodeJob(ctx context.Context, a sqlcgen.RescheduleTranscodeJobParams) error {
	r.note(ctx)
	return r.ctxAwareRepo.RescheduleTranscodeJob(ctx, a)
}

func (r *budgetRepo) FailTranscodeJob(ctx context.Context, a sqlcgen.FailTranscodeJobParams) error {
	r.note(ctx)
	return r.ctxAwareRepo.FailTranscodeJob(ctx, a)
}

func (r *budgetRepo) DeferTranscodeJob(ctx context.Context, a sqlcgen.DeferTranscodeJobParams) error {
	r.note(ctx)
	return r.ctxAwareRepo.DeferTranscodeJob(ctx, a)
}

// requireBuiltAfter fails unless at least one outcome write happened and every
// one ran under a budget that started at or after `after`.
func (r *budgetRepo) requireBuiltAfter(t *testing.T, after time.Time) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.deadlines) == 0 {
		t.Fatal("no outcome write reached the repo")
	}
	for _, d := range r.deadlines {
		if built := d.Add(-testBookkeeping); built.Before(after) {
			t.Fatalf("an outcome write's budget started %v before the work it records ended — "+
				"it was built too early and a long job would find it spent", after.Sub(built))
		}
	}
}

func newBudgetRepo() *budgetRepo {
	return &budgetRepo{ctxAwareRepo: &ctxAwareRepo{fakeRepo: newFakeRepo()}}
}

// The transcoder records when it finished; budgetRepo checks each outcome
// write's budget began after that. No wall-clock expiry is involved.
const (
	testBookkeeping = time.Hour // never expires inside a test; see budgetRepo
	slowTranscode   = 40 * time.Millisecond
)

// TestLongTranscodeStillCompletes is the regression test for the stuck-'running'
// row: a transcode that outlives the bookkeeping budget must still be marked
// done. Before the fix the job's media and playlist were promoted and the job row
// was left 'running', blocking every subsequent enqueue for that video.
func TestLongTranscodeStillCompletes(t *testing.T) {
	repo := newBudgetRepo()
	videoID := uuid.New()
	tc := &slowTranscoder{
		delay: slowTranscode,
		res:   media.HLSResult{MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8"},
	}
	svc := NewService(repo, tc)
	svc.bookkeepingTimeout = testBookkeeping

	if err := svc.Enqueue(context.Background(), videoID, "originals/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	n, err := svc.DrainJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if n != 1 {
		t.Errorf("DrainJobs completed %d jobs, want 1", n)
	}
	if j := repo.job(t, videoID); j.State != "done" {
		t.Fatalf("job state = %q after a transcode longer than the bookkeeping budget, want done — "+
			"the budget bounds the outcome WRITE, not the job; a stuck 'running' row blocks re-enqueue "+
			"and 409s admin re-transcode until the lease sweep", j.State)
	}
	repo.requireBuiltAfter(t, tc.finished())
	// A completed job is not live: the video can be re-enqueued immediately.
	if svc.HasLiveJob(context.Background(), videoID) {
		t.Error("video still reports a live job after its transcode completed")
	}
}

// TestLongFailingTranscodeIsRescheduled covers the failure path: recordFailure's
// reschedule write shares the same budget, so a long transcode that FAILS was
// never returned to the queue either — it stayed 'running' with its attempt
// uncounted.
func TestLongFailingTranscodeIsRescheduled(t *testing.T) {
	repo := newBudgetRepo()
	videoID := uuid.New()
	tc := &slowTranscoder{delay: slowTranscode, err: errors.New("ffmpeg: boom")}
	svc := NewService(repo, tc)
	svc.bookkeepingTimeout = testBookkeeping

	if err := svc.Enqueue(context.Background(), videoID, "originals/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	repo.requireBuiltAfter(t, tc.finished())
	j := repo.job(t, videoID)
	if j.State != "pending" || j.Attempts != 1 {
		t.Fatalf("job = state %q attempts %d after a long failing transcode, want pending/1 — "+
			"a failure must be rescheduled with a live bookkeeping context", j.State, j.Attempts)
	}
	if !j.NextAttemptAt.After(time.Now()) {
		t.Error("next attempt should be scheduled in the future (backoff)")
	}
}

// TestLongFailingTranscodeIsDeadLettered covers the terminal branch of
// recordFailure: the dead-letter write (and the failed-playlist marker it drives)
// needs the same live budget, or a video that can never be transcoded stays
// 'running' forever instead of failing visibly.
func TestLongFailingTranscodeIsDeadLettered(t *testing.T) {
	repo := newBudgetRepo()
	videoID := uuid.New()
	tc := &slowTranscoder{delay: slowTranscode, err: errors.New("ffmpeg: boom")}
	svc := NewService(repo, tc)
	svc.bookkeepingTimeout = testBookkeeping

	if err := svc.Enqueue(context.Background(), videoID, "originals/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Start on the last attempt so this drain dead-letters.
	repo.job(t, videoID).Attempts = maxAttempts - 1

	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	repo.requireBuiltAfter(t, tc.finished())
	if j := repo.job(t, videoID); j.State != "failed" || j.Attempts != maxAttempts {
		t.Fatalf("job = state %q attempts %d after the last attempt of a long transcode, want failed/%d",
			j.State, j.Attempts, maxAttempts)
	}
	if sp, ok := svc.Playlist(context.Background(), videoID); !ok || sp.State != PlaylistFailed {
		t.Errorf("playlist = (%+v, %v), want a failed marker after dead-letter", sp, ok)
	}
}

// TestBookkeepingBudgetStillSurvivesCancellation pins the property the budget
// exists for, so the fix above cannot be "solved" by simply passing the worker's
// context through: the writes must still land when the worker context is already
// cancelled AND the job outlived the budget.
func TestBookkeepingBudgetStillSurvivesCancellation(t *testing.T) {
	repo := newBudgetRepo()
	videoID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tc := &slowTranscoder{
		delay: slowTranscode,
		res:   media.HLSResult{MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8"},
	}
	svc := NewService(repo, tc)
	svc.bookkeepingTimeout = testBookkeeping
	if err := svc.Enqueue(context.Background(), videoID, "originals/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	cancel() // SIGTERM landed; the transcode still finished

	if _, err := svc.DrainJobs(ctx, 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if j := repo.job(t, videoID); j.State != "done" {
		t.Fatalf("job state = %q, want done — the bookkeeping context must stay DETACHED from the "+
			"worker's cancellation as well as freshly budgeted", j.State)
	}
}

// TestDeferredJobIsWrittenWithALiveBudget guards the third write path: the
// scratch-space deferral. It runs before any transcode, so it was never broken by
// the old placement — this pins that it keeps a live budget now that each write
// path builds its own.
func TestDeferredJobIsWrittenWithALiveBudget(t *testing.T) {
	repo := newBudgetRepo()
	videoID := uuid.New()
	repo.sizes["originals/huge.mp4"] = 4 << 30 // 12 GiB estimated need

	svc := NewService(repo, &fakeTranscoder{},
		WithScratchGuard(func() (uint64, error) { return 11 << 30, nil }, 10<<30))
	svc.bookkeepingTimeout = testBookkeeping

	start := time.Now()
	if err := svc.Enqueue(context.Background(), videoID, "originals/huge.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	repo.requireBuiltAfter(t, start)
	j := repo.job(t, videoID)
	if j.State != "pending" || j.Attempts != 0 {
		t.Fatalf("job = state %q attempts %d, want pending/0 (deferred, no attempt consumed)", j.State, j.Attempts)
	}
	if !j.NextAttemptAt.After(time.Now()) {
		t.Error("deferred job should be scheduled in the future")
	}
}
