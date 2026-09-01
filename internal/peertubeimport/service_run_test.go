package peertubeimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRunRepo is an in-memory Repository. Only the calls executeRun makes are
// recorded; the rest satisfy the interface. It is enough here because the
// terminal bookkeeping of a run — which row write, which audit result — is
// decided entirely from the report, with no database behaviour involved.
type fakeRunRepo struct {
	mu        sync.Mutex
	claims    []sqlcgen.ClaimDueImportRunsRow
	renewals  int
	completed []uuid.UUID
	failed    []sqlcgen.FailImportRunParams
}

func (f *fakeRunRepo) CreateImportRun(context.Context, sqlcgen.CreateImportRunParams) (sqlcgen.PeertubeImportRun, error) {
	return sqlcgen.PeertubeImportRun{}, errors.New("not used")
}

func (f *fakeRunRepo) GetImportRun(context.Context, uuid.UUID) (sqlcgen.PeertubeImportRun, error) {
	return sqlcgen.PeertubeImportRun{}, errors.New("not used")
}

func (f *fakeRunRepo) GetLatestImportRun(context.Context) (sqlcgen.PeertubeImportRun, error) {
	return sqlcgen.PeertubeImportRun{}, errors.New("not used")
}

func (f *fakeRunRepo) ListImportRuns(context.Context, sqlcgen.ListImportRunsParams) ([]sqlcgen.PeertubeImportRun, error) {
	return nil, nil
}

func (f *fakeRunRepo) ClaimDueImportRuns(context.Context, int32) ([]sqlcgen.ClaimDueImportRunsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.claims
	f.claims = nil
	return out, nil
}

func (f *fakeRunRepo) RenewImportRunLease(context.Context, uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewals++
	return nil
}

func (f *fakeRunRepo) SetImportRunVersion(context.Context, sqlcgen.SetImportRunVersionParams) error {
	return nil
}

func (f *fakeRunRepo) UpdateImportRunProgress(context.Context, sqlcgen.UpdateImportRunProgressParams) error {
	return nil
}

func (f *fakeRunRepo) CompleteImportRun(_ context.Context, arg sqlcgen.CompleteImportRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, arg.ID)
	return nil
}

func (f *fakeRunRepo) FailImportRun(_ context.Context, arg sqlcgen.FailImportRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, arg)
	return nil
}

func (f *fakeRunRepo) renewCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renewals
}

// runAuditEvents decodes the audit lines out of a captured JSON log — the same
// capture-buffer idiom the httpapi audit tests use, which the import's own
// audit events had no coverage of at all.
func runAuditEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		if rec["audit"] == true {
			events = append(events, rec)
		}
	}
	return events
}

// findRunAudit returns the first audit event matching action+result, or nil.
func findRunAudit(events []map[string]any, action, result string) map[string]any {
	for _, e := range events {
		if e["action"] == action && e["result"] == result {
			return e
		}
	}
	return nil
}

// TestFinishAuditReportsAFailedRunAsFailed is the whole point of the change.
// Importer.Run returns nil when every individual entity failed — failures are
// recorded per row and the loop continues — so branching on that error alone
// stamped "success" on a run that imported nothing. The run row is prunable and
// the audit log is not, so the audit line is the record that survives to be
// read, and it has to be the honest one.
func TestFinishAuditReportsAFailedRunAsFailed(t *testing.T) {
	var buf bytes.Buffer
	repo := &fakeRunRepo{}
	svc := NewService(repo, WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))

	report := NewReport(false, PolicySkip, false)
	report.count(KindVideo).Failed = 13528
	id := uuid.New()

	svc.finishRun(context.Background(), id, "", "", report)

	// The run genuinely finished, so the ROW is still completed rather than
	// failed. There is no fifth run state and 0067's CHECK would not admit one.
	if len(repo.completed) != 1 || repo.completed[0] != id {
		t.Fatalf("CompleteImportRun calls = %v, want exactly [%v]", repo.completed, id)
	}
	if len(repo.failed) != 0 {
		t.Fatalf("a finished run must not be marked failed on the row: %v", repo.failed)
	}

	events := runAuditEvents(t, &buf)
	if findRunAudit(events, observability.ActionPeerTubeImportFinish, observability.ResultSuccess) != nil {
		t.Fatal("a run in which 13528 rows failed emitted a SUCCESS finish audit — " +
			"six months later that is the only surviving record of the migration")
	}
	ev := findRunAudit(events, observability.ActionPeerTubeImportFinish, observability.ResultFailure)
	if ev == nil {
		t.Fatal("no failure finish audit was emitted for a run whose report carries failures")
	}
	reason, _ := ev["reason"].(string)
	if !strings.Contains(reason, "13528") {
		t.Errorf("finish audit reason %q does not carry the failed tally", reason)
	}
}

