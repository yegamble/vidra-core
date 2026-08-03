package transcode

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ctxAwareRepo is a fakeRepo whose queue-state writes honour their context, as
// a real pgx-backed repository does. The plain fakeRepo ignores ctx, which is
// exactly why the shutdown bug went unnoticed: with a cancelled context the
// database rejects the write, but the fake happily accepted it.
type ctxAwareRepo struct {
	*fakeRepo
}

func (r *ctxAwareRepo) CompleteTranscodeJob(ctx context.Context, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.fakeRepo.CompleteTranscodeJob(ctx, id)
}

func (r *ctxAwareRepo) RescheduleTranscodeJob(ctx context.Context, a sqlcgen.RescheduleTranscodeJobParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.fakeRepo.RescheduleTranscodeJob(ctx, a)
}

// cancellingTranscoder simulates SIGTERM landing mid-transcode: it cancels the
// worker context (as the shutdown path does) and then fails the way an
// interrupted ffmpeg does.
type cancellingTranscoder struct{ cancel context.CancelFunc }

func (c *cancellingTranscoder) Transcode(ctx context.Context, _ uuid.UUID, _ string) (media.HLSResult, error) {
	c.cancel()
	<-ctx.Done()
	return media.HLSResult{}, errors.New("ffmpeg: interrupted")
}

// TestShutdownStillReschedulesTheJob is the regression test for the silent-drop
// on shutdown: DrainJobs used to pass the worker's (by then CANCELLED) context
// into the failure bookkeeping, so on every deploy the reschedule write was
// rejected and the row stayed 'running' forever — which the partial unique index
// then treats as a live job, making re-enqueue a no-op and the admin
// re-transcode endpoint a permanent 409. The bookkeeping context must be
// detached from cancellation.
func TestShutdownStillReschedulesTheJob(t *testing.T) {
	repo := &ctxAwareRepo{fakeRepo: newFakeRepo()}
	videoID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc := NewService(repo, &cancellingTranscoder{cancel: cancel})
	if err := svc.Enqueue(context.Background(), videoID, "originals/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if _, err := svc.DrainJobs(ctx, 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}

	j := repo.job(t, videoID)
	if j.State != "pending" {
		t.Fatalf("job state = %q after shutdown, want pending — a job abandoned on shutdown must return to the queue, not stay 'running' forever", j.State)
	}
	if j.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (the abandoned run must count)", j.Attempts)
	}
}

// TestShutdownStillCompletesAFinishedJob covers the mirror case: a transcode
// that finished just as shutdown began must still be marked done, or the next
// boot re-runs a full ffmpeg ladder that was already paid for.
func TestShutdownStillCompletesAFinishedJob(t *testing.T) {
	repo := &ctxAwareRepo{fakeRepo: newFakeRepo()}
	videoID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tc := &fakeTranscoder{res: media.HLSResult{MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8"}}
	svc := NewService(repo, tc)
	if err := svc.Enqueue(context.Background(), videoID, "originals/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Cancel before the drain: the transcode still succeeds (the fake ignores
	// ctx, as an ffmpeg run that finished just before SIGTERM would), so only the
	// completion write is left racing the cancellation.
	cancel()

	if _, err := svc.DrainJobs(ctx, 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if j := repo.job(t, videoID); j.State != "done" {
		t.Fatalf("job state = %q, want done — a completed transcode must not be re-run on the next boot", j.State)
	}
}
