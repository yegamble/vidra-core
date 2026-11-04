package catalogsync

import (
	"context"
	"errors"
	"reflect"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchclient"
)

type EpochReceiver interface {
	CatalogStatus(context.Context) (catalog.Status, error)
	SetCatalogEpoch(context.Context, uuid.UUID, *uuid.UUID) error
}

// ApplyBootstrapEpoch applies only an initial durable operator intent. True
// means matching receiver epoch evidence, never FULL completion/read readiness.
// No source connection/transaction/lock may be held by its caller during HTTP.
func ApplyBootstrapEpoch(ctx context.Context, receiver EpochReceiver, w catalog.SourceWork, observed catalog.Status) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if w.LastErrorCode == "epoch_conflict" {
		return false, catalog.ErrEpochConflict
	}
	_, err := catalog.AssessReceiver(w.Evidence, observed)
	if err == nil {
		return true, nil // Fresh caller GET already matches source proof/counters.
	}
	if !errors.Is(err, catalog.ErrReceiverEpochMismatch) {
		return false, err
	}
	// After any capture/verification progress an epoch mismatch is a restore or
	// conflict requiring explicit new recovery, never automatic reinitialization.
	if w.Evidence.AllocatedGeneration != 0 || w.Evidence.Pending != nil || w.Evidence.Verified != nil ||
		!w.ForceFull || w.ForceFullAfterGeneration != 0 {
		return false, err
	}
	if !reflect.DeepEqual(observed.Epoch, w.ReceiverPredecessor) ||
		w.ReceiverPredecessor != nil && (*w.ReceiverPredecessor == uuid.Nil || *w.ReceiverPredecessor == w.Evidence.Epoch) {
		return false, catalog.ErrEpochConflict
	}
	putErr := receiver.SetCatalogEpoch(ctx, w.Evidence.Epoch, w.ReceiverPredecessor)
	var rejected *searchclient.HTTPError
	if errors.As(putErr, &rejected) && rejected.Status == 409 {
		// A later matching GET must not erase an observed CAS conflict. The caller
		// persists this sanitized barrier under its exact current source fence.
		return false, errors.Join(catalog.ErrEpochConflict, putErr)
	}
	if err := ctx.Err(); err != nil {
		return false, errors.Join(putErr, err)
	}
	current, err := receiver.CatalogStatus(ctx)
	if err != nil {
		return false, errors.Join(putErr, err)
	}
	// Resolve success/lost response only with independent exact assessment.
	// Matching names with highest/completed beyond allocator zero still refuse.
	if _, err := catalog.AssessReceiver(w.Evidence, current); err != nil {
		return false, errors.Join(putErr, err)
	}
	return true, nil
}
