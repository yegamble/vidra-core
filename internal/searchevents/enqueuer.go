package searchevents

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// OutboxRepository is the data access the Enqueuer needs: the single-statement
// outbox insert plus the doc-source reads used to build video.upsert and
// reconcile payloads. *sqlcgen.Queries satisfies it; tests substitute a fake.
type OutboxRepository interface {
	EnqueueSearchEvent(ctx context.Context, arg sqlcgen.EnqueueSearchEventParams) error
	GetVideoSearchDoc(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoSearchDocRow, error)
	ListVideoSearchDocsPage(ctx context.Context, arg sqlcgen.ListVideoSearchDocsPageParams) ([]sqlcgen.ListVideoSearchDocsPageRow, error)
}

// Metrics receives best-effort enqueue-failure counts (nil is fine). Implemented
// by observability.Metrics in production.
type Metrics interface {
	IncSearchEnqueueFailure(eventType string)
}

// Enqueuer builds and persists search events to the outbox. All methods are
// best-effort: a marshal or DB error is logged + metered and swallowed so the
// caller's request never fails. A nil *Enqueuer is a valid no-op receiver, so
// call sites can be wired unconditionally.
type Enqueuer struct {
	repo    OutboxRepository
	logger  *slog.Logger
	metrics Metrics
}

// Option customises the Enqueuer.
type Option func(*Enqueuer)

// WithMetrics wires the enqueue-failure counter.
func WithMetrics(m Metrics) Option { return func(e *Enqueuer) { e.metrics = m } }

// NewEnqueuer builds an Enqueuer over repo. logger defaults to slog.Default().
func NewEnqueuer(repo OutboxRepository, logger *slog.Logger, opts ...Option) *Enqueuer {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Enqueuer{repo: repo, logger: logger}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// enqueue marshals payload and inserts one outbox row. Best-effort.
func (e *Enqueuer) enqueue(ctx context.Context, eventType string, payload any) {
	if e == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		e.fail(ctx, eventType, err)
		return
	}
	e.enqueueRaw(ctx, eventType, raw)
}

// enqueueRaw inserts one outbox row from an already-serialized payload.
func (e *Enqueuer) enqueueRaw(ctx context.Context, eventType string, payload json.RawMessage) {
	if e == nil {
		return
	}
	if err := e.repo.EnqueueSearchEvent(ctx, sqlcgen.EnqueueSearchEventParams{
		EventType: eventType,
		Payload:   payload,
	}); err != nil {
		e.fail(ctx, eventType, err)
	}
}

// fail logs + meters a dropped event.
func (e *Enqueuer) fail(ctx context.Context, eventType string, err error) {
	e.logger.WarnContext(ctx, "search event enqueue failed (dropped; reconcile is the backstop)",
		"event_type", eventType, "error", err)
	if e.metrics != nil {
		e.metrics.IncSearchEnqueueFailure(eventType)
	}
}

// EnqueueVideoUpsert fetches the full doc for videoID and enqueues a video.upsert.
// A missing/unreadable video is logged + dropped (best-effort).
func (e *Enqueuer) EnqueueVideoUpsert(ctx context.Context, videoID uuid.UUID) {
	if e == nil {
		return
	}
	row, err := e.repo.GetVideoSearchDoc(ctx, videoID)
	if err != nil {
		e.fail(ctx, TypeVideoUpsert, err)
		return
	}
	e.enqueue(ctx, TypeVideoUpsert, videoUpsertPayload{Video: videoDocFromRow(row)})
}

// EnqueueVideoSuppress enqueues a video.suppress with the given reason.
func (e *Enqueuer) EnqueueVideoSuppress(ctx context.Context, videoID uuid.UUID, reason string) {
	e.enqueue(ctx, TypeVideoSuppress, videoSuppressPayload{VideoID: videoID, Reason: reason})
}

// EnqueueVideoStats enqueues a video.stats refresh (optional; reconcile-driven).
func (e *Enqueuer) EnqueueVideoStats(ctx context.Context, videoID uuid.UUID, views, likes int64) {
	e.enqueue(ctx, TypeVideoStats, videoStatsPayload{VideoID: videoID, Views: views, Likes: likes})
}

// EnqueueChannelUpsert enqueues a channel.upsert (denormalized channel fields).
func (e *Enqueuer) EnqueueChannelUpsert(ctx context.Context, id uuid.UUID, handle, displayName string, ownerID uuid.UUID) {
	e.enqueue(ctx, TypeChannelUpsert, channelUpsertPayload{Channel: channelDoc{
		ID: id, Handle: handle, DisplayName: displayName, OwnerID: ownerID,
	}})
}

// EnqueueChannelDelete enqueues a channel.delete (suppresses its docs).
func (e *Enqueuer) EnqueueChannelDelete(ctx context.Context, channelID uuid.UUID) {
	e.enqueue(ctx, TypeChannelDelete, channelDeletePayload{ChannelID: channelID})
}

// EnqueueUserSuppress enqueues a user.suppress (unlisted true suppresses the
// owner's docs; false restores them).
func (e *Enqueuer) EnqueueUserSuppress(ctx context.Context, userID uuid.UUID, unlisted bool) {
	e.enqueue(ctx, TypeUserSuppress, userSuppressPayload{UserID: userID, Unlisted: unlisted})
}

// EnqueueUserHistoryDeleted enqueues a user.history_deleted for the given scope.
func (e *Enqueuer) EnqueueUserHistoryDeleted(ctx context.Context, userID uuid.UUID, scope string) {
	e.enqueue(ctx, TypeUserHistoryDel, userHistoryDeletedPayload{UserID: userID, Scope: scope})
}

// EnqueueUserPurge enqueues the durable half of an account deletion: suppress the
// owner's docs (unlisted=true) and purge all their history. The full row deletion
// is a best-effort DIRECT DELETE /internal/v1/users/{id} the caller also issues;
// these events guarantee the privacy-critical outcome even if that call is lost.
func (e *Enqueuer) EnqueueUserPurge(ctx context.Context, userID uuid.UUID) {
	e.EnqueueUserSuppress(ctx, userID, true)
	e.EnqueueUserHistoryDeleted(ctx, userID, HistoryScopeAll)
}

// EnqueueConfigUpdated enqueues a search.config_updated carrying the effective
// search-settings subset.
func (e *Enqueuer) EnqueueConfigUpdated(ctx context.Context, cfg SearchConfig) {
	e.enqueue(ctx, TypeConfigUpdated, configUpdatedPayload{Settings: cfg})
}

// EnqueueWatchProgress enqueues a video.watch_progress (caller throttles).
func (e *Enqueuer) EnqueueWatchProgress(ctx context.Context, wp WatchProgress) {
	e.enqueue(ctx, TypeVideoWatchProg, wp)
}

// EnqueueBehavioral enqueues a behavioural event whose payload the caller has
// already assembled + enriched (the POST /search/events pass-through and
// search.submitted). eventType must be one the search service understands;
// unknown types are accepted-and-ignored there.
func (e *Enqueuer) EnqueueBehavioral(ctx context.Context, eventType string, payload json.RawMessage) {
	e.enqueueRaw(ctx, eventType, payload)
}
