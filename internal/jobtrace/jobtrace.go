// Package jobtrace carries a job's IDENTITY — the request that caused it and
// the process running it — into the operational projection, and emits the one
// log line a failed job owes its operator.
//
// THE FAILURE MODE IT CLOSES. job_runs and job_events have carried request_id,
// correlation_id, trace_id, worker_id, heartbeat_at and lease_expires_at since
// migration 0083, and until now NOTHING wrote any of them: measured 0 of 9 runs
// and 0 of 53 events on a real instance, with audit_log.job_id populated on 0 of
// 56 rows. The admin run detail rendered em-dashes for every one, the jobs
// table's WORKER column was empty on every row, ?worker_id= matched nothing, and
// the dead-letter's own advice — "Execution failed; inspect correlated system
// logs for diagnostic detail" — could not be followed twice over: the run
// carried no id to grep for, and the worker emitted no line about the failure at
// all. Three real failures across three queues produced zero WARN or ERROR lines
// on the worker process.
//
// WHY IT COULD NOT BE DONE IN THE TRIGGERS. The projection is maintained by
// AFTER triggers reading the queue row. Neither the originating request nor the
// process that claimed the row is a COLUMN of that table, so the trigger cannot
// know either. This package carries both in from Go, keyed on the (queue,
// source_id) pair the projection already indexes uniquely — no queue table
// grows a column, and the trigger functions are untouched.
//
// WHY EACH STAMP ALSO BACKFILLS EVENTS. The projection's own event is written
// inside the trigger, so it exists before any Go statement can run: the
// 'enqueued' event by the time an enqueue returns, the 'started' event by the
// time a claim returns. Later events inherit through migration 0133's BEFORE
// INSERT trigger and need no help; those first two are backfilled by the same
// statement that stamps the run.
//
// EVERYTHING HERE IS BEST EFFORT. A job that ran must never fail because its
// bookkeeping could not be written: the projection is an operator's view, not
// the execution's source of truth. Every method swallows its error into a log
// line and returns.
package jobtrace

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Repository is the projection write side. *sqlcgen.Queries satisfies it.
type Repository interface {
	StampJobRunCorrelation(ctx context.Context, arg sqlcgen.StampJobRunCorrelationParams) (uuid.UUID, error)
	StampJobRunWorker(ctx context.Context, arg sqlcgen.StampJobRunWorkerParams) error
	TouchJobRunHeartbeat(ctx context.Context, arg sqlcgen.TouchJobRunHeartbeatParams) error
	GetJobRunIdentityBySource(ctx context.Context, arg sqlcgen.GetJobRunIdentityBySourceParams) (sqlcgen.GetJobRunIdentityBySourceRow, error)
}

// Recorder stamps runs and logs failures for ONE process.
type Recorder struct {
	repo     Repository
	workerID string
	logger   *slog.Logger
}

// New builds a recorder. A nil repo yields a nil recorder, and every method on a
// nil recorder is a no-op — so a service constructed without one (unit tests,
// embedders) behaves exactly as it did.
//
// workerID is the process's heartbeat key (internal/processheartbeat.ProcessID),
// deliberately the SAME string, so a run's WORKER column and the status page's
// process list name the same thing and one can be pasted into the other's filter.
func New(repo Repository, workerID string, logger *slog.Logger) *Recorder {
	if repo == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{repo: repo, workerID: workerID, logger: logger}
}

// WorkerID reports this recorder's process id.
func (r *Recorder) WorkerID() string {
	if r == nil {
		return ""
	}
	return r.workerID
}

// Enqueued stamps the run just created for sourceID with the ids of the request
// that caused it, and returns that run's id (uuid.Nil when it could not be
// resolved, which callers treat as "no link to record").
//
// ctx is the ORIGINATING request's context: the correlation middleware put the
// ids there, so an enqueue reached through three service layers still carries
// the header the browser sent. A job enqueued by a scheduler has no request
// behind it and stamps empty strings — an honest blank beats an invented id.
func (r *Recorder) Enqueued(ctx context.Context, queue, sourceID string, actor uuid.UUID) uuid.UUID {
	if r == nil || sourceID == "" {
		return uuid.Nil
	}
	ids := observability.CorrelationFromContext(ctx)
	runID, err := r.repo.StampJobRunCorrelation(ctx, sqlcgen.StampJobRunCorrelationParams{
		Queue:         queue,
		SourceID:      sourceID,
		RequestID:     ids.RequestID,
		CorrelationID: ids.CorrelationID,
		TraceID:       traceID(ctx),
		ActorID:       nullableUUID(actor),
		ParentJobID:   parentFromContext(ctx),
	})
	if err != nil {
		r.logger.WarnContext(ctx, "could not stamp the job run's correlation ids; the admin run detail will show none for this job",
			"queue", queue, "source_id", sourceID, "error", err.Error())
		return uuid.Nil
	}
	return runID
}

