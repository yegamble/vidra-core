package jobtrace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeRepo struct {
	runs      map[string]sqlcgen.GetJobRunIdentityBySourceRow
	stamps    []sqlcgen.StampJobRunCorrelationParams
	workers   []sqlcgen.StampJobRunWorkerParams
	beats     []sqlcgen.TouchJobRunHeartbeatParams
	stampErr  error
	lookupErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{runs: map[string]sqlcgen.GetJobRunIdentityBySourceRow{}}
}

func key(queue, source string) string { return queue + "/" + source }

func (f *fakeRepo) StampJobRunCorrelation(_ context.Context, a sqlcgen.StampJobRunCorrelationParams) (uuid.UUID, error) {
	if f.stampErr != nil {
		return uuid.Nil, f.stampErr
	}
	f.stamps = append(f.stamps, a)
	row := f.runs[key(a.Queue, a.SourceID)]
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	row.RequestID, row.CorrelationID, row.TraceID = a.RequestID, a.CorrelationID, a.TraceID
	f.runs[key(a.Queue, a.SourceID)] = row
	return row.ID, nil
}

func (f *fakeRepo) StampJobRunWorker(_ context.Context, a sqlcgen.StampJobRunWorkerParams) error {
	f.workers = append(f.workers, a)
	return nil
}

func (f *fakeRepo) TouchJobRunHeartbeat(_ context.Context, a sqlcgen.TouchJobRunHeartbeatParams) error {
	f.beats = append(f.beats, a)
	return nil
}

func (f *fakeRepo) GetJobRunIdentityBySource(_ context.Context, a sqlcgen.GetJobRunIdentityBySourceParams) (sqlcgen.GetJobRunIdentityBySourceRow, error) {
	if f.lookupErr != nil {
		return sqlcgen.GetJobRunIdentityBySourceRow{}, f.lookupErr
	}
	return f.runs[key(a.Queue, a.SourceID)], nil
}

func capture() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func TestEnqueuedStampsTheOriginatingRequest(t *testing.T) {
	repo := newFakeRepo()
	logger, _ := capture()
	r := New(repo, "box:1", logger)

	actor := uuid.New()
	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{
		RequestID: "req-abc", CorrelationID: "corr-xyz",
	})
	ctx = ContextWithActor(ctx, actor)

	runID := r.Enqueued(ctx, "transcode_jobs", "job-1", ActorFromContext(ctx))
	if runID == uuid.Nil {
		t.Fatal("Enqueued returned no run id, so the audit row cannot link job_id forward")
	}
	if len(repo.stamps) != 1 {
		t.Fatalf("stamps = %d, want 1", len(repo.stamps))
	}
	st := repo.stamps[0]
	if st.Queue != "transcode_jobs" || st.SourceID != "job-1" {
		t.Errorf("stamp keyed on %s/%s", st.Queue, st.SourceID)
	}
	if st.RequestID != "req-abc" || st.CorrelationID != "corr-xyz" {
		t.Errorf("stamp ids = %q/%q, want the request's", st.RequestID, st.CorrelationID)
	}
	if !st.ActorID.Valid || uuid.UUID(st.ActorID.Bytes) != actor {
		t.Errorf("actor = %+v, want %s", st.ActorID, actor)
	}
}

func TestEnqueuedByASchedulerStampsHonestBlanks(t *testing.T) {
	repo := newFakeRepo()
	logger, _ := capture()
	r := New(repo, "box:1", logger)
	// A background loop has no request behind it. An INVENTED id would be worse
	// than a blank: it would correlate a run to a request that never happened.
	r.Enqueued(context.Background(), "import_jobs", "job-2", uuid.Nil)
	st := repo.stamps[0]
	if st.RequestID != "" || st.CorrelationID != "" || st.ActorID.Valid {
		t.Errorf("scheduler enqueue stamped %+v; want blanks", st)
	}
}

func TestClaimedAndBeatCarryTheWorkerAndTheLease(t *testing.T) {
	repo := newFakeRepo()
	logger, _ := capture()
	r := New(repo, "box:7", logger)
	expires := time.Now().Add(30 * time.Minute)

	r.Claimed(context.Background(), "transcode_jobs", "job-1", expires)
	if len(repo.workers) != 1 {
		t.Fatalf("workers = %d, want 1", len(repo.workers))
	}
	w := repo.workers[0]
	if w.WorkerID != "box:7" {
		t.Errorf("worker_id = %q, want the process id", w.WorkerID)
	}
	if !w.LeaseExpiresAt.Valid || !w.LeaseExpiresAt.Time.Equal(expires) {
		t.Errorf("lease = %+v, want the queue's own deadline %v", w.LeaseExpiresAt, expires)
	}

	r.Beat(context.Background(), "transcode_jobs", "job-1", expires.Add(time.Minute))
	if len(repo.beats) != 1 || repo.beats[0].WorkerID != "box:7" {
		t.Fatalf("beats = %+v", repo.beats)
	}
}

