//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestServiceRunLifecycle exercises the durable import_runs path end to end: a
// launch, the single-active guard, the worker executing a dry-run, and the
// persisted progress report — the admin-API contract behind the vidra-user
// "Import from PeerTube" UI.
func TestServiceRunLifecycle(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	srcMediaDir := t.TempDir()
	seedSourceMedia(t, srcMediaDir)
	srcMedia, err := storage.NewLocal(srcMediaDir)
	if err != nil {
		t.Fatal(err)
	}
	destMedia, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// The factory records the source-authoritative bit the worker hands it. That
	// bit travels admin request → run row → claim → importer, and the hop that has
	// no test anywhere else is the DURABLE one: a run launched by an admin is
	// executed later, possibly by another process, so a column that did not carry
	// it would silently downgrade every source-authoritative run to a gap-fill.
	var factoryAuth []bool
	factory := func(_ context.Context, _ ConflictPolicy, sourceAuthoritative bool) (*Importer, func(), error) {
		factoryAuth = append(factoryAuth, sourceAuthoritative)
		imp := NewImporter(dest, NewSourceFromPool(src), Options{
			SrcMedia: srcMedia, DestMedia: destMedia, SourceAuthoritative: sourceAuthoritative,
		})
		return imp, func() {}, nil
	}
	svc := NewService(sqlcgen.New(dest), WithImporterFactory(factory))

	if !svc.Configured() {
		t.Fatal("service should be configured when a factory is wired")
	}

	// Launch a dry-run (started_by NULL — no admin user exists in this scratch DB).
	run, err := svc.CreateRun(ctx, "dry_run", PolicySkip, false, uuid.Nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.State != "pending" {
		t.Fatalf("new run state = %q, want pending", run.State)
	}
	if run.SourceAuthoritative {
		t.Fatal("a run launched without asking for it must not be source-authoritative")
	}

	// A second launch while one is active must be rejected (single-active guard).
	if _, err := svc.CreateRun(ctx, "run", PolicySkip, false, uuid.Nil); err != ErrBusy {
		t.Fatalf("second launch err = %v, want ErrBusy", err)
	}

	// The worker executes the due run.
	n, err := svc.DrainDueRuns(ctx, 5)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d runs, want 1", n)
	}

	got, err := svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.State != "done" {
		t.Fatalf("run state = %q, want done", got.State)
	}
	if got.SourceVersion == nil || *got.SourceVersion != 800 {
		t.Errorf("run source_version = %v, want 800", got.SourceVersion)
	}
	if got.Report == nil {
		t.Fatal("done run must carry a progress report")
	}
	if got.Report.Entities[KindUser].Planned != 2 {
		t.Errorf("report users planned = %d, want 2", got.Report.Entities[KindUser].Planned)
	}
	// A dry-run writes nothing to the destination content tables.
	if got.Report.DryRun != true {
		t.Error("dry-run report must have dry_run=true")
	}
	if uc := countRows(t, ctx, dest, "users"); uc != 0 {
		t.Errorf("dry-run wrote %d users, want 0", uc)
	}

	if got.Report.SourceAuthoritative {
		t.Error("the report must record the mode the run actually used")
	}
	if len(factoryAuth) != 1 || factoryAuth[0] {
		t.Errorf("factory saw source_authoritative=%v, want exactly one false", factoryAuth)
	}

	// With no active run, a fresh launch is accepted again — this time asking for
	// the source to win, which has to survive the round trip through the run row.
	auth, err := svc.CreateRun(ctx, "dry_run", PolicySkip, true, uuid.Nil)
	if err != nil {
		t.Fatalf("relaunch after completion: %v", err)
	}
	if !auth.SourceAuthoritative {
		t.Fatal("the launched run did not record that the source is authoritative")
	}
	if _, err := svc.DrainDueRuns(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(factoryAuth) != 2 || !factoryAuth[1] {
		t.Fatalf("factory saw source_authoritative=%v, want the second run to be true — the claim did not carry the column", factoryAuth)
	}
	done, err := svc.GetRun(ctx, auth.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if done.Report == nil || !done.Report.SourceAuthoritative {
		t.Fatal("the persisted report must say the run was source-authoritative")
	}
}