// Claimed records that THIS process now owns the run, with the lease deadline
// the queue itself enforces (the claim pushes next_attempt_at forward and the
// lease ticker renews it) rather than a second, drifting copy of it.
func (r *Recorder) Claimed(ctx context.Context, queue, sourceID string, leaseExpires time.Time) {
	if r == nil || sourceID == "" {
		return
	}
	if err := r.repo.StampJobRunWorker(ctx, sqlcgen.StampJobRunWorkerParams{
		Queue:          queue,
		SourceID:       sourceID,
		WorkerID:       r.workerID,
		LeaseExpiresAt: timestamptz(leaseExpires),
	}); err != nil {
		r.logger.WarnContext(ctx, "could not stamp the worker on a claimed job run; the admin jobs table will show no worker for it",
			"queue", queue, "source_id", sourceID, "worker_id", r.workerID, "error", err.Error())
	}
}

// Beat pushes a running job's heartbeat and lease forward. Called from the same
// ticker that renews the queue row's lease, so heartbeat_at answers exactly the
// question the run detail's Heartbeat field asks: when did a process last say it
// was alive on this run.
func (r *Recorder) Beat(ctx context.Context, queue, sourceID string, leaseExpires time.Time) {
	if r == nil || sourceID == "" {
		return
	}
	if err := r.repo.TouchJobRunHeartbeat(ctx, sqlcgen.TouchJobRunHeartbeatParams{
		Queue:          queue,
		SourceID:       sourceID,
		WorkerID:       r.workerID,
		LeaseExpiresAt: timestamptz(leaseExpires),
	}); err != nil {
		// DEBUG, not WARN: a missed heartbeat is cosmetic, the lease renewal
		// beside it is not, and a warn per job per lease interval would drown
		// the very log this package exists to make greppable.
		r.logger.DebugContext(ctx, "job run heartbeat not written",
			"queue", queue, "source_id", sourceID, "error", err.Error())
	}
}

// RunID resolves the projection's id for one queue row, or uuid.Nil.
func (r *Recorder) RunID(ctx context.Context, queue, sourceID string) uuid.UUID {
	return r.identity(ctx, queue, sourceID).ID
}

// RunContext is the context a worker should do a job's WORK under: it marks the
// run as the parent of anything the work enqueues (ContextWithParentJob) and it
// puts the ORIGINATING REQUEST's identifiers back on the context, read off the
// run row this worker is executing.
//
// The second half is why it exists. A worker's context is a background one and
// carries no request, so every row queued from inside a job recorded an empty
// request_id and correlation_id — the rehearsal measured it on the federation
// side, where the 24 deliveries queued by the transcode-completion hook were
// faithfully blank on BOTH the queue row and its projected run, and /admin/jobs
// showed a dash where the act that caused them should be. The identity is not
// missing, it is one row away: the enqueue stamped it on the parent run, and
// this reads it back so the whole chain — request → finalize → transcode →
// twelve deliveries — carries one correlation id.
//
// An unknown run (no projection row yet, or a queue this recorder does not
// know) leaves ctx exactly as it was: an invented id would be worse than a
// blank one.
func (r *Recorder) RunContext(ctx context.Context, queue, sourceID string) context.Context {
	row := r.identity(ctx, queue, sourceID)
	ctx = ContextWithParentJob(ctx, row.ID)
	if row.RequestID == "" && row.CorrelationID == "" {
		return ctx
	}
	// An identity already on the context wins: it is the live request, and this
	// row is a record of an older one.
	if ids := observability.CorrelationFromContext(ctx); ids.RequestID != "" || ids.CorrelationID != "" {
		return ctx
	}
	return observability.ContextWithCorrelation(ctx, observability.Correlation{
		RequestID: row.RequestID, CorrelationID: row.CorrelationID,
	})
}

