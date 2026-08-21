// Package jobstatus aggregates the state of every durable background-work queue
// into one operator-facing snapshot (fix_plan P17.4): per-queue depth counts by
// state plus the oldest pending item's age (a stuck-worker signal), and a merged
// recent-failures list. It is the backend contract behind the admin jobs page.
//
// The legacy snapshot is derived entirely from the queue tables. The additive
// job_runs model below carries explicit worker/lease/heartbeat fields; those are
// intentionally not inferred by this compatibility overview.
// Failures carry only id/error/attempts — never the inbox URL, source URL,
// storage key, or any argument (those live in columns this package never selects).
package jobstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const (
	// perQueueFailureFetch is how many recent failures to pull from each queue
	// before merging; maxRecentFailures caps the merged, newest-first list.
	perQueueFailureFetch = 10
	maxRecentFailures    = 20
)

// Queue names (stable identifiers the frontend keys on).
const (
	QueueTranscode      = "transcode_jobs"
	QueueFederation     = "federation_deliveries"
	QueueImport         = "import_jobs"
	QueueCaption        = "caption_jobs"
	QueueAccountExport  = "account_exports"
	QueueUploadSessions = "upload_sessions"
	// QueueStorageMigration is the storage-migration queue. Its DEPTH counts
	// OBJECTS rather than campaigns — a campaign is one row that would sit at
	// 'running' for a week, while the objects under it are the work whose depth
	// and staleness an operator can act on — but it keeps the campaign's queue
	// name so the depth gauge, the job_runs projection and the admin jobs page
	// all say "storage_migrations" and mean the same feature.
	QueueStorageMigration = "storage_migrations"
)

// QueueStatus is one queue's normalised depth snapshot. For upload_sessions —
// which has no retry/running model — pending=active, done=completed,
// failed=cancelled, running=0.
type QueueStatus struct {
	Queue                   string `json:"queue"`
	Pending                 int64  `json:"pending"`
	Running                 int64  `json:"running"`
	Done                    int64  `json:"done"`
	Failed                  int64  `json:"failed"`
	OldestPendingAgeSeconds int64  `json:"oldest_pending_age_seconds"`
}

// Failure is one dead-lettered job, safe to show an operator: no secrets, no URLs.
type Failure struct {
	Queue    string    `json:"queue"`
	ID       uuid.UUID `json:"id"`
	Error    string    `json:"error"`
	Attempts int32     `json:"attempts"`
	FailedAt time.Time `json:"failed_at"`
}

// Overview is the whole admin-jobs snapshot.
type Overview struct {
	Queues         []QueueStatus `json:"queues"`
	RecentFailures []Failure     `json:"recent_failures"`
}

// Querier is the subset of the sqlc query set this package needs. *sqlcgen.Queries
// satisfies it; tests supply a fake.
type Querier interface {
	TranscodeJobStats(ctx context.Context) (sqlcgen.TranscodeJobStatsRow, error)
	FederationDeliveryStats(ctx context.Context) (sqlcgen.FederationDeliveryStatsRow, error)
	ImportJobStats(ctx context.Context) (sqlcgen.ImportJobStatsRow, error)
	CaptionJobStats(ctx context.Context) (sqlcgen.CaptionJobStatsRow, error)
	AccountExportStats(ctx context.Context) (sqlcgen.AccountExportStatsRow, error)
	UploadSessionStats(ctx context.Context) (sqlcgen.UploadSessionStatsRow, error)
	StorageMigrationStats(ctx context.Context) (sqlcgen.StorageMigrationStatsRow, error)
	TranscodeRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.TranscodeRecentFailuresRow, error)
	FederationRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.FederationRecentFailuresRow, error)
	ImportRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.ImportRecentFailuresRow, error)
	CaptionRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.CaptionRecentFailuresRow, error)
	AccountExportRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.AccountExportRecentFailuresRow, error)
	StorageMigrationRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.StorageMigrationRecentFailuresRow, error)
}

// Service produces the durable-queue overview.
type Service struct {
	q  Querier
	op operationalQuerier
}

// NewService builds the service over a query set (typically *sqlcgen.Queries).
func NewService(q Querier) *Service {
	s := &Service{q: q}
	if op, ok := q.(operationalQuerier); ok {
		s.op = op
	}
	return s
}

