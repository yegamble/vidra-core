package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var ErrCatalogPendingNotExpired = errors.New("catalog pending full lacks receiver expiry evidence")

// RetireExpiredCatalogPending preserves the epoch and allocator, and atomically
// leaves a fresh-full fence before removing expired pending payloads. The next
// CaptureCatalog must allocate a higher generation; no expired body is retried.
func (s *Store) RetireExpiredCatalogPending(ctx context.Context, w catalog.SourceWork, receiver catalog.Status) (bool, error) {
	_, err := catalog.AssessReceiver(w.Evidence, receiver)
	if !errors.Is(err, catalog.ErrPendingExpired) {
		return false, errors.Join(ErrCatalogPendingNotExpired, err)
	}
	if w.ForceFullAfterGeneration < 0 || w.ForceFullAfterGeneration > w.Evidence.AllocatedGeneration ||
		(w.LastErrorID == nil) != (w.LastErrorCode == "") || (w.LastErrorID != nil && *w.LastErrorID == uuid.Nil) {
		return false, ErrCatalogPendingNotExpired
	}
	var errorID pgtype.UUID
	if w.LastErrorID != nil {
		errorID = pgtype.UUID{Bytes: *w.LastErrorID, Valid: true}
	}
	p := w.Evidence.Pending // AssessReceiver expiry requires a valid pending proof.
	var changed int64
	err = s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		var err error
		changed, err = sqlcgen.New(tx).RetireCatalogPending(ctx, sqlcgen.RetireCatalogPendingParams{
			Epoch: p.Identity.Epoch, Generation: int64(p.Identity.Generation),
			PageCount: p.Manifest.PageCount, DocumentCount: p.Manifest.DocumentCount,
			MembershipSha256: p.Manifest.MembershipSHA256, PagesSha256: p.Manifest.PagesSHA256,
			ExpectedForceFull: w.ForceFull, ExpectedForceFullAfterGeneration: w.ForceFullAfterGeneration,
			ExpectedErrorID: errorID,
		})
		return err
	})
	if err != nil {
		return false, err // Reload after uncertain COMMIT/unlock, never infer rollback.
	}
	return changed == 1, nil
}
