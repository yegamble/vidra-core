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

	// The factory records the source-authoritative bit the worker hands it. That
	// bit travels admin request → run row → claim → importer, and the hop that has
	// no test anywhere else is the DURABLE one: a run launched by an admin is
	// executed later, possibly by another process, so a column that did not carry
	// it would silently downgrade every source-authoritative run to a gap-fill.
	var factoryAuth []bool
	factory := func(_ context.Context, params RunParams) (*Importer, func(), error) {
		factoryAuth = append(factoryAuth, params.SourceAuthoritative)
		imp := NewImporter(dest, NewSourceFromPool(src), Options{
			Policy:                    params.Policy,
			AcknowledgedSchemaVersion: params.AcknowledgedSchemaVersion,
			SourceAuthoritative:       params.SourceAuthoritative,
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
	if run.SourceAuthoritative {
		t.Fatal("a run launched without asking for it must not be source-authoritative")
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

	if got.Report.SourceAuthoritative {
		t.Error("the report must record the mode the run actually used")
	}
	if len(factoryAuth) != 1 || factoryAuth[0] {
		t.Errorf("factory saw source_authoritative=%v, want exactly one false", factoryAuth)
	}

	// With no active run, a fresh launch is accepted again — this time asking for
	// the source to win, which has to survive the round trip through the run row.
	auth, err := svc.CreateRun(ctx, Launch{Mode: "dry_run", Policy: PolicySkip, SourceAuthoritative: true}, uuid.Nil)
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

// TestServiceRunCarriesMediaMode is the durable hop for the fourth axis. An admin
// launches through the API and a WORKER — possibly another process — claims the
// run and builds the importer, so a media mode that does not survive the run row
// is a media mode the operator cannot choose at all. Before this it came from
// PEERTUBE_IMPORT_MEDIA_MODE at process start, which meant changing it mid
// migration required editing the env file and restarting the API.
//
// Three things have to hold together: an omitted mode resolves to the SERVER
// default (not to the package default), a named mode overrides it for that run
// only, and the mode the run executed under is readable off the run afterwards —
// which is how "why is my bucket 8 TB?" and "why does nothing play?" stay
// answerable once the run is history.
func TestServiceRunCarriesMediaMode(t *testing.T) {
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

	var sawModes []MediaMode
	factory := func(_ context.Context, params RunParams) (*Importer, func(), error) {
		sawModes = append(sawModes, params.MediaMode)
		imp := NewImporter(dest, NewSourceFromPool(src), Options{
			Policy:    params.Policy,
			MediaMode: params.MediaMode,
			SrcMedia:  srcMedia, DestMedia: destMedia,
		})
		return imp, func() {}, nil
	}
	// The server default here is 'none', deliberately different from the package
	// default 'copy': an assertion that cannot tell the two apart proves nothing.
	svc := NewService(sqlcgen.New(dest), WithImporterFactory(factory), WithDefaultMediaMode(MediaModeNone))

	drain := func(in Launch) Run {
		t.Helper()
		launched, err := svc.CreateRun(ctx, in, uuid.Nil)
		if err != nil {
			t.Fatalf("create run %+v: %v", in, err)
		}
		if _, err := svc.DrainDueRuns(ctx, 5); err != nil {
			t.Fatalf("drain: %v", err)
		}
		out, err := svc.GetRun(ctx, launched.ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if out.State != "done" {
			t.Fatalf("run state = %q (error=%q), want done", out.State, out.Error)
		}
		return out
	}

	// 1. Omitted → the server default, resolved at LAUNCH so the row is the record.
	deflt := drain(Launch{Mode: "dry_run", Policy: PolicySkip})
	if deflt.MediaMode != string(MediaModeNone) {
		t.Errorf("run launched without a media mode recorded %q, want the server default %q", deflt.MediaMode, MediaModeNone)
	}

	// 2. Named → that mode, for this run only.
	named := drain(Launch{Mode: "dry_run", Policy: PolicySkip, MediaMode: MediaModeReference})
	if named.MediaMode != string(MediaModeReference) {
		t.Errorf("run launched with media_mode=reference recorded %q", named.MediaMode)
	}

	// 3. Both reached the worker across the claim — the hop nothing else covers.
	want := []MediaMode{MediaModeNone, MediaModeReference}
	if len(sawModes) != len(want) {
		t.Fatalf("factory saw %v, want %v", sawModes, want)
	}
	for i := range want {
		if sawModes[i] != want[i] {
			t.Fatalf("factory saw %v, want %v — the claim did not carry the column", sawModes, want)
		}
	}
}