// Overview reads every queue's depth snapshot and a merged recent-failures list.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	var ov Overview

	tj, err := s.q.TranscodeJobStats(ctx)
	if err != nil {
		return ov, err
	}
	fd, err := s.q.FederationDeliveryStats(ctx)
	if err != nil {
		return ov, err
	}
	ij, err := s.q.ImportJobStats(ctx)
	if err != nil {
		return ov, err
	}
	cj, err := s.q.CaptionJobStats(ctx)
	if err != nil {
		return ov, err
	}
	ae, err := s.q.AccountExportStats(ctx)
	if err != nil {
		return ov, err
	}
	us, err := s.q.UploadSessionStats(ctx)
	if err != nil {
		return ov, err
	}
	sm, err := s.q.StorageMigrationStats(ctx)
	if err != nil {
		return ov, err
	}
	ov.Queues = []QueueStatus{
		{QueueTranscode, tj.Pending, tj.Running, tj.Done, tj.Failed, tj.OldestPendingAgeSeconds},
		{QueueFederation, fd.Pending, fd.Running, fd.Done, fd.Failed, fd.OldestPendingAgeSeconds},
		{QueueImport, ij.Pending, ij.Running, ij.Done, ij.Failed, ij.OldestPendingAgeSeconds},
		{QueueCaption, cj.Pending, cj.Running, cj.Done, cj.Failed, cj.OldestPendingAgeSeconds},
		{QueueAccountExport, ae.Pending, ae.Running, ae.Done, ae.Failed, ae.OldestPendingAgeSeconds},
		{QueueUploadSessions, us.Pending, us.Running, us.Done, us.Failed, us.OldestPendingAgeSeconds},
		{QueueStorageMigration, sm.Pending, sm.Running, sm.Done, sm.Failed, sm.OldestPendingAgeSeconds},
	}

	failures := make([]Failure, 0, maxRecentFailures)
	if rows, err := s.q.TranscodeRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueTranscode, r.ID, redactDetail(r.Error), r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.FederationRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueFederation, r.ID, redactDetail(r.Error), r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.ImportRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueImport, r.ID, redactDetail(r.Error), r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.CaptionRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueCaption, r.ID, redactDetail(r.Error), r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.AccountExportRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueAccountExport, r.ID, redactDetail(r.Error), r.Attempts, r.UpdatedAt})
		}
	}

	// Storage migration failures report the CAMPAIGN id, never the object key:
	// the overview contract forbids storage keys, and several dead-lettered
	// objects from one campaign legitimately share an id here.
	if rows, err := s.q.StorageMigrationRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueStorageMigration, r.ID, redactDetail(r.Error), r.Attempts, r.UpdatedAt})
		}
	}

	sort.Slice(failures, func(i, j int) bool { return failures[i].FailedAt.After(failures[j].FailedAt) })
	if len(failures) > maxRecentFailures {
		failures = failures[:maxRecentFailures]
	}
	ov.RecentFailures = failures
	return ov, nil
}

// Depths flattens the per-queue overview into the four canonical states, for the
// Prometheus vidra_queue_depth gauge.
func (s *Service) Depths(ctx context.Context) ([]QueueDepth, error) {
	ov, err := s.Overview(ctx)
	if err != nil {
		return nil, err
	}
	depths := make([]QueueDepth, 0, len(ov.Queues)*4)
	for _, q := range ov.Queues {
		depths = append(depths,
			QueueDepth{q.Queue, "pending", q.Pending},
			QueueDepth{q.Queue, "running", q.Running},
			QueueDepth{q.Queue, "done", q.Done},
			QueueDepth{q.Queue, "failed", q.Failed},
		)
	}
	return depths, nil
}

// QueueDepth is one gauge sample: queue + state + count. It mirrors
// observability.QueueDepth without this package importing the OTel/metrics layer
// (cmd/api adapts one to the other).
type QueueDepth struct {
	Queue string
	State string
	Count int64
}

// Operational states are intentionally independent of the legacy queue
// vocabularies. The database projection maps each source state onto one of these
// stable API values.
const (
	StateQueued          = "queued"
	StateClaimed         = "claimed"
	StateRunning         = "running"
	StateRetryScheduled  = "retry_scheduled"
	StateSucceeded       = "succeeded"
	StateFailed          = "failed"
	StateDeadLettered    = "dead_lettered"
	StateCancelRequested = "cancel_requested"
	StateCancelled       = "cancelled"
)

