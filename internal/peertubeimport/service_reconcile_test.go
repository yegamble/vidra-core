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

	svc.finishRun(context.Background(), sqlcgen.ClaimDueImportRunsRow{ID: uuid.New(), Mode: "run"}, "", "",
		NewReport(false, PolicySkip, false))

	if len(repo.completed) != 1 || len(repo.failed) != 0 {
		t.Fatalf("a failed reconcile changed the run's terminal state: completed=%d failed=%d",
			len(repo.completed), len(repo.failed))
	}
}
