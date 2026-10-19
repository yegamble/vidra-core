package searchevents

import (
	"context"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// DefaultReconcilePageSize is the reconcile sweep's keyset page size.
//
// Lowered from the original 500 (MASTER-PLAN §2.3) so that ONE page reliably
// fits inside maxRequestPayloadBytes. The drainer already refuses to put more
// than a budget's worth of payload in a request, but a page bigger than the
// whole budget has to be sent alone, and a page bigger than the RECEIVER's body
// limit cannot be delivered at all — it would retry to the cap and dead-letter,
// which is the failure this whole bound exists to prevent. Documents vary in
// size (a long description is worth many short ones), so the margin is
// deliberately wide rather than tuned to an average.
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
		if err := e.enqueueWithError(ctx, TypeReconcilePage, reconcilePagePayload{RunID: runID, Videos: docs}); err != nil {
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