var validRunStates = map[string]bool{
	StateQueued: true, StateClaimed: true, StateRunning: true,
	StateRetryScheduled: true, StateSucceeded: true, StateFailed: true,
	StateDeadLettered: true, StateCancelRequested: true, StateCancelled: true,
}

// ValidRunState reports whether state is part of the stable JobRunState API.
func ValidRunState(state string) bool { return validRunStates[state] }

// ErrOperationalUnavailable means the additive job-run schema/repository was not
// wired. Production always wires it; the distinction keeps older overview-only
// test fakes small and explicit.
var ErrOperationalUnavailable = errors.New("jobstatus: operational job repository unavailable")

// ErrRunNotFound is returned by Detail when the requested run does not exist.
var ErrRunNotFound = errors.New("jobstatus: run not found")

// Filter is shared by the REST list and SSE replay query. Empty strings mean no
// filter. Failure=true selects failed/dead-lettered runs; false excludes them;
// nil leaves both in the result.
type Filter struct {
	JobID         *uuid.UUID
	State         string
	Type          string
	Queue         string
	ResourceType  string
	ResourceID    string
	WorkerID      string
	Failure       *bool
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

// Run is the administrator-facing, secret-free execution projection. SourceID,
// lease tokens, and idempotency keys are deliberately not exposed.
type Run struct {
	ID              uuid.UUID       `json:"id"`
	PipelineRunID   string          `json:"pipeline_run_id,omitempty"`
	ParentJobID     string          `json:"parent_job_id,omitempty"`
	Type            string          `json:"type"`
	Queue           string          `json:"queue"`
	State           string          `json:"state"`
	Stage           string          `json:"stage,omitempty"`
	ProgressPercent *int16          `json:"progress_percent,omitempty"`
	Priority        int32           `json:"priority"`
	Attempt         int32           `json:"attempt"`
	MaxAttempts     *int32          `json:"max_attempts,omitempty"`
	ActorID         string          `json:"actor_id,omitempty"`
	ResourceType    string          `json:"resource_type,omitempty"`
	ResourceID      string          `json:"resource_id,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	WorkerID        string          `json:"worker_id,omitempty"`
	LeaseExpiresAt  *time.Time      `json:"lease_expires_at,omitempty"`
	HeartbeatAt     *time.Time      `json:"heartbeat_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	ClaimedAt       *time.Time      `json:"claimed_at,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	InputMetadata   json.RawMessage `json:"input_metadata"`
	OutputMetadata  json.RawMessage `json:"output_metadata"`
	ErrorClass      string          `json:"error_class,omitempty"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorDetail     string          `json:"error_detail,omitempty"`
	ErrorRetryable  *bool           `json:"error_retryable,omitempty"`
}

// Event is one durable run transition. Cursor is an opaque decimal string in
// JSON/SSE so JavaScript never rounds a BIGINT above Number.MAX_SAFE_INTEGER.
type Event struct {
	ID              uuid.UUID       `json:"id"`
	Cursor          string          `json:"cursor"`
	JobID           uuid.UUID       `json:"job_id"`
	PipelineRunID   string          `json:"pipeline_run_id,omitempty"`
	Kind            string          `json:"kind"`
	State           string          `json:"state"`
	Stage           string          `json:"stage,omitempty"`
	ProgressPercent *int16          `json:"progress_percent,omitempty"`
	Attempt         int32           `json:"attempt"`
	WorkerID        string          `json:"worker_id,omitempty"`
	Message         string          `json:"message,omitempty"`
	Metadata        json.RawMessage `json:"metadata"`
	RequestID       string          `json:"request_id,omitempty"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	TraceID         string          `json:"trace_id,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at"`
}

// Detail is a high-water-mark-consistent run/event page. The service reads the
// cursor before the snapshot, so an event committed during the REST query is
// either reflected in the snapshot or replayed after EventCursor (possibly both,
// which is harmless), never missed between REST and SSE.
type Detail struct {
	Run         Run
	Events      []Event
	EventsTotal int64
	EventCursor int64
}

// QueueHealth is a DB-derived gauge sample. It deliberately contains no
// monotonic retry/failure counters; trigger backfill cannot make those honest.
type QueueHealth struct {
	Queue                  string
	OldestQueuedAgeSeconds int64
	StaleRunning           int64
}

type operationalQuerier interface {
	MaxOperationalJobEventCursor(context.Context) (int64, error)
	MinOperationalJobEventCursor(context.Context) (int64, error)
	ListOperationalJobRuns(context.Context, sqlcgen.ListOperationalJobRunsParams) ([]sqlcgen.JobRun, error)
	CountOperationalJobRuns(context.Context, sqlcgen.CountOperationalJobRunsParams) (int64, error)
	GetOperationalJobRun(context.Context, uuid.UUID) (sqlcgen.JobRun, error)
	ListOperationalJobEvents(context.Context, sqlcgen.ListOperationalJobEventsParams) ([]sqlcgen.JobEvent, error)
	CountOperationalJobEvents(context.Context, sqlcgen.CountOperationalJobEventsParams) (int64, error)
	ListOperationalJobEventsAfter(context.Context, sqlcgen.ListOperationalJobEventsAfterParams) ([]sqlcgen.JobEvent, error)
	OperationalJobQueueMetrics(context.Context, int32) ([]sqlcgen.OperationalJobQueueMetricsRow, error)
	PruneOperationalJobEvents(context.Context, sqlcgen.PruneOperationalJobEventsParams) (int64, error)
	PruneTerminalOperationalJobRuns(context.Context, sqlcgen.PruneTerminalOperationalJobRunsParams) (int64, error)
	PruneTerminalPipelineRuns(context.Context, sqlcgen.PruneTerminalPipelineRunsParams) (int64, error)
}

// ListRuns returns a filtered page and a replay high-water mark. Cursor is read
// first to close the REST-to-SSE race described on Detail.
func (s *Service) ListRuns(ctx context.Context, f Filter, limit, offset int32) ([]Run, int64, int64, error) {
	if s.op == nil {
		return nil, 0, 0, ErrOperationalUnavailable
	}
	cursor, err := s.op.MaxOperationalJobEventCursor(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	p := listParams(f, limit, offset)
	rows, err := s.op.ListOperationalJobRuns(ctx, p)
	if err != nil {
		return nil, 0, 0, err
	}
	total, err := s.op.CountOperationalJobRuns(ctx, countParams(f))
	if err != nil {
		return nil, 0, 0, err
	}
	runs := make([]Run, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, runFromRow(row))
	}
	return runs, total, cursor, nil
}

// GetDetail returns one run and a newest-first event page through the captured
// high-water mark.
func (s *Service) GetDetail(ctx context.Context, id uuid.UUID, limit, offset int32) (Detail, error) {
	if s.op == nil {
		return Detail{}, ErrOperationalUnavailable
	}
	cursor, err := s.op.MaxOperationalJobEventCursor(ctx)
	if err != nil {
		return Detail{}, err
	}
	row, err := s.op.GetOperationalJobRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrRunNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	events, err := s.op.ListOperationalJobEvents(ctx, sqlcgen.ListOperationalJobEventsParams{
		JobID: id, ThroughCursor: cursor, ResultLimit: limit, ResultOffset: offset,
	})
	if err != nil {
		return Detail{}, err
	}
	total, err := s.op.CountOperationalJobEvents(ctx, sqlcgen.CountOperationalJobEventsParams{
		JobID: id, ThroughCursor: cursor,
	})
	if err != nil {
		return Detail{}, err
	}
	out := make([]Event, 0, len(events))
	for _, event := range events {
		out = append(out, eventFromRow(event))
	}
	return Detail{Run: runFromRow(row), Events: out, EventsTotal: total, EventCursor: cursor}, nil
}

// EventsAfter returns oldest-first replay events strictly after cursor.
func (s *Service) EventsAfter(ctx context.Context, cursor int64, f Filter, limit int32) ([]Event, error) {
	if s.op == nil {
		return nil, ErrOperationalUnavailable
	}
	p := eventParams(f, cursor, limit)
	rows, err := s.op.ListOperationalJobEventsAfter(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row))
	}
	return out, nil
}

