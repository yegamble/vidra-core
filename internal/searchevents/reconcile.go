package searchevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// DefaultReconcilePageSize bounds one source read. Encoded bytes independently
// bound the emitted pages: legal descriptions can expand sixfold in JSON.
const DefaultReconcilePageSize = 200

// RunReconcile emits a full index-reconciliation sweep to the outbox: one
// reconcile.begin, then reconcile.page events (each carrying up to pageSize full
// docs) over every eligible public+published local video keyset-paged by id, then
// a reconcile.end with the total. The search service, on reconcile.end, suppresses
// any eligible local doc whose run stamp is stale — repairing the index against
// dropped incremental events. Paging and enqueue errors abort the sweep and are
// returned to the caller. This prevents enqueue loss from completing a partial
// sweep; it does not guarantee delivery ordering during search-service outages.
func (e *Enqueuer) RunReconcile(ctx context.Context, pageSize int32) error {
	if e == nil {
		return nil
	}
	if pageSize <= 0 {
		pageSize = DefaultReconcilePageSize
	}
	runID := uuid.New()
	if err := e.enqueueWithError(ctx, TypeReconcileBegin, reconcileBeginPayload{RunID: runID}); err != nil {
		return err
	}

	after := uuid.Nil
	total := 0
	for {
		rows, err := e.repo.ListVideoSearchDocsPage(ctx, sqlcgen.ListVideoSearchDocsPageParams{
			After:    after,
			PageSize: pageSize,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]VideoDoc, 0, len(rows))
		for _, r := range rows {
			docs = append(docs, videoDocFromPageRow(r))
		}
		if err := e.enqueueReconcilePages(ctx, runID, docs); err != nil {
			return err
		}
		total += len(docs)
		after = rows[len(rows)-1].ID
		if len(rows) < int(pageSize) {
			break
		}
	}

	return e.enqueueWithError(ctx, TypeReconcileEnd, reconcileEndPayload{RunID: runID, Total: total})
}

func (e *Enqueuer) enqueueReconcilePages(ctx context.Context, runID uuid.UUID, docs []VideoDoc) error {
	page := reconcilePagePayload{RunID: runID, Videos: []VideoDoc{}}
	empty, err := json.Marshal(page)
	if err != nil {
		return err
	}
	// Reserve the real batch/envelope framing, including the longest RFC3339
	// timestamp encoding. Outbox IDs and timestamps are assigned after enqueue.
	frame, err := json.Marshal(EventBatch{Events: []Envelope{{
		Type: TypeReconcilePage, SchemaVersion: SchemaVersion, Payload: empty,
		OccurredAt: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("", 3600)),
	}}})
	if err != nil {
		return err
	}
	size := len(frame)
	for _, doc := range docs {
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		if len(frame)+len(raw) > maxRequestPayloadBytes {
			return fmt.Errorf("search reconcile document %s exceeds %d-byte request budget", doc.ID, maxRequestPayloadBytes)
		}
		n := len(raw)
		if len(page.Videos) > 0 {
			n++ // JSON array comma.
		}
		if size+n > maxRequestPayloadBytes {
			if err := e.enqueueWithError(ctx, TypeReconcilePage, page); err != nil {
				return err
			}
			page.Videos = nil
			size, n = len(frame), len(raw)
		}
		page.Videos = append(page.Videos, doc)
		size += n
	}
	return e.enqueueWithError(ctx, TypeReconcilePage, page)
}
