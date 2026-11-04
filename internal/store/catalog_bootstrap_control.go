package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var (
	ErrCatalogPreflightRequired    = errors.New("new catalog bootstrap requires fresh receiver preflight")
	ErrCatalogReceiverIncompatible = errors.New("catalog receiver preflight is incompatible")
	ErrCatalogBusy                 = errors.New("matching catalog pair does not require an epoch reset")
)

type CatalogBootstrapPreflight struct {
	Source   *catalog.SourceWork
	Receiver catalog.Status
}

// AcceptCatalogBootstrap never performs HTTP. Nil preflight permits only an
// exact active original-tuple retry; otherwise the caller fetches source and
// receiver evidence outside authority, then calls again for one fenced recheck.
func (s *Store) AcceptCatalogBootstrap(ctx context.Context, r catalog.BootstrapRequest, p *CatalogBootstrapPreflight, actor audit.ActorSnapshot) (bool, error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	var coalesced bool
	var refusal error
	err := s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.ReadCatalogBootstrapState(ctx, r.Epoch)
		if err != nil {
			return err
		}
		outcome := "accepted"
		if row.RequestedEpoch.Valid {
			coalesced = row.CurrentEpoch.Valid && uuid.UUID(row.CurrentEpoch.Bytes) == r.Epoch &&
				catalogUUID(r.ExpectedEpoch) == row.SourcePredecessor && catalogUUID(r.ExpectedReceiverEpoch) == row.ReceiverPredecessor
			if !coalesced {
				outcome, refusal = "catalog_conflict", ErrCatalogConflict
			}
		} else if catalogUUID(r.ExpectedEpoch) != row.CurrentEpoch {
			outcome, refusal = "catalog_conflict", ErrCatalogConflict
		} else {
			outcome, refusal = checkCatalogBootstrapPreflight(ctx, q, row.CurrentEpoch.Valid, r, p)
			if errors.Is(refusal, ErrCatalogPreflightRequired) {
				return refusal // Internal two-phase request, not a rejected action.
			}
		}
		ev, err := audit.CatalogBootstrapEvent(actor, r, coalesced, outcome)
		if err != nil {
			return err
		}
		if refusal == nil && !coalesced {
			if err := q.InsertCatalogEpoch(ctx, sqlcgen.InsertCatalogEpochParams{Epoch: r.Epoch,
				SourcePredecessor: catalogUUID(r.ExpectedEpoch), ReceiverPredecessor: catalogUUID(r.ExpectedReceiverEpoch)}); err != nil {
				return err
			}
			if row.CurrentEpoch.Valid {
				if n, err := q.DeleteCatalogCurrentState(ctx, uuid.UUID(row.CurrentEpoch.Bytes)); err != nil || n != 1 {
					return errors.Join(ErrCatalogConflict, err)
				}
			}
			if err := q.InitializeCatalogState(ctx, r.Epoch); err != nil {
				return err
			}
		}
		return audit.NewService(q).Record(ctx, ev)
	})
	if err != nil {
		return false, err // Includes uncertain COMMIT/unlock: no accepted receipt.
	}
	if refusal != nil {
		return false, refusal
	}
	return coalesced, nil
}

func checkCatalogBootstrapPreflight(ctx context.Context, q *sqlcgen.Queries, current bool, r catalog.BootstrapRequest, p *CatalogBootstrapPreflight) (string, error) {
	if p == nil {
		return "", ErrCatalogPreflightRequired
	}
	// Round-trip the small typed status through the strict capability validator;
	// no source connection or lock was held during its original HTTP observation.
	raw, err := json.Marshal(p.Receiver)
	if err != nil || len(raw) > catalog.MaxRequestBytes {
		return "catalog_receiver_incompatible", ErrCatalogReceiverIncompatible
	}
	if _, err := catalog.ParseStatus(raw); err != nil {
		return "catalog_receiver_incompatible", ErrCatalogReceiverIncompatible
	}
	if !reflect.DeepEqual(p.Receiver.Epoch, r.ExpectedReceiverEpoch) || (p.Receiver.Epoch != nil && *p.Receiver.Epoch == r.Epoch) {
		return "catalog_conflict", ErrCatalogConflict
	}
	var source *catalog.SourceWork
	if current {
		row, err := q.ReadCatalogDeliveryState(ctx)
		if err != nil {
			return "operation_failed", err
		}
		w, err := mapCatalogWork(row)
		if err != nil {
			return "operation_failed", err
		}
		source = &w
	}
	if !reflect.DeepEqual(source, p.Source) {
		return "catalog_conflict", ErrCatalogConflict
	}
	if source != nil {
		_, err := catalog.AssessReceiver(source.Evidence, p.Receiver)
		switch {
		case errors.Is(err, catalog.ErrReceiverEpochMismatch), errors.Is(err, catalog.ErrProducerBehindReceiver),
			errors.Is(err, catalog.ErrReceiverBehindVerified), errors.Is(err, catalog.ErrReceiverProofMismatch):
		case err != nil && !errors.Is(err, catalog.ErrPendingExpired):
			return "operation_failed", err
		default:
			return "catalog_busy", ErrCatalogBusy
		}
	}
	return "accepted", nil
}

func catalogUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}