// CursorBounds returns the retained event cursor range (0,0 when empty).
func (s *Service) CursorBounds(ctx context.Context) (int64, int64, error) {
	if s.op == nil {
		return 0, 0, ErrOperationalUnavailable
	}
	min, err := s.op.MinOperationalJobEventCursor(ctx)
	if err != nil {
		return 0, 0, err
	}
	max, err := s.op.MaxOperationalJobEventCursor(ctx)
	return min, max, err
}

// QueueHealth returns bounded-cardinality, DB-derived queue-age/staleness gauges.
func (s *Service) QueueHealth(ctx context.Context, staleAfter time.Duration) ([]QueueHealth, error) {
	if s.op == nil {
		return nil, ErrOperationalUnavailable
	}
	seconds := int32(staleAfter / time.Second)
	if seconds < 1 {
		seconds = 300
	}
	rows, err := s.op.OperationalJobQueueMetrics(ctx, seconds)
	if err != nil {
		return nil, err
	}
	out := make([]QueueHealth, 0, len(rows))
	for _, row := range rows {
		out = append(out, QueueHealth(row))
	}
	return out, nil
}

// Prune applies the explicit Phase-1 retention policy: event history is kept 30
// days, terminal runs/pipelines 90 days, and active work is never deleted.
func (s *Service) Prune(ctx context.Context, now time.Time) (events, runs, pipelines int64, err error) {
	if s.op == nil {
		return 0, 0, 0, ErrOperationalUnavailable
	}
	const batchSize = int32(10000)
	events, err = s.op.PruneOperationalJobEvents(ctx, sqlcgen.PruneOperationalJobEventsParams{
		Cutoff: now.Add(-30 * 24 * time.Hour), BatchSize: batchSize,
	})
	if err != nil {
		return
	}
	cutoff := pgtype.Timestamptz{Time: now.Add(-90 * 24 * time.Hour), Valid: true}
	runs, err = s.op.PruneTerminalOperationalJobRuns(ctx, sqlcgen.PruneTerminalOperationalJobRunsParams{
		Cutoff: cutoff, BatchSize: batchSize,
	})
	if err != nil {
		return
	}
	pipelines, err = s.op.PruneTerminalPipelineRuns(ctx, sqlcgen.PruneTerminalPipelineRunsParams{
		Cutoff: cutoff, BatchSize: batchSize,
	})
	return
}