// The line the dead-letter's own advice — "inspect correlated system logs for
// diagnostic detail" — sends the operator to read. Before this, three real
// worker failures across three queues wrote NO log line at all.
func TestFailedWritesOneCorrelatedLine(t *testing.T) {
	repo := newFakeRepo()
	runID := uuid.New()
	repo.runs[key("transcode_jobs", "job-1")] = sqlcgen.GetJobRunIdentityBySourceRow{
		ID: runID, RequestID: "req-abc", CorrelationID: "corr-xyz", TraceID: "0af7651916cd43dd8448eb211c80319c",
	}
	logger, buf := capture()
	r := New(repo, "box:7", logger)

	// A WORKER's context is a background one and carries no request ids: the
	// chain only closes because they are read back off the run the enqueue
	// stamped.
	r.Failed(context.Background(), Failure{
		Queue: "transcode_jobs", SourceID: "job-1", Resource: "vid-9",
		Attempt: 5, State: StateDeadLettered,
		Err: errors.New("import: fetch https://source.invalid/v.mp4?token=abc123 failed"),
	})
	got := lines(t, buf)
	if len(got) != 1 {
		t.Fatalf("wrote %d lines, want exactly one", len(got))
	}
	line := got[0]
	if line["level"] != "ERROR" {
		t.Errorf("level = %v; a dead-letter is work that will NOT happen", line["level"])
	}
	for k, want := range map[string]any{
		"job_id": "job-1", "run_id": runID.String(), "worker_id": "box:7",
		"request_id": "req-abc", "correlation_id": "corr-xyz",
		"state": "dead_lettered", "queue": "transcode_jobs", "resource_id": "vid-9",
		"attempt": float64(5),
	} {
		if line[k] != want {
			t.Errorf("%s = %v, want %v", k, line[k], want)
		}
	}
	// The cause is redacted with the SAME helper the admin failure list uses, or
	// the log becomes the leak the list was built to prevent. (Known and
	// recorded gap in that helper, unchanged here: it strips URL schemes and
	// credential-shaped pairs, NOT a bare storage key such as
	// `ffprobe "web-videos/<uuid>.mp4"`.)
	s, _ := line["error"].(string)
	if !strings.Contains(s, "[redacted]") {
		t.Errorf("error = %q; the signed source URL must be redacted", s)
	}
	if strings.Contains(s, "token=abc123") {
		t.Errorf("error = %q; a credential reached the worker log", s)
	}

	buf.Reset()
	r.Failed(context.Background(), Failure{
		Queue: "transcode_jobs", SourceID: "job-1", Attempt: 2,
		State: StateRetryScheduled, Err: errors.New("boom"),
	})
	got = lines(t, buf)
	if len(got) != 1 || got[0]["level"] != "WARN" {
		t.Fatalf("retry line = %+v; a scheduled retry is work that WILL happen", got)
	}
}

func TestFailedFallsBackToTheContextWhenTheRunIsUnknown(t *testing.T) {
	repo := newFakeRepo()
	repo.lookupErr = errors.New("gone")
	logger, buf := capture()
	r := New(repo, "box:7", logger)
	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{
		RequestID: "req-live", CorrelationID: "corr-live",
	})
	r.Failed(ctx, Failure{Queue: "import_jobs", SourceID: "j", Attempt: 1, State: StateRetryScheduled})
	line := lines(t, buf)[0]
	if line["request_id"] != "req-live" || line["correlation_id"] != "corr-live" {
		t.Errorf("line = %+v; an unresolvable run must not cost the line its ids", line)
	}
	if line["run_id"] != "" {
		t.Errorf("run_id = %v, want empty rather than a fabricated id", line["run_id"])
	}
}

func TestStampFailureNeverStopsTheJob(t *testing.T) {
	repo := newFakeRepo()
	repo.stampErr = errors.New("db down")
	logger, buf := capture()
	r := New(repo, "box:1", logger)
	if id := r.Enqueued(context.Background(), "transcode_jobs", "job-1", uuid.Nil); id != uuid.Nil {
		t.Errorf("Enqueued = %s, want uuid.Nil when the stamp failed", id)
	}
	if len(lines(t, buf)) != 1 {
		t.Error("a failed stamp must say so once; the projection is an operator's view, never the execution's truth")
	}
}

