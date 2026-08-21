package searchevents

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// eventLeaseSeconds is how long a claimed row stops being due. Draining the outbox is one batched HTTP POST, so
// the lease only has to outlast one attempt; it is generous rather than tight so
// a worker killed mid-request does not have its row re-claimed while the remote
// side is still processing the first attempt. A crashed worker's row becomes due
// again on its own once the lease elapses -- that is the whole recovery
// mechanism, and it needs no boot-time sweep and no assumption about who else is
// alive.
const eventLeaseSeconds = 300

const (
	// maxDrainAttempts is how many times an event is retried before it is
	// dead-lettered (state 'dead'). MASTER-PLAN §2.3.
	maxDrainAttempts = 10
	// drainBaseBackoff is the first retry delay; it doubles each attempt.
	drainBaseBackoff = 30 * time.Second
	// maxDrainBackoff caps the exponential backoff (MASTER-PLAN §2.3: 30s→1h).
	maxDrainBackoff = time.Hour
	// maxLastErrorLen bounds the stored last_error string.
	maxLastErrorLen = 500
)

// DrainRepository is the outbox data access the drainer needs.
type DrainRepository interface {
	ClaimDueSearchEvents(ctx context.Context, arg sqlcgen.ClaimDueSearchEventsParams) ([]sqlcgen.ClaimDueSearchEventsRow, error)
	MarkSearchEventDelivered(ctx context.Context, id int64) error
	RescheduleSearchEvent(ctx context.Context, arg sqlcgen.RescheduleSearchEventParams) error
	DeadLetterSearchEvent(ctx context.Context, arg sqlcgen.DeadLetterSearchEventParams) error
}

// Sender delivers a batched envelope to the search service. searchclient.Client
// satisfies it. A returned error is a TRANSPORT/5xx failure (the whole batch
// retries); per-event problems ride EventBatchResult.Failed on a nil error.
type Sender interface {
	SendEvents(ctx context.Context, batch EventBatch) (EventBatchResult, error)
}

// DrainMetrics receives best-effort dead-letter counts (nil is fine).
type DrainMetrics interface {
	IncSearchDeadLetter(eventType string)
}

// Drainer claims due outbox rows, batches them to the search service, and marks
// them delivered — or reschedules with exponential backoff, dead-lettering after
// the attempt cap. It is driven by runSearchOutboxWorker on a ticker.
type Drainer struct {
	repo    DrainRepository
	sender  Sender
	logger  *slog.Logger
	metrics DrainMetrics
}

// DrainOption customises the Drainer.
type DrainOption func(*Drainer)

// WithDrainMetrics wires the dead-letter counter.
func WithDrainMetrics(m DrainMetrics) DrainOption { return func(d *Drainer) { d.metrics = m } }

// NewDrainer builds a Drainer. logger defaults to slog.Default().
func NewDrainer(repo DrainRepository, sender Sender, logger *slog.Logger, opts ...DrainOption) *Drainer {
	if logger == nil {
		logger = slog.Default()
	}
	d := &Drainer{repo: repo, sender: sender, logger: logger}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Drain claims up to limit due events and delivers them in one batch. On a
// transport/5xx failure the whole batch is rescheduled/dead-lettered; on a 200
// each event is marked delivered unless the search service reported it in
// Failed[], in which case only that event retries. Returns the number delivered;
// only the claim-query error is surfaced (per-event outcomes are persisted).
func (d *Drainer) Drain(ctx context.Context, limit int32) (int, error) {
	rows, err := d.repo.ClaimDueSearchEvents(ctx, sqlcgen.ClaimDueSearchEventsParams{
		BatchSize:    limit,
		LeaseSeconds: eventLeaseSeconds,
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	batch := EventBatch{Events: make([]Envelope, 0, len(rows))}
	for _, r := range rows {
		batch.Events = append(batch.Events, Envelope{
			EventID:       r.EventID,
			Type:          r.EventType,
			OccurredAt:    r.CreatedAt,
			SchemaVersion: SchemaVersion,
			Payload:       r.Payload,
		})
	}

	result, sendErr := d.sender.SendEvents(ctx, batch)
	if sendErr != nil {
		msg := sendErr.Error()
		for _, r := range rows {
			d.recordFailure(ctx, r, msg)
		}
		return 0, sendErr
	}

	failed := make(map[uuid.UUID]FailedEvent, len(result.Failed))
	for _, f := range result.Failed {
		failed[f.EventID] = f
	}
	delivered := 0
	for _, r := range rows {
		if f, bad := failed[r.EventID]; bad {
			d.recordFailure(ctx, r, f.Code+": "+f.Message)
			continue
		}
		if err := d.repo.MarkSearchEventDelivered(ctx, r.ID); err != nil {
			d.logger.WarnContext(ctx, "search event mark-delivered failed", "id", r.ID, "error", err)
			continue
		}
		delivered++
	}
	return delivered, nil
}

// recordFailure reschedules with backoff, or dead-letters after the cap.
func (d *Drainer) recordFailure(ctx context.Context, row sqlcgen.ClaimDueSearchEventsRow, msg string) {
	if len(msg) > maxLastErrorLen {
		msg = msg[:maxLastErrorLen]
	}
	attempts := int(row.Attempts) + 1
	if attempts >= maxDrainAttempts {
		if err := d.repo.DeadLetterSearchEvent(ctx, sqlcgen.DeadLetterSearchEventParams{
			ID: row.ID, LastError: msg,
		}); err != nil {
			d.logger.WarnContext(ctx, "search event dead-letter failed", "id", row.ID, "error", err)
			return
		}
		d.logger.WarnContext(ctx, "search event dead-lettered", "id", row.ID, "event_type", row.EventType, "attempts", attempts, "last_error", msg)
		if d.metrics != nil {
			d.metrics.IncSearchDeadLetter(row.EventType)
		}
		return
	}
	_ = d.repo.RescheduleSearchEvent(ctx, sqlcgen.RescheduleSearchEventParams{
		ID:            row.ID,
		NextAttemptAt: time.Now().UTC().Add(drainBackoff(attempts)),
		LastError:     msg,
	})
}

// drainBackoff is drainBaseBackoff * 2^(attempts-1), capped at maxDrainBackoff.
func drainBackoff(attempts int) time.Duration {
	d := drainBaseBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= maxDrainBackoff {
			return maxDrainBackoff
		}
	}
	return d
}