func listParams(f Filter, limit, offset int32) sqlcgen.ListOperationalJobRunsParams {
	return sqlcgen.ListOperationalJobRunsParams{
		State: optional(f.State), Type: optional(f.Type), Queue: optional(f.Queue),
		ResourceType: optional(f.ResourceType), ResourceID: optional(f.ResourceID),
		WorkerID: optional(f.WorkerID), Failure: f.Failure,
		CreatedAfter: pgTime(f.CreatedAfter), CreatedBefore: pgTime(f.CreatedBefore),
		ResultLimit: limit, ResultOffset: offset,
	}
}

func countParams(f Filter) sqlcgen.CountOperationalJobRunsParams {
	p := listParams(f, 0, 0)
	return sqlcgen.CountOperationalJobRunsParams{
		State: p.State, Type: p.Type, Queue: p.Queue, ResourceType: p.ResourceType,
		ResourceID: p.ResourceID, WorkerID: p.WorkerID, Failure: p.Failure,
		CreatedAfter: p.CreatedAfter, CreatedBefore: p.CreatedBefore,
	}
}

func eventParams(f Filter, cursor int64, limit int32) sqlcgen.ListOperationalJobEventsAfterParams {
	var jobID pgtype.UUID
	if f.JobID != nil {
		jobID = pgtype.UUID{Bytes: *f.JobID, Valid: true}
	}
	return sqlcgen.ListOperationalJobEventsAfterParams{
		AfterCursor: cursor, JobID: jobID, State: optional(f.State), Type: optional(f.Type),
		Queue: optional(f.Queue), ResourceType: optional(f.ResourceType),
		ResourceID: optional(f.ResourceID), WorkerID: optional(f.WorkerID), Failure: f.Failure,
		CreatedAfter: pgTime(f.CreatedAfter), CreatedBefore: pgTime(f.CreatedBefore),
		ResultLimit: limit,
	}
}

func optional(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func pgTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}

