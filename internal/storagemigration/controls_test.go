package storagemigration

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// SC2: the destination clean-up. A34's eighth finding was that an aborted
// campaign's partial copies stay on the destination FOREVER — nothing in the
// product removes them, and emptying the bucket is the operator's job, on a
// store they may not have console access to.
//
// What is asserted here is both halves: the copies go, and the SOURCE does not.
// Getting that backwards is the one mistake this whole package exists to
// prevent, and the abort is the path where the two handles are easiest to
// confuse — the destination is s.target in the forward topology and the SOURCE
// in the swapped one.
func TestAbortWithCleanupRemovesTheDestinationCopiesAndNothingElse(t *testing.T) {
	ctx := context.Background()
	svc, _, src, dst := newTestService(t)

	bodies := map[string]string{
		"web-videos/a.mp4": "the original bytes",
		"thumbnails/a.jpg": "poster",
		"captions/a.vtt":   "WEBVTT",
	}
	for k, v := range bodies {
		put(t, src, k, v)
	}
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, svc)
	for k := range bodies {
		if ok, _ := dst.Exists(ctx, k); !ok {
			t.Fatalf("%q never reached the destination, so the clean-up would prove nothing", k)
		}
	}

	got, err := svc.Abort(ctx, camp.ID, true)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if got.State != StateAborting {
		t.Fatalf("state = %q, want %q", got.State, StateAborting)
	}

	drain(t, svc)
	final, _, _, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.State != StateCancelled {
		t.Fatalf("state after the clean-up drained = %q, want %q", final.State, StateCancelled)
	}
	for k := range bodies {
		if ok, _ := dst.Exists(ctx, k); ok {
			t.Errorf("%q is still on the destination after an abort with clean-up", k)
		}
		if ok, _ := src.Exists(ctx, k); !ok {
			t.Fatalf("%q was deleted from the SOURCE by an abort — the abort deleted the library", k)
		}
		if body := read(t, src, k); body != bodies[k] {
			t.Errorf("the source copy of %q changed: %q", k, body)
		}
	}
}

// The default is unchanged, and deliberately so: without the flag an abort is
// exactly the cancel it always was, and the copies stay where they are. They
// are byte-identical objects under identical keys and inert until some future
// campaign re-verifies them; deleting them by default would be a destructive
// act taken on the way OUT of a destructive operation, chosen for the operator.
func TestAbortWithoutCleanupLeavesTheDestinationAlone(t *testing.T) {
	ctx := context.Background()
	svc, _, src, dst := newTestService(t)
	put(t, src, "web-videos/a.mp4", "the original bytes")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, svc)

	got, err := svc.Abort(ctx, camp.ID, false)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if got.State != StateCancelled {
		t.Errorf("state = %q, want %q", got.State, StateCancelled)
	}
	if ok, _ := dst.Exists(ctx, "web-videos/a.mp4"); !ok {
		t.Error("a plain abort deleted a destination copy")
	}
}

// SC2: the preview. "Start a storage migration" is a button whose consequence
// an operator cannot otherwise see, and a preview that created a campaign row
// would defeat its own purpose.
func TestPreviewCountsWithoutCreatingAnything(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, _ := newTestService(t)
	bodies := map[string]string{
		"web-videos/a.mp4": "0123456789",
		"thumbnails/a.jpg": "abc",
	}
	for k, v := range bodies {
		put(t, src, k, v)
	}

	pv, err := svc.Preview(ctx)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pv.Objects != 2 {
		t.Errorf("objects = %d, want 2", pv.Objects)
	}
	if !pv.BytesKnown {
		t.Fatal("a local store can report sizes; bytes_known must be true")
	}
	if pv.Bytes != 13 {
		t.Errorf("bytes = %d, want 13", pv.Bytes)
	}
	if pv.SourceDesc == "" || pv.TargetDesc == "" || pv.SourceDesc == pv.TargetDesc {
		t.Errorf("the preview does not name two distinct stores: %+v", pv)
	}
	if len(repo.campaigns) != 0 {
		t.Errorf("the preview created %d campaign rows; it must create none", len(repo.campaigns))
	}
	if len(repo.objects) != 0 {
		t.Errorf("the preview wrote %d ledger rows; it must write none", len(repo.objects))
	}
}

