package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var ErrCatalogConflict = errors.New("catalog control expected source fence is stale")

// AcceptCatalogFullRequest couples durable intent and audit under brief source
// authority. The caller authorizes the admin and preflights compatible matching
// receiver authority outside this transaction. No acceptance grants readiness.
func (s *Store) AcceptCatalogFullRequest(ctx context.Context, r catalog.FullRequest, actor audit.ActorSnapshot) (bool, error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	var coalesced bool
	var refusal error
	err := s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		state, err := q.ReadCatalogDeliveryState(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		outcome := "accepted"
		if errors.Is(err, pgx.ErrNoRows) || state.Epoch != r.ExpectedEpoch || state.AllocatedGeneration != r.ExpectedAllocatedGeneration {
			refusal, outcome = ErrCatalogConflict, "catalog_conflict"
		} else {
			coalesced = state.ForceFull && state.ForceFullAfterGeneration == state.AllocatedGeneration
		}
		ev, err := audit.CatalogFullRequestEvent(actor, r, coalesced, outcome)
		if err != nil {
			return err
		}
		if refusal == nil {
			n, err := q.RequestCatalogFull(ctx, sqlcgen.RequestCatalogFullParams{Epoch: r.ExpectedEpoch,
				ExpectedAllocatedGeneration: r.ExpectedAllocatedGeneration})
			if err != nil {
				return err
			}
			if n != 1 {
				refusal, coalesced = ErrCatalogConflict, false
				ev, err = audit.CatalogFullRequestEvent(actor, r, false, "catalog_conflict")
				if err != nil {
					return err
				}
			}
		}
		// A conflict is a committed rejected attempt, with no accepted intent.
		// An audit INSERT failure instead aborts the entire business transaction.
		return audit.NewService(q).Record(ctx, ev)
	})
	if err != nil {
		// Includes uncertain COMMIT/unlock outcomes: never issue an accepted
		// receipt; an exact retry rechecks the durable state and coalesces safely.
		return false, err
	}
	if refusal != nil {
		return false, refusal
	}
	return coalesced, nil
}