func runFromRow(row sqlcgen.JobRun) Run {
	return Run{
		ID: row.ID, PipelineRunID: pgUUIDString(row.PipelineRunID), ParentJobID: pgUUIDString(row.ParentJobID),
		Type: row.Type, Queue: row.Queue, State: row.State, Stage: row.Stage,
		ProgressPercent: row.ProgressPercent, Priority: row.Priority, Attempt: row.Attempt,
		MaxAttempts: row.MaxAttempts, ActorID: pgUUIDString(row.ActorID),
		ResourceType: row.ResourceType, ResourceID: row.ResourceID, RequestID: row.RequestID,
		CorrelationID: row.CorrelationID, TraceID: row.TraceID, WorkerID: row.WorkerID,
		LeaseExpiresAt: pgTimePtr(row.LeaseExpiresAt), HeartbeatAt: pgTimePtr(row.HeartbeatAt),
		CreatedAt: row.CreatedAt, ClaimedAt: pgTimePtr(row.ClaimedAt), StartedAt: pgTimePtr(row.StartedAt),
		UpdatedAt: row.UpdatedAt, FinishedAt: pgTimePtr(row.FinishedAt),
		InputMetadata: safeMetadata(row.InputMetadata), OutputMetadata: safeMetadata(row.OutputMetadata),
		ErrorClass: row.ErrorClass, ErrorCode: row.ErrorCode,
		ErrorDetail: redactDetail(row.ErrorDetail), ErrorRetryable: row.ErrorRetryable,
	}
}

func eventFromRow(row sqlcgen.JobEvent) Event {
	return Event{
		ID: row.ID, Cursor: formatCursor(row.Cursor), JobID: row.JobID,
		PipelineRunID: pgUUIDString(row.PipelineRunID), Kind: row.Kind, State: row.State,
		Stage: row.Stage, ProgressPercent: row.ProgressPercent, Attempt: row.Attempt,
		WorkerID: row.WorkerID, Message: redactDetail(row.Message), Metadata: safeMetadata(row.Metadata),
		RequestID: row.RequestID, CorrelationID: row.CorrelationID, TraceID: row.TraceID,
		OccurredAt: row.OccurredAt,
	}
}

func pgUUIDString(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
}

func pgTimePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

func formatCursor(v int64) string { return fmt.Sprintf("%d", v) }

// Metadata is deny-by-default at the API edge as defense in depth. Writers must
// choose from this bounded operational vocabulary; unknown keys are discarded.
var metadataAllowlist = map[string]bool{
	"mode": true, "resolver": true, "language": true, "media_class": true,
	"network": true, "dry_run": true, "conflict_policy": true,
	"source_version": true, "planned": true, "imported": true, "skipped": true,
	"failed": true, "unsupported": true, "width": true, "height": true,
	"duration_seconds": true, "bytes": true, "items": true, "reason_code": true,
}

func safeMetadata(raw []byte) json.RawMessage {
	if len(raw) == 0 || len(raw) > 16384 {
		return json.RawMessage(`{}`)
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return json.RawMessage(`{}`)
	}
	out := make(map[string]any)
	for key, value := range in {
		if metadataAllowlist[key] {
			if safe, ok := safeMetadataValue(value); ok {
				out[key] = safe
			}
		}
	}
	data, err := json.Marshal(out)
	if err != nil || len(data) > 8192 {
		return json.RawMessage(`{}`)
	}
	return data
}

var (
	sensitiveDetail   = regexp.MustCompile(`(?i)(?:https?|s3|file)://\S+|(?:authorization|cookie|password|token|secret)\s*[:=]\s*\S+`)
	emailDetail       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	safeMetadataToken = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,128}$`)
)

func safeMetadataValue(value any) (any, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case float64:
		// JSON decoding produces float64 for numbers. Operational counts and sizes
		// are non-negative and bounded below the exact-integer range.
		if v >= 0 && v <= 9_007_199_254_740_991 {
			return v, true
		}
	case string:
		// Only a short, single-token enum/opaque identifier is admitted. This
		// structurally rejects URLs, signed query strings, emails, Bearer values,
		// multiline process output, and nested serialized payloads.
		if utf8.ValidString(v) && safeMetadataToken.MatchString(v) &&
			!sensitiveDetail.MatchString(v) && !emailDetail.MatchString(v) {
			return v, true
		}
	}
	return nil, false
}

func redactDetail(s string) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "�")
	s = sensitiveDetail.ReplaceAllString(s, "[redacted]")
	s = emailDetail.ReplaceAllString(s, "[redacted-email]")
	if len(s) > 2048 {
		s = s[:2048]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