// SC2: the switch and the release, as the two explicit steps they are.
//
// Switch RECORDS a cutover the operator has already performed in the
// environment — it cannot perform one — and Release ends the grace window,
// which is the act that makes the move irreversible. Neither is a consequence
// of the other, and the release re-checks the one precondition that cannot be
// waived.
func TestSwitchThenReleaseAreTwoSeparateOperatorSteps(t *testing.T) {
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

	// Step one is the operator's, and this process cannot do it: while the
	// environment still points the original way round, there is no cutover to
	// record and the refusal says which half is missing.
	if _, err := forward.Switch(ctx, camp.ID); err != ErrCutoverNotObserved {
		t.Fatalf("Switch before the env swap = %v, want ErrCutoverNotObserved", err)
	}

	// The env swap plus a restart: same two backends, other way round.
	swapped := NewService(repo, dst, src, Config{Grace: time.Hour})
	got, err := swapped.Switch(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Switch after the swap: %v", err)
	}
	if got.State != StateCutover {
		t.Fatalf("state = %q, want %q", got.State, StateCutover)
	}
	if got.ObservedCutoverAt == nil {
		t.Fatal("recording the cutover did not start the grace clock")
	}

	// A whole grace hour has NOT elapsed, and the sweep will not delete a thing.
	if err := swapped.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce in grace: %v", err)
	}
	if ok, _ := src.Exists(ctx, "web-videos/a.mp4"); !ok {
		t.Fatal("the source was deleted inside the grace window")
	}

	// Step two, explicit: release the source. It is the same instance and the
	// same grace hour — the difference is that an operator asked.
	if _, err := swapped.Release(ctx, camp.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got, _, _, _ := swapped.Get(ctx, camp.ID); got.State != StateDeletingSource {
		t.Fatalf("state after release = %q, want %q", got.State, StateDeletingSource)
	}
	drain(t, swapped)
	if ok, _ := src.Exists(ctx, "web-videos/a.mp4"); ok {
		t.Error("the released source still holds its copies")
	}
	if body := read(t, dst, "web-videos/a.mp4"); body != "bytes a" {
		t.Errorf("the destination lost bytes during the release: %q", body)
	}
}

// The precondition Release cannot waive: an environment swapped before the copy
// finished leaves objects the new store does not hold, and this is the request
// that would delete their only remaining copy.
func TestReleaseRefusesWhileObjectsAreUncopied(t *testing.T) {
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
	// Enumerate, then copy ONE object: the ledger knows about two.
	if err := forward.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if _, err := forward.CopyOnce(ctx, 1); err != nil {
		t.Fatalf("CopyOnce: %v", err)
	}

	swapped := NewService(repo, dst, src, Config{Grace: 0})
	if err := swapped.SweepOnce(ctx); err != nil { // observes cutover
		t.Fatalf("SweepOnce after swap: %v", err)
	}
	if _, err := swapped.Release(ctx, camp.ID); err != ErrObjectsUncopied {
		t.Fatalf("Release with an uncopied object = %v, want ErrObjectsUncopied", err)
	}
	if ok, _ := src.Exists(ctx, "web-videos/a.mp4"); !ok {
		t.Error("a refused release still deleted from the source")
	}
	if ok, _ := src.Exists(ctx, "thumbnails/a.jpg"); !ok {
		t.Error("a refused release still deleted from the source")
	}
}

// SC2: the failure categories, and the half that was invisible.
//
// objects_failed counts only rows whose whole five-attempt budget is spent, so a
// campaign every object of which is being refused reports 0 failed and an empty
// last_error for as long as the backoff ladder takes — the operator watches
// progress stall and is told nothing. `retrying` is that half, and it is the
// number the surface needed.
func TestFailureCategoriesSurfaceRetryingObjectsBeforeTheyDeadLetter(t *testing.T) {
	ctx := context.Background()
	src := localAt(t, "source")
	// A target that refuses every write is what a narrowed credential looks
	// like from inside the copy loop.
	repo := newFakeRepo()
	svc := NewService(repo, src, refusingBackend{localAt(t, "target")}, Config{})

	put(t, src, "web-videos/a.mp4", "bytes a")
	put(t, src, "thumbnails/a.jpg", "bytes b")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if _, err := svc.CopyOnce(ctx, 8); err != nil {
		t.Fatalf("CopyOnce: %v", err)
	}

	got, _, failures, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The finding, restated as an assertion: the campaign-level counter is
	// still zero and the categories are not.
	if got.ObjectsFailed != 0 {
		t.Fatalf("objects_failed = %d; this test needs the pre-dead-letter window", got.ObjectsFailed)
	}
	if len(failures) == 0 {
		t.Fatal("a campaign whose every object is being refused reports no failure categories at all — this is the finding")
	}
	var retrying int64
	for _, f := range failures {
		if f.Category == "" {
			t.Error("a failure category is empty")
		}
		retrying += f.Retrying
		if f.Terminal != 0 {
			t.Errorf("category %q reports %d terminal before any budget was spent", f.Category, f.Terminal)
		}
	}
	if retrying != 2 {
		t.Errorf("retrying = %d across all categories, want 2", retrying)
	}
}

// refusingBackend answers every write with a classified refusal, exactly as a
// narrowed bucket credential does.
type refusingBackend struct{ storage.Backend }

func (r refusingBackend) Put(context.Context, string, io.Reader) (int64, error) {
	return 0, &storage.Error{Class: storage.ClassWriteDenied, Op: "put"}
}

// Describe is forwarded because it is an OPTIONAL capability: embedding a
// storage.Backend promotes only that interface's methods, and a target whose
// identity cannot be established is refused before a campaign starts.
func (r refusingBackend) Describe() string { return storage.Describe(r.Backend) }