func TestNilRecorderIsInert(t *testing.T) {
	var r *Recorder
	if id := r.Enqueued(context.Background(), "q", "s", uuid.New()); id != uuid.Nil {
		t.Error("nil Enqueued")
	}
	r.Claimed(context.Background(), "q", "s", time.Now())
	r.Beat(context.Background(), "q", "s", time.Now())
	r.Failed(context.Background(), Failure{Queue: "q", SourceID: "s"})
	if r.WorkerID() != "" || r.RunID(context.Background(), "q", "s") != uuid.Nil {
		t.Error("nil accessors")
	}
	if New(nil, "x", nil) != nil {
		t.Error("New with no repo must be nil so an unwired service stays unwired")
	}
}

func TestParentJobContextChainsARunToItsCause(t *testing.T) {
	repo := newFakeRepo()
	logger, _ := capture()
	r := New(repo, "box:1", logger)
	parent := uuid.New()
	ctx := ContextWithParentJob(context.Background(), parent)
	r.Enqueued(ctx, "transcode_jobs", "child-1", uuid.Nil)
	st := repo.stamps[0]
	if !st.ParentJobID.Valid || uuid.UUID(st.ParentJobID.Bytes) != parent {
		t.Errorf("parent = %+v, want %s", st.ParentJobID, parent)
	}
	// And a plain context leaves it null rather than inventing a parent.
	repo.stamps = nil
	r.Enqueued(context.Background(), "transcode_jobs", "orphan-1", uuid.Nil)
	if repo.stamps[0].ParentJobID.Valid {
		t.Error("an unparented enqueue must leave parent_job_id NULL")
	}
}

// TestRunContextPutsTheOriginatingRequestBackOnTheContext is the rehearsal's
// finding (d). A worker's context is a background one and carries no request, so
// every row queued from inside a job recorded an empty request_id and
// correlation_id — the 24 federation deliveries the transcode-completion hook
// queued were faithfully blank on BOTH the queue row and its projected run, and
// /admin/jobs showed a dash where the act that caused them should be. The
// identity was never missing, only one row away.
func TestRunContextPutsTheOriginatingRequestBackOnTheContext(t *testing.T) {
	repo := newFakeRepo()
	runID := uuid.New()
	repo.runs[key("transcode_jobs", "job-1")] = sqlcgen.GetJobRunIdentityBySourceRow{
		ID: runID, RequestID: "req-abc", CorrelationID: "corr-abc",
	}
	logger, _ := capture()
	r := New(repo, "box:7", logger)

	ctx := r.RunContext(context.Background(), "transcode_jobs", "job-1")
	ids := observability.CorrelationFromContext(ctx)
	if ids.RequestID != "req-abc" || ids.CorrelationID != "corr-abc" {
		t.Fatalf("correlation = %+v, want the run's originating ids", ids)
	}
	// And the run is still marked as the parent, so the chain stays a chain.
	if got := parentFromContext(ctx); !got.Valid || uuid.UUID(got.Bytes) != runID {
		t.Errorf("parent job = %+v, want %v", got, runID)
	}
}

// A LIVE request wins over the row: the row is a record of an older request, and
// overwriting the one actually in flight would misattribute everything it does.
func TestRunContextDoesNotOverwriteALiveRequest(t *testing.T) {
	repo := newFakeRepo()
	repo.runs[key("transcode_jobs", "job-1")] = sqlcgen.GetJobRunIdentityBySourceRow{
		ID: uuid.New(), RequestID: "req-old", CorrelationID: "corr-old",
	}
	logger, _ := capture()
	r := New(repo, "box:7", logger)

	live := observability.ContextWithCorrelation(context.Background(),
		observability.Correlation{RequestID: "req-live", CorrelationID: "corr-live"})
	ids := observability.CorrelationFromContext(r.RunContext(live, "transcode_jobs", "job-1"))
	if ids.RequestID != "req-live" {
		t.Fatalf("request id = %q, want the live request", ids.RequestID)
	}
}

// An unknown run invents nothing: a blank id is honest, and a made-up one would
// send an operator to logs that do not exist.
func TestRunContextOnAnUnknownRunLeavesTheContextAlone(t *testing.T) {
	repo := newFakeRepo()
	logger, _ := capture()
	r := New(repo, "box:7", logger)

	ids := observability.CorrelationFromContext(r.RunContext(context.Background(), "transcode_jobs", "nope"))
	if ids.RequestID != "" || ids.CorrelationID != "" {
		t.Fatalf("correlation = %+v, want empty", ids)
	}
}
