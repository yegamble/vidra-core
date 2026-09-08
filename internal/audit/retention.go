package audit

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrPruneUnavailable means the service has no repository wired, so a caller
// asking for a sweep is asking a service that cannot do one — reported rather
// than silently answered "0 rows".
var ErrPruneUnavailable = errors.New("audit: prune repository unavailable")

// Retention for the security-audit trail.
//
// Until this existed, audit_log was append-only in practice: 0029 created it,
// 0084 gave it a typed envelope, and no DELETE against it existed anywhere in
// the tree. Every login, every failed MFA challenge, every moderation decision
// and every admin config change an instance had ever made was kept forever. That
// is a defensible default for a week-old instance and an indefensible one for a
// five-year-old instance, for two separate reasons that pull the same way: an
// unbounded table nobody sweeps eventually costs an operator real disk and real
// index, and — the reason that matters more — a trail that keeps a user's
// authentication history for the life of the install is data retained past any
// purpose it was collected for.
//
// The window is a KNOB and not a constant because the honest answer is
// jurisdictional. Some operators are required to keep a security trail for a
// year; some are required not to keep one much longer than that.
//
// # What this deliberately is not
//
// It is NOT state-aware like searchevents.Pruner, and it does not exempt
// anything. An audit row is terminal the moment it is written — the table is
// append-only, has no lifecycle, and nothing reads a row back to decide whether
// something is still in flight — so "is this row finished with" has one answer.
// A per-action exemption list was considered and rejected: it would be the one
// place where the retention an operator configured is not the retention they
// get, and an operator who needs a longer trail for one action needs a longer
// window, not a hidden exception.

const (
	// DefaultRetention is 400 days: comfortably more than a year, so a trail can
	// answer "what changed at this point last year" and the annual review that
	// asks it, plus the five weeks of slack that stop a 365-day window from
	// deleting last year's evidence the week before somebody goes looking.
	DefaultRetention = 400 * 24 * time.Hour

	// pruneBatchSize bounds one DELETE. Smaller than the QoE and outbox pruners'
	// 10k because this table is low-volume by construction (it is the security
	// trail, not an activity stream) — a batch sized for playback telemetry
	// would in practice be one unbounded statement here, which is the thing
	// batching exists to prevent.
	pruneBatchSize = int32(2000)

	// pruneMaxBatches caps ONE sweep at 200k rows, so the first sweep on an
	// install that has never pruned spreads over several daily ticks instead of
	// becoming one transaction storm against the table every write appends to.
	// A run that hits the cap deletes the OLDEST expired rows (the query orders)
	// and the next tick continues from there.
	pruneMaxBatches = 100
)

// Prune deletes audit rows older than retention, oldest first, in bounded
// batches, and records ONE audit row naming how many went.
//
// A zero or negative retention keeps the trail forever and does no work at all —
// the house convention for every other window in this codebase (0 = unlimited),
// and the setting an operator who is required to keep everything needs.
//
// The count is carried in the envelope's `count` metadata field rather than in
// prose, because audit_log cannot carry prose: `reason` is a bounded, structured
// string and the metadata vocabulary is an allowlist. `count` is already in it.
//
// The bookkeeping row is written only when rows were actually deleted. A daily
// tick that finds nothing expired has nothing to record, and recording it anyway
// would make the trail's own exhaust the majority of the trail on any instance
// quiet enough to matter — rows which would then, in their turn, need pruning.
//
// Running it twice is a no-op: the second sweep finds nothing past the cutoff
// and costs one query. It is leader-gated by its caller so an idle replica does
// no sweep work, but two concurrent sweeps would also be correct — the batch
// subselect takes whatever still matches.
func (s *Service) Prune(ctx context.Context, now time.Time, retention time.Duration) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, ErrPruneUnavailable
	}
	if retention <= 0 {
		return 0, nil
	}
	deleted, err := s.pruneBatches(ctx, now.Add(-retention))
	if deleted > 0 {
		// Best-effort, exactly as every other audit write in this codebase is:
		// a bookkeeping row that failed to land must not turn a successful
		// prune into a failed one, and the worker's log line names the same
		// count either way.
		_ = s.Record(ctx, Event{
			Action: observability.ActionAuditRetentionPrune,
			Result: observability.ResultSuccess,
			Actor:  ActorSnapshot{Kind: "system"},
			Metadata: []MetadataField{
				{Key: "count", Value: strconv.FormatInt(deleted, 10)},
			},
		})
	}
	return deleted, err
}

// pruneBatches runs batches until one comes back short (nothing left) or the
// batch cap is reached, whichever is first. It returns what it managed to delete
// alongside any error, so a partial sweep is still reported and still recorded.
func (s *Service) pruneBatches(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for i := 0; i < pruneMaxBatches; i++ {
		n, err := s.repo.PruneAuditLog(ctx, sqlcgen.PruneAuditLogParams{
			Cutoff: cutoff, BatchSize: pruneBatchSize,
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
