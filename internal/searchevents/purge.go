package searchevents

import (
	"context"

	"github.com/google/uuid"
)

// PurgeUserEvents erases core's OWN copy of a user's search data: every
// data-bearing search_outbox row whose payload names them at the top level
// (search.submitted with its raw query text, the POST /search/events
// behavioural pass-through, video.watch_progress). It returns the number of
// rows removed.
//
// This is the local half of an erasure and it deliberately lives on the
// Enqueuer, next to the code that WROTE those rows. The two facts that make the
// delete correct -- which payload key carries the subject, and which event types
// are control instructions rather than data -- are the enqueuer's own knowledge;
// splitting them across two types is how they drift apart. The enforcement
// itself is in the SQL (PurgeUserSearchOutbox), not here, so no call site can
// vary it.
//
// Unlike every other method on this type it is NOT best-effort and returns its
// error. A dropped video.upsert is repaired by the reconcile sweep; there is no
// sweep that re-erases, so an erasure that silently failed would leave the data
// AND report success. The caller decides what a failure means for its status
// code -- but it must not be told nothing happened when something did not.
//
// Running it twice deletes nothing the second time and is safe: the erasure is
// idempotent by construction (the rows are gone), and it leaves the pending
// user.suppress / user.history_deleted instructions alone on every pass.
func (e *Enqueuer) PurgeUserEvents(ctx context.Context, userID uuid.UUID) (int64, error) {
	if e == nil || e.repo == nil {
		return 0, nil
	}
	return e.repo.PurgeUserSearchOutbox(ctx, userID.String())
}
