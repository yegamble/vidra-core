// Package jobstatus aggregates the state of every durable background-work queue
// into one operator-facing snapshot (fix_plan P17.4): per-queue depth counts by
// state plus the oldest pending item's age (a stuck-worker signal), and a merged
// recent-failures list. It is the backend contract behind the admin jobs page.
//
// The snapshot is derived entirely from the queue tables — no separate worker
// heartbeat store is needed: a healthy worker keeps `pending`/`oldest_pending_age`
// low, and dead-lettered rows surface in `failed` + the recent-failures list.
// Failures carry only id/error/attempts — never the inbox URL, source URL,
// storage key, or any argument (those live in columns this package never selects).
package jobstatus

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

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
	TranscodeRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.TranscodeRecentFailuresRow, error)
	FederationRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.FederationRecentFailuresRow, error)
	ImportRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.ImportRecentFailuresRow, error)
	CaptionRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.CaptionRecentFailuresRow, error)
	AccountExportRecentFailures(ctx context.Context, limit int32) ([]sqlcgen.AccountExportRecentFailuresRow, error)
}

// Service produces the durable-queue overview.
type Service struct {
	q Querier
}

// NewService builds the service over a query set (typically *sqlcgen.Queries).
func NewService(q Querier) *Service { return &Service{q: q} }

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
	ov.Queues = []QueueStatus{
		{QueueTranscode, tj.Pending, tj.Running, tj.Done, tj.Failed, tj.OldestPendingAgeSeconds},
		{QueueFederation, fd.Pending, fd.Running, fd.Done, fd.Failed, fd.OldestPendingAgeSeconds},
		{QueueImport, ij.Pending, ij.Running, ij.Done, ij.Failed, ij.OldestPendingAgeSeconds},
		{QueueCaption, cj.Pending, cj.Running, cj.Done, cj.Failed, cj.OldestPendingAgeSeconds},
		{QueueAccountExport, ae.Pending, ae.Running, ae.Done, ae.Failed, ae.OldestPendingAgeSeconds},
		{QueueUploadSessions, us.Pending, us.Running, us.Done, us.Failed, us.OldestPendingAgeSeconds},
	}

	failures := make([]Failure, 0, maxRecentFailures)
	if rows, err := s.q.TranscodeRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueTranscode, r.ID, r.Error, r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.FederationRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueFederation, r.ID, r.Error, r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.ImportRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueImport, r.ID, r.Error, r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.CaptionRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueCaption, r.ID, r.Error, r.Attempts, r.UpdatedAt})
		}
	}
	if rows, err := s.q.AccountExportRecentFailures(ctx, perQueueFailureFetch); err != nil {
		return ov, err
	} else {
		for _, r := range rows {
			failures = append(failures, Failure{QueueAccountExport, r.ID, r.Error, r.Attempts, r.UpdatedAt})
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