// TestFinishAuditReportsACleanRunAsSuccess is the counter-test: a failure branch
// broad enough to mark every run failed is its own bug, and would make the
// signal above worthless within a week.
func TestFinishAuditReportsACleanRunAsSuccess(t *testing.T) {
	var buf bytes.Buffer
	repo := &fakeRunRepo{}
	svc := NewService(repo, WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))

	report := NewReport(false, PolicySkip, false)
	report.count(KindVideo).Imported = 13528
	// Skipped and unsupported are not failures: an already-imported row and a
	// family this version defers are both expected outcomes of a healthy run.
	report.count(KindVideo).Skipped = 12
	report.count(KindRendition).Unsupported = 40

	svc.finishRun(context.Background(), uuid.New(), "", "", report)

	events := runAuditEvents(t, &buf)
	if findRunAudit(events, observability.ActionPeerTubeImportFinish, observability.ResultFailure) != nil {
		t.Fatal("a clean run emitted a FAILURE finish audit")
	}
	if findRunAudit(events, observability.ActionPeerTubeImportFinish, observability.ResultSuccess) == nil {
		t.Fatal("a clean run did not emit a success finish audit")
	}
}

// TestRunLeaseIsRenewedWhileTheRunExecutes pins the renewal wiring. The claim
// leases the run for 30 minutes and jobrecovery sweeps every 2, so a migration
// that runs for hours had its row requeued out from under it: the run read
// 'pending' with started_at NULL while it was actively writing, and on a second
// instance the requeued row could be claimed and executed CONCURRENTLY — which
// the view-count pass does not survive, since it reads the applied total outside
// its transaction and then applies a delta.
//
// The slow step here is the importer factory, which is the first thing
// executeRun calls and is inside the lease along with everything after it.
func TestRunLeaseIsRenewedWhileTheRunExecutes(t *testing.T) {
	repo := &fakeRunRepo{claims: []sqlcgen.ClaimDueImportRunsRow{{ID: uuid.New(), Mode: "run", ConflictPolicy: "skip"}}}
	var buf bytes.Buffer
	svc := NewService(repo,
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithImporterFactory(func(ctx context.Context, _ RunParams) (*Importer, func(), error) {
			// Stand in for a run that outlives several lease periods.
			select {
			case <-time.After(40 * time.Millisecond):
			case <-ctx.Done():
			}
			return nil, nil, errors.New("source unavailable")
		}))
	svc.leaseInterval = 5 * time.Millisecond

	n, err := svc.DrainDueRuns(context.Background(), 1)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d runs, want 1", n)
	}
	if got := repo.renewCount(); got < 2 {
		t.Fatalf("the lease was renewed %d times across a run spanning ~8 lease periods; "+
			"an unrenewed lease is swept back into the queue while the import is still writing", got)
	}
	// The terminal path still lands: renewal must not have displaced it.
	if len(repo.failed) != 1 {
		t.Fatalf("FailImportRun calls = %d, want 1", len(repo.failed))
	}
}