func (r *Recorder) identity(ctx context.Context, queue, sourceID string) sqlcgen.GetJobRunIdentityBySourceRow {
	if r == nil || sourceID == "" {
		return sqlcgen.GetJobRunIdentityBySourceRow{}
	}
	row, err := r.repo.GetJobRunIdentityBySource(ctx, sqlcgen.GetJobRunIdentityBySourceParams{
		Queue: queue, SourceID: sourceID,
	})
	if err != nil {
		return sqlcgen.GetJobRunIdentityBySourceRow{}
	}
	return row
}

// Failure is one attempt's outcome, as the worker saw it.
type Failure struct {
	Queue    string
	SourceID string
	// Resource is the domain object the job is about (a video id), so the line
	// is actionable without a second lookup.
	Resource string
	Attempt  int
	// State is the projection's vocabulary: "retry_scheduled" or "dead_lettered".
	State string
	Err   error
}

const (
	// StateRetryScheduled and StateDeadLettered mirror the job_runs state
	// vocabulary so a log line filters with the same word the dashboard shows.
	StateRetryScheduled = "retry_scheduled"
	StateDeadLettered   = "dead_lettered"
)

// Failed writes the one structured line a failed job owes its operator.
//
// This is the whole point of the package on the worker side. Before it, three
// real failures across three queues produced no WARN or ERROR line at all, so
// the dead-letter's advice to "inspect correlated system logs" pointed at logs
// that contained nothing about the failure and shared no id with the run. The
// line carries every id needed to walk the chain in either direction — the
// originating request, the run, the process, the attempt — and the error
// REDACTED through the same helper the admin failure list uses, because a
// worker log is read by the same operator on the same screen and a raw cause
// can carry a signed URL or an address.
//
// Level follows consequence: a scheduled retry is a WARN (the work will happen),
// a dead-letter is an ERROR (it will not).
func (r *Recorder) Failed(ctx context.Context, f Failure) {
	if r == nil {
		return
	}
	// A worker's context is a background one and carries no request ids, so the
	// originating ids are read back OFF the run the enqueue stamped. That
	// read-back is what makes one grep walk the chain in both directions:
	// request → audit row → run → events → these lines.
	run := r.identity(ctx, f.Queue, f.SourceID)
	ids := observability.CorrelationFromContext(ctx)
	req, corr, trc := run.RequestID, run.CorrelationID, run.TraceID
	if req == "" {
		req = ids.RequestID
	}
	if corr == "" {
		corr = ids.CorrelationID
	}
	if trc == "" {
		trc = traceID(ctx)
	}
	attrs := []any{
		"queue", f.Queue,
		"job_id", f.SourceID,
		"run_id", nonZero(run.ID),
		"worker_id", r.workerID,
		"attempt", f.Attempt,
		"state", f.State,
		"request_id", req,
		"correlation_id", corr,
	}
	if f.Resource != "" {
		attrs = append(attrs, "resource_id", f.Resource)
	}
	if trc != "" {
		attrs = append(attrs, "trace_id", trc)
	}
	if f.Err != nil {
		attrs = append(attrs, "error", jobstatus.RedactDetail(f.Err.Error()))
	}
	if f.State == StateDeadLettered {
		r.logger.ErrorContext(ctx, "job dead-lettered; no further attempts will be made", attrs...)
		return
	}
	r.logger.WarnContext(ctx, "job attempt failed; a bounded retry was scheduled", attrs...)
}

func nonZero(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func traceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

func nullableUUID(id uuid.UUID) pgtype.UUID {
	return pgconv.UUIDPtr(optional(id))
}

func optional(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

type parentJobKey struct{}

// ContextWithParentJob marks ctx as running INSIDE a job, so anything that
// enqueue path creates is recorded as that job's child. It is how a chain like
// upload finalize → transcode becomes a parent/child pair on the run detail
// instead of two unrelated rows.
func ContextWithParentJob(ctx context.Context, runID uuid.UUID) context.Context {
	if runID == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, parentJobKey{}, runID)
}

func parentFromContext(ctx context.Context) pgtype.UUID {
	id, _ := ctx.Value(parentJobKey{}).(uuid.UUID)
	return nullableUUID(id)
}

type actorKey struct{}

// ContextWithActor records the authenticated principal so an enqueue three
// service layers below the HTTP handler attributes its run to them. Services
// take a context.Context, never an echo.Context, which is why every enqueue
// path in the codebase had a nil actor on its operational run until this
// existed.
func ContextWithActor(ctx context.Context, id uuid.UUID) context.Context {
	if id == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, actorKey{}, id)
}

// ActorFromContext returns the authenticated principal, or uuid.Nil.
func ActorFromContext(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(actorKey{}).(uuid.UUID)
	return id
}
