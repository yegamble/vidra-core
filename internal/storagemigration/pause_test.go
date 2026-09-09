package storagemigration

import (
	"context"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// newPausedTestService is newTestService with a write-probe verdict already
// recorded against the target, which is what boot does when EnsureBucket comes
// back write_denied.
func newPausedTestService(t *testing.T) (*Service, *fakeRepo, *storage.WriteHealth) {
	t.Helper()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	health := storage.NewWriteHealth(dst, storage.WriteProbeInterval)
	return NewService(repo, src, dst, Config{TargetWrite: health, WorkerID: "test-host:1"}), repo, health
}

// SC1, the api half: a target the process cannot write to must not stop the
// campaign from EXISTING or the instance from running — it must park the
// campaign with a typed reason.
//
// A34 booted against a read-only target credential and got `fatal error=
// "storage migration target: … Access Denied. [write_denied]"` with the process
// exiting before the listener opened. This is the same fact arriving as a
// paused campaign instead.
func TestAWriteDeniedTargetPausesTheCampaign(t *testing.T) {
	ctx := context.Background()
	svc, repo, health := newPausedTestService(t)

	put(t, svc.primary, "web-videos/a.mp4", "the original bytes")
	put(t, svc.primary, "thumbnails/a.jpg", "poster")

	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The credential is narrowed WHILE the campaign runs.
	health.RecordDenied(storage.ClassWriteDenied)

	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	got, _, _, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StatePaused {
		t.Fatalf("state = %q, want %q", got.State, StatePaused)
	}
	if got.PausedReason != PauseReasonTargetWriteDenied {
		t.Errorf("paused_reason = %q, want %q", got.PausedReason, PauseReasonTargetWriteDenied)
	}
	if got.ResumeState == "" {
		t.Error("the campaign forgot which phase to resume into")
	}
	// The operator-facing sentence must say the INSTANCE is fine. Getting that
	// backwards is how an operator restarts a healthy api at 3am.
	if !strings.Contains(got.LastError, "otherwise unaffected") {
		t.Errorf("last_error does not say the instance is unaffected: %q", got.LastError)
	}

	// A paused campaign claims nothing. That is what "paused" has to mean, or
	// the workers keep burning attempts against a store that refuses them.
	n, err := svc.CopyOnce(ctx, 8)
	if err != nil {
		t.Fatalf("CopyOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("a paused campaign claimed %d objects", n)
	}

	// Nothing was deleted from the source, which is the property that makes a
	// pause safe to sit in indefinitely.
	keys, err := svc.primary.(storage.RootLister).ListAllKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Errorf("the source holds %d objects, want 2 — a pause must delete nothing", len(keys))
	}
	_ = repo
}

// SC1, the worker half: the pause LIFTS on its own. A campaign that needed an
// admin to notice a recovered credential would be a worse outcome than the boot
// refusal it replaced — at least that one was loud.
func TestARecoveredTargetResumesTheCampaign(t *testing.T) {
	ctx := context.Background()
	svc, _, health := newPausedTestService(t)

	put(t, svc.primary, "web-videos/a.mp4", "the original bytes")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	health.RecordDenied(storage.ClassWriteDenied)
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got, _, _, _ := svc.Get(ctx, camp.ID); got.State != StatePaused {
		t.Fatalf("state = %q, want paused", got.State)
	}

	// The credential is repaired: the next probe succeeds.
	if err := health.Probe(ctx); err != nil {
		t.Fatalf("probe after repair: %v", err)
	}

	drain(t, svc)
	got, _, _, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State == StatePaused {
		t.Fatalf("the campaign is still paused after the target accepted writes again: %+v", got)
	}
	if got.PausedReason != "" {
		t.Errorf("paused_reason = %q on a resumed campaign", got.PausedReason)
	}
	if got.State != StateSynced {
		t.Errorf("state = %q, want %q — the campaign should have finished copying", got.State, StateSynced)
	}
	if body := read(t, svc.target, "web-videos/a.mp4"); body != "the original bytes" {
		t.Errorf("the object did not arrive after the resume: %q", body)
	}
}

// An OPERATOR pause is not lifted by a healthy probe. The two reasons carry
// different authority on purpose: an admin who paused a move to take a
// maintenance window must not find it running again five minutes later.
func TestAnOperatorPauseSurvivesAHealthyProbe(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newPausedTestService(t)
	put(t, svc.primary, "web-videos/a.mp4", "the original bytes")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := svc.Pause(ctx, camp.ID, PauseReasonOperator); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	drain(t, svc)
	got, _, _, _ := svc.Get(ctx, camp.ID)
	if got.State != StatePaused {
		t.Fatalf("an operator pause was lifted by the sweep: state = %q", got.State)
	}
	if got.PausedReason != PauseReasonOperator {
		t.Errorf("paused_reason = %q, want %q", got.PausedReason, PauseReasonOperator)
	}

	// And an explicit resume puts it back exactly where it was.
	if _, err := svc.Resume(ctx, camp.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	drain(t, svc)
	if got, _, _, _ := svc.Get(ctx, camp.ID); got.State != StateSynced {
		t.Errorf("after resume state = %q, want %q", got.State, StateSynced)
	}
}

// Starting a campaign into a store that will not accept a write is refused at
// the START. Walking an operator into a campaign that parks on its first tick
// leaves them a row they did not ask for; the refusal is the same information
// with nothing to clean up.
func TestStartRefusesAWriteDeniedTarget(t *testing.T) {
	ctx := context.Background()
	svc, _, health := newPausedTestService(t)
	put(t, svc.primary, "web-videos/a.mp4", "bytes")
	health.RecordDenied(storage.ClassWriteDenied)

	if _, err := svc.Start(ctx); err == nil {
		t.Fatal("a campaign was started into a store that refuses writes")
	} else if err != ErrTargetWriteDenied {
		t.Errorf("Start error = %v, want ErrTargetWriteDenied", err)
	}
}

// A resume is refused while the target is still write-denied, for the same
// reason: it would pause again on the next sweep, and a button that silently
// undoes itself within the minute is worse than one that says why.
func TestResumeRefusesAStillWriteDeniedTarget(t *testing.T) {
	ctx := context.Background()
	svc, _, health := newPausedTestService(t)
	put(t, svc.primary, "web-videos/a.mp4", "bytes")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	health.RecordDenied(storage.ClassWriteDenied)
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if _, err := svc.Resume(ctx, camp.ID); err != ErrTargetWriteDenied {
		t.Errorf("Resume with a denied target = %v, want ErrTargetWriteDenied", err)
	}
	if got, _, _, _ := svc.Get(ctx, camp.ID); got.State != StatePaused {
		t.Errorf("the campaign left paused state on a refused resume: %q", got.State)
	}
}

// The transition table, as refusals. Every illegal control names the state the
// campaign is ACTUALLY in, because the commonest cause is a stale page.
func TestIllegalTransitionsNameTheState(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestService(t)
	put(t, svc.primary, "web-videos/a.mp4", "bytes")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, svc) // -> synced

	// Resume is not legal on a running campaign.
	_, err = svc.Resume(ctx, camp.ID)
	var ill *IllegalTransitionError
	if !asIllegal(err, &ill) {
		t.Fatalf("Resume on a synced campaign = %v, want an IllegalTransitionError", err)
	}
	if ill.State != StateSynced {
		t.Errorf("the refusal names state %q, want %q", ill.State, StateSynced)
	}

	// Release is not legal before cutover: the api is still serving from the
	// source, and this is the request that would delete it.
	_, err = svc.Release(ctx, camp.ID)
	if !asIllegal(err, &ill) {
		t.Fatalf("Release before cutover = %v, want an IllegalTransitionError", err)
	}

	// Switch is refused with its OWN reason while the topology is forward — the
	// environment swap has not happened, and saying "illegal transition" would
	// hide which half of a two-step action is missing.
	if _, err := svc.Switch(ctx, camp.ID); err != ErrCutoverNotObserved {
		t.Errorf("Switch in the forward topology = %v, want ErrCutoverNotObserved", err)
	}
}

func asIllegal(err error, target **IllegalTransitionError) bool {
	e, ok := err.(*IllegalTransitionError)
	if ok {
		*target = e
	}
	return ok
}

// A write-denied "target" must NOT stall the delete-source phase, and this is
// the test for a bug the pause rail introduced and review caught.
//
// After the operator's env swap the handle the target monitor probes is the OLD
// SOURCE — the store being decommissioned — not the one the api serves from. Its
// refusing a write probe says nothing about whether the campaign may finish
// deleting it. Worse, pausing is not legal from `cutover` or `deleting_source`
// at all, so the guarded UPDATE matched no row while the rail logged that it had
// paused something and told the sweep to stop: a delete-source phase would have
// stalled for as long as a store nobody writes to kept refusing writes, on a lie
// in the log.
func TestAWriteDeniedTargetDoesNotStallTheDeleteSourcePhase(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	forward := NewService(repo, src, dst, Config{})

	put(t, src, "web-videos/a.mp4", "bytes a")
	put(t, src, "thumbnails/a.jpg", "bytes b")
	camp, err := forward.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, forward)

	// THE ENV SWAP, plus a monitor that says the handle now pointing at the OLD
	// SOURCE will not accept a write. Which is true, and irrelevant.
	health := storage.NewWriteHealth(src, storage.WriteProbeInterval)
	health.RecordDenied(storage.ClassWriteDenied)
	swapped := NewService(repo, dst, src, Config{Grace: 0, TargetWrite: health})

	drain(t, swapped)

	got, _, _, err := swapped.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State == StatePaused {
		t.Fatalf("a post-cutover campaign was paused by the OLD store's write probe: %+v", got)
	}
	if got.State != StateDone {
		t.Fatalf("state = %q, want %q — the delete-source phase stalled", got.State, StateDone)
	}
	if ok, _ := src.Exists(ctx, "web-videos/a.mp4"); ok {
		t.Error("the source copies were never deleted")
	}
}
