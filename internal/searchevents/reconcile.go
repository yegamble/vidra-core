package searchevents

import (
	"context"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// DefaultReconcilePageSize is the reconcile sweep's keyset page size
// (MASTER-PLAN §2.3: 500 docs/page).
const DefaultReconcilePageSize = 500

// RunReconcile emits a full index-reconciliation sweep to the outbox: one
// reconcile.begin, then reconcile.page events (each carrying up to pageSize full
// docs) over every eligible public+published local video keyset-paged by id, then
// a reconcile.end with the total. The search service, on reconcile.end, suppresses
// any eligible local doc whose run stamp is stale — repairing the index against
// dropped incremental events. Only the paging query error is returned (the page
// enqueues are best-effort, like all emission).
func (e *Enqueuer) RunReconcile(ctx context.Context, pageSize int32) error {
	if e == nil {
		return nil
	}
	if pageSize <= 0 {
		pageSize = DefaultReconcilePageSize
	}
	runID := uuid.New()
	e.enqueue(ctx, TypeReconcileBegin, reconcileBeginPayload{RunID: runID})

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
		e.enqueue(ctx, TypeReconcilePage, reconcilePagePayload{RunID: runID, Videos: docs})
		total += len(docs)
		after = rows[len(rows)-1].ID
		if len(rows) < int(pageSize) {
			break
		}
	}

	e.enqueue(ctx, TypeReconcileEnd, reconcileEndPayload{RunID: runID, Total: total})
	return nil
}
