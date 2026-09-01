package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The importer writes videos with direct SQL inside its own transactions and
// emits no index event, so the only path into vidra-search is the reconcile
// sweep — once at process start and thereafter on a leader-elected ticker whose
// interval defaults to 24 HOURS. The documented migration flow is an admin-UI
// import on a running stack right up to cutover, explicitly so there is no
// restart, which left the entire migrated catalogue unsearchable for up to a day
// with restarting the core container as the only remedy.

// TestCompletedRunReconcilesTheSearchIndex is that fix: a real run that reaches
// its terminal bookkeeping sweeps the freshly written catalogue into the index.
func TestCompletedRunReconcilesTheSearchIndex(t *testing.T) {
	var buf bytes.Buffer
	repo := &fakeRunRepo{}
	sweeps := 0
	svc := NewService(repo,
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithSearchReconcile(func(context.Context) error { sweeps++; return nil }))

	report := NewReport(false, PolicySkip, false)
	report.count(KindVideo).Imported = 13528
	svc.finishRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "run"}, "", "", report)

	if sweeps != 1 {
		t.Fatalf("reconcile sweeps after a completed run = %d, want exactly 1; "+
			"without one the imported catalogue is unsearchable until the 24h ticker fires", sweeps)
	}
	if len(repo.completed) != 1 {
		t.Errorf("CompleteImportRun calls = %d, want 1", len(repo.completed))
	}
}

// TestDryRunDoesNotReconcileTheSearchIndex: a dry run planned and wrote nothing,
// so there is nothing new to index and no reason to page the whole catalogue
// through the outbox.
func TestDryRunDoesNotReconcileTheSearchIndex(t *testing.T) {
	var buf bytes.Buffer
	sweeps := 0
	svc := NewService(&fakeRunRepo{},
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithSearchReconcile(func(context.Context) error { sweeps++; return nil }))

	svc.finishRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "dry_run"}, "", "",
		NewReport(true, PolicySkip, false))

	if sweeps != 0 {
		t.Fatalf("a dry run triggered %d reconcile sweeps, want 0", sweeps)
	}
}

// TestReconcileFailureDoesNotUnfinishTheRun keeps the sweep a side effect. It is
// best-effort by the same rule every emission in this codebase follows: the run
// really did finish, and a failed index refresh must not rewrite that record.
func TestReconcileFailureDoesNotUnfinishTheRun(t *testing.T) {
	var buf bytes.Buffer
	repo := &fakeRunRepo{}
	svc := NewService(repo,
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithSearchReconcile(func(context.Context) error { return errors.New("outbox unavailable") }))

	// The report has to record an import or the sweep is never attempted at all
	// and this test proves nothing: the trigger is "did this run write anything".
	report := NewReport(false, PolicySkip, false)
	report.count(KindVideo).Imported = 1
	svc.finishRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "run"}, "", "", report)

	if len(repo.completed) != 1 || len(repo.failed) != 0 {
		t.Fatalf("a failed reconcile changed the run's terminal state: completed=%d failed=%d",
			len(repo.completed), len(repo.failed))
	}
}

// TestPartiallyFailedRunStillReconcilesTheSearchIndex closes the gap #143 left
// open on purpose. A run that dies partway — the source connection dropped in the
// middle of the video pass, a context deadline, an abort on --conflict-policy
// fail — has ALREADY WRITTEN everything it imported before it failed. Those rows
// are in the catalogue and are not in the index, and the trigger sat on the
// success path, so nothing swept them: the operator's remedy was the 24h ticker
// or a container restart, which is the very thing #143 existed to remove.
//
// The condition that matters is "did this run write anything", not "did this run
// finish cleanly" — a failed run's report is a real tally of real rows.
func TestPartiallyFailedRunStillReconcilesTheSearchIndex(t *testing.T) {
	var buf bytes.Buffer
	repo := &fakeRunRepo{}
	sweeps := 0
	svc := NewService(repo,
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithSearchReconcile(func(context.Context) error { sweeps++; return nil }))

	// What Importer.Run hands back alongside an error: the counts it accumulated
	// before the step that failed.
	partial := NewReport(false, PolicySkip, false)
	partial.count(KindVideo).Imported = 4113
	svc.abandonRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "run"},
		"", "", partial, errors.New("source connection lost"))

	if sweeps != 1 {
		t.Fatalf("reconcile sweeps after a partially-failed run = %d, want exactly 1; "+
			"the 4113 videos it did import stay unsearchable until the 24h ticker fires", sweeps)
	}
	if len(repo.failed) != 1 {
		t.Errorf("FailImportRun calls = %d, want 1 — the run still failed", len(repo.failed))
	}
	if len(repo.completed) != 0 {
		t.Errorf("a failed run was also completed %d times, want 0", len(repo.completed))
	}
}

// A run that failed before writing ANYTHING has nothing to sweep. Paging the
// whole catalogue through the outbox after every refused preflight would make
// the index refresh a side effect of failure, which is neither useful nor free.
func TestFailedRunThatWroteNothingDoesNotReconcile(t *testing.T) {
	var buf bytes.Buffer
	sweeps := 0
	svc := NewService(&fakeRunRepo{},
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithSearchReconcile(func(context.Context) error { sweeps++; return nil }))

	svc.abandonRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "run"},
		"", "", NewReport(false, PolicySkip, false), errors.New("could not read the source"))

	if sweeps != 0 {
		t.Fatalf("a run that imported nothing triggered %d reconcile sweeps, want 0", sweeps)
	}
}

// A DRY run that failed partway still wrote nothing: the mode, not the outcome,
// is what makes that true.
func TestFailedDryRunDoesNotReconcile(t *testing.T) {
	var buf bytes.Buffer
	sweeps := 0
	svc := NewService(&fakeRunRepo{},
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithSearchReconcile(func(context.Context) error { sweeps++; return nil }))

	plan := NewReport(true, PolicySkip, false)
	plan.count(KindVideo).Planned = 4113
	svc.abandonRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "dry_run"},
		"", "", plan, errors.New("source connection lost"))

	if sweeps != 0 {
		t.Fatalf("a failed dry run triggered %d reconcile sweeps, want 0", sweeps)
	}
}
