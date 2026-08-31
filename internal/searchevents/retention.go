package searchevents

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Outbox row states, mirroring migration 0092's CHECK constraint.
const (
	// StatePending is an event that has NOT been delivered yet. It is never
	// pruned at any age: it is an index mutation, or a privacy-critical
	// user.history_deleted purge, that has not happened. There is no second
	// copy — deleting one loses it, and nothing downstream would notice.
	StatePending = "pending"
	// StateDelivered is finished work: the search service has it, and core has
	// no remaining use for the row.
	StateDelivered = "delivered"
	// StateDead is an event the drainer gave up on after maxDrainAttempts. It is
	// the only surviving evidence of why a delivery failed.
	StateDead = "dead"
)

const (
	// DefaultEventRetentionDays backstops the search_event_retention_days
	// instance setting (default 90, validated 1..365). It is used when the
	// overlay cannot be read or answers outside that range: a window that has
	// gone missing must NOT silently become infinite (the defect this worker
	// exists to close) nor zero (which would delete rows the operator meant to
	// keep).
	DefaultEventRetentionDays = 90
	// maxEventRetentionDays mirrors the setting's own validated ceiling, so a
	// value that somehow got past validation cannot widen the window.
	maxEventRetentionDays = 365

	// DeadForensicFloor is the minimum window a dead row gets, regardless of how
	// tight the configured retention is.
	//
	// This is the ONE place the two states diverge, and it is deliberately a
	// floor rather than an extension: on a default install (90 days) dead and
	// delivered expire identically, so core never holds a search event longer
	// than the operator's declared window. The floor only bites below it, and
	// the reason is that a dead row is evidence — the drainer's entire retry
	// lifetime is under a day (10 attempts, backoff capped at 1h), so a window
	// of hours would delete the evidence before anyone watching
	// vidra_queue_depth{queue="search_outbox",state="dead"} on a weekly rotation
	// ever saw it. Seven days is a rotation, and it is bounded: a dead row can
	// outlive a delivered one by at most six days, and only on an install that
	// chose a window tighter than a week.
	DeadForensicFloor = 7 * 24 * time.Hour

	// pruneBatchSize matches qoe.pruneBatchSize and jobstatus.Prune. Batching
	// bounds the lock footprint of a delete that, on the first sweep of an
	// install that has never pruned, could otherwise touch the whole table while
	// the drainer is claiming from it.
	pruneBatchSize = int32(10000)
	// pruneMaxBatches caps one sweep at 1M rows per state, so a catch-up on a
	// table that has accumulated since migration 0092 spreads over several ticks
	// instead of becoming one unbounded transaction storm.
	pruneMaxBatches = 100
)

// ErrPrunerUnavailable is returned when the pruner has no repository wired.
var ErrPrunerUnavailable = errors.New("searchevents: prune repository unavailable")

// PruneRepository is the retention data access. *sqlcgen.Queries satisfies it.
type PruneRepository interface {
	PruneSearchOutbox(ctx context.Context, arg sqlcgen.PruneSearchOutboxParams) (int64, error)
}

// Pruner enforces retention on search_outbox.
//
// The outbox has never been pruned: 0092 built the delivery path and no DELETE
// against the table existed anywhere in the tree. What accumulated is not inert
// queue exhaust — a server-side search.submitted payload carries the raw query
// text, the user_id and the session_id, so a user's searches survived both
// "Clear search history" and account deletion in core's PRIMARY database, while
// the UI told them the removal was permanent. Bounding that is why this exists;
// the table size is the second reason, not the first.
//
// Retention is state-aware and the window comes from the existing
// search_event_retention_days setting rather than a new knob, so the number an
// operator sets is the number that governs search-event data everywhere on the
// instance — the same value is already pushed to vidra-search in
// search.config_updated. Before this, the search service honoured it and core's
// own copy did not.
type Pruner struct {
	repo         PruneRepository
	retentionDay func() int64
	logger       *slog.Logger
}

// NewPruner builds a Pruner. retentionDays reads the live
// search_event_retention_days overlay (nil falls back to the default); logger
// defaults to slog.Default().
func NewPruner(repo PruneRepository, retentionDays func() int64, logger *slog.Logger) *Pruner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pruner{repo: repo, retentionDay: retentionDays, logger: logger}
}

// Prune deletes expired terminal rows in bounded batches and reports how many of
// each state went. Pending rows are never touched, at any age.
//
// Running it twice is a no-op: the second sweep finds nothing past the cutoff
// and costs one query per prunable state. Two replicas running it at once would
// be correct too — the batch subselect takes whatever still matches — but it is
// leader-gated anyway so an idle instance does no queue work.
func (p *Pruner) Prune(ctx context.Context, now time.Time) (delivered, dead int64, err error) {
	if p == nil || p.repo == nil {
		return 0, 0, ErrPrunerUnavailable
	}
	window := p.retentionWindow()
	delivered, err = p.pruneState(ctx, StateDelivered, now.Add(-window))
	if err != nil {
		return delivered, 0, err
	}
	deadWindow := window
	if deadWindow < DeadForensicFloor {
		deadWindow = DeadForensicFloor
	}
	dead, err = p.pruneState(ctx, StateDead, now.Add(-deadWindow))
	return delivered, dead, err
}

// retentionWindow is the configured search-event window, clamped to the range
// the setting itself validates.
func (p *Pruner) retentionWindow() time.Duration {
	days := int64(DefaultEventRetentionDays)
	if p.retentionDay != nil {
		if d := p.retentionDay(); d >= 1 && d <= maxEventRetentionDays {
			days = d
		}
	}
	return time.Duration(days) * 24 * time.Hour
}

// pruneState runs batches for one state until one comes back short (nothing
// left) or the batch cap is reached, whichever is first.
func (p *Pruner) pruneState(ctx context.Context, state string, cutoff time.Time) (int64, error) {
	var total int64
	for i := 0; i < pruneMaxBatches; i++ {
		n, err := p.repo.PruneSearchOutbox(ctx, sqlcgen.PruneSearchOutboxParams{
			State: state, Cutoff: cutoff, BatchSize: pruneBatchSize,
		})
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(pruneBatchSize) {
			return total, nil
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
	return total, nil
}
