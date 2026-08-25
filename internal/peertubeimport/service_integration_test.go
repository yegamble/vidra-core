//go:build integration

package peertubeimport

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/vidra/vidra-core/internal/observability"
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

	factory := func(_ context.Context, params RunParams) (*Importer, func(), error) {
		imp := NewImporter(dest, NewSourceFromPool(src), Options{
			Policy:                    params.Policy,
			AcknowledgedSchemaVersion: params.AcknowledgedSchemaVersion,
			SrcMedia:                  srcMedia, DestMedia: destMedia,
		})
		return imp, func() {}, nil
	}
	svc := NewService(sqlcgen.New(dest), WithImporterFactory(factory))

	if !svc.Configured() {
		t.Fatal("service should be configured when a factory is wired")
	}

	// Launch a dry-run (started_by NULL — no admin user exists in this scratch DB).
	run, err := svc.CreateRun(ctx, Launch{Mode: "dry_run", Policy: PolicySkip}, uuid.Nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.State != "pending" {
		t.Fatalf("new run state = %q, want pending", run.State)
	}
	// Nothing acknowledges an unverified schema unless the launch said so.
	if run.AcknowledgedSchemaVersion != nil {
		t.Fatalf("a launch that acknowledged nothing recorded %v", *run.AcknowledgedSchemaVersion)
	}

	// A second launch while one is active must be rejected (single-active guard).
	if _, err := svc.CreateRun(ctx, Launch{Mode: "run", Policy: PolicySkip}, uuid.Nil); err != ErrBusy {
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

	// With no active run, a fresh launch is accepted again.
	if _, err := svc.CreateRun(ctx, Launch{Mode: "dry_run", Policy: PolicySkip}, uuid.Nil); err != nil {
		t.Fatalf("relaunch after completion: %v", err)
	}
}

// TestServiceUnverifiedSchemaAcknowledgement is the whole feature end to end
// through the durable run path, against an UNVERIFIED source — the shape the live
// migration hit (its source reported 1040 against a ceiling of 1000) and could
// not get past in the admin UI at all.
//
// It proves three things in order, because all three have to hold: an
// un-acknowledged run is still refused; the refusal is machine-readable and
// carries the version it refused; and a run whose launch acknowledged THAT
// version proceeds — and is on the record as having done so.
func TestServiceUnverifiedSchemaAcknowledgement(t *testing.T) {
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

	// Move the seeded source past the verified ceiling, exactly as a live 8.x
	// instance does.
	const unverified = MaxSupportedSchemaVersion + 40
	mustExec(t, ctx, src, `UPDATE "application" SET "migrationVersion" = $1`, unverified)

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

	var sawAck int
	factory := func(_ context.Context, params RunParams) (*Importer, func(), error) {
		sawAck = params.AcknowledgedSchemaVersion
		imp := NewImporter(dest, NewSourceFromPool(src), Options{
			Policy:                    params.Policy,
			AcknowledgedSchemaVersion: params.AcknowledgedSchemaVersion,
			SrcMedia:                  srcMedia, DestMedia: destMedia,
		})
		return imp, func() {}, nil
	}
	var audits []observability.AuditEvent
	svc := NewService(sqlcgen.New(dest), WithImporterFactory(factory),
		WithAudit(func(_ context.Context, ev observability.AuditEvent) { audits = append(audits, ev) }))

	// 1. No acknowledgement → the hard stop stands. The admin path still cannot
	//    force, and this is the assertion that says so.
	refused, cerr := svc.CreateRun(ctx, Launch{Mode: "dry_run", Policy: PolicySkip}, uuid.Nil)
	if cerr != nil {
		t.Fatalf("create run: %v", cerr)
	}
	if _, err := svc.DrainDueRuns(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	got, err := svc.GetRun(ctx, refused.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("un-acknowledged run against schema %d state = %q, want failed", unverified, got.State)
	}
	if sawAck != 0 {
		t.Errorf("the worker built an importer with acknowledgement %d for a run that acknowledged nothing", sawAck)
	}

	// 2. The refusal is machine-readable and names the version, or the operator
	//    has nothing to acknowledge.
	if got.ErrorCode != CodeUnverifiedSchema {
		t.Errorf("failed run error_code = %q, want %q", got.ErrorCode, CodeUnverifiedSchema)
	}
	if got.SourceVersion == nil || *got.SourceVersion != unverified {
		t.Fatalf("failed run source_version = %v, want %d", got.SourceVersion, unverified)
	}

	// 3. An acknowledgement naming that exact version lets the same run through.
	ackRun, err := svc.CreateRun(ctx, Launch{Mode: "dry_run", Policy: PolicySkip, AcknowledgedSchemaVersion: *got.SourceVersion}, uuid.Nil)
	if err != nil {
		t.Fatalf("create acknowledged run: %v", err)
	}
	if _, err := svc.DrainDueRuns(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	done, err := svc.GetRun(ctx, ackRun.ID)
	if err != nil {
		t.Fatalf("get acknowledged run: %v", err)
	}
	if done.State != "done" {
		t.Fatalf("acknowledged run state = %q (error=%q code=%q), want done", done.State, done.Error, done.ErrorCode)
	}
	// The acknowledgement is on the record beside started_by, not merely acted on.
	if done.AcknowledgedSchemaVersion == nil || *done.AcknowledgedSchemaVersion != unverified {
		t.Errorf("run recorded acknowledgement %v, want %d", done.AcknowledgedSchemaVersion, unverified)
	}
	if sawAck != unverified {
		t.Errorf("the worker built an importer with acknowledgement %d, want %d", sawAck, unverified)
	}
	// The audit log holds it independently of the run row, which is prunable, and
	// says the gate was OPENED rather than merely that something was requested.
	overruled := false
	for _, ev := range audits {
		if strings.Contains(ev.Reason, fmt.Sprintf("acknowledged unverified schema version %d", unverified)) {
			overruled = true
		}
	}
	if !overruled {
		t.Errorf("no audit event named the unverified schema version the run proceeded on; got %+v", audits)
	}

	// And it does not carry: the NEXT launch has to say so again.
	next, err := svc.CreateRun(ctx, Launch{Mode: "dry_run", Policy: PolicySkip}, uuid.Nil)
	if err != nil {
		t.Fatalf("create follow-up run: %v", err)
	}
	if _, err := svc.DrainDueRuns(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	after, err := svc.GetRun(ctx, next.ID)
	if err != nil {
		t.Fatalf("get follow-up run: %v", err)
	}
	if after.State != "failed" || after.ErrorCode != CodeUnverifiedSchema {
		t.Errorf("follow-up run state=%q code=%q — an acknowledgement must not outlive the run it was made on",
			after.State, after.ErrorCode)
	}
}
