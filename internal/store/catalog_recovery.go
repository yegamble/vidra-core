package store

import (
	"context"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var ErrCatalogRecoveryNotRequired = errors.New("catalog receiver lacks behind-verified recovery evidence")

// RequireCatalogRecoveryFull commits a fresh-full barrier before recovery drain.
// Its caller observed the receiver outside source authority. After success it
// must reload Work; ACK of any generation <= the threshold preserves SQL fallback.
func (s *Store) RequireCatalogRecoveryFull(ctx context.Context, w catalog.SourceWork, receiver catalog.Status) (bool, error) {
	if w.LastErrorCode == "epoch_conflict" {
		return false, catalog.ErrEpochConflict
	}
	if _, err := catalog.AssessReceiver(w.Evidence, receiver); !errors.Is(err, catalog.ErrReceiverBehindVerified) {
		return false, errors.Join(ErrCatalogRecoveryNotRequired, err)
	}
	var changed bool
	err := s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		row, err := q.ReadCatalogDeliveryState(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // Bootstrap/retirement replaced this observed source work.
		}
		if err != nil {
			return err
		}
		current, err := mapCatalogWork(row)
		if err != nil {
			return err
		}
		if current.LastErrorCode == "epoch_conflict" {
			return catalog.ErrEpochConflict
		}
		if !reflect.DeepEqual(current, w) {
			return nil // Preserve a newer force, allocation, proof or error token.
		}
		n, err := q.RequestCatalogFull(ctx, sqlcgen.RequestCatalogFullParams{Epoch: w.Evidence.Epoch,
			ExpectedAllocatedGeneration: w.Evidence.AllocatedGeneration})
		changed = n == 1
		return err
	})
	if err != nil {
		return false, err // Reload after uncertain COMMIT/unlock; never assume success.
	}
	return changed, nil
}
