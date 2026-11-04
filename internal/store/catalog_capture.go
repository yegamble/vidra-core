package store

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var (
	ErrCatalogCapturePending = errors.New("catalog full already pending")
	ErrCatalogEpochChanged   = errors.New("catalog epoch or counter requires recovery")
)

// CaptureCatalog commits a frozen observation, not verified read authority.
// It holds one reserved connection and RR snapshot only for local SQL/framing.
// Any error returns no identity; allocation, force, pages, header and hint ACK
// roll back together. Delivery must fetch committed bytes after this returns.
func (s *Store) CaptureCatalog(ctx context.Context, epoch uuid.UUID, limits catalog.Limits) (catalog.Identity, error) {
	var captured catalog.Identity
	err := s.WithCatalogCapture(ctx, func(tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		state, err := q.LockCatalogCaptureState(ctx)
		if err != nil {
			return err
		}
		if epoch == uuid.Nil || state.Epoch != epoch || state.AllocatedGeneration == math.MaxInt64 {
			return ErrCatalogEpochChanged
		}
		if state.Pending {
			return ErrCatalogCapturePending
		}
		generation, err := q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{
			Epoch: epoch, ExpectedAllocatedGeneration: state.AllocatedGeneration, Dirty: state.Dirty,
		})
		if err != nil {
			return err
		}
		identity := catalog.Identity{Scope: catalog.ScopeLocal, Epoch: epoch, Generation: catalog.Generation(generation)}
		full, err := catalog.NewFullBuilder(identity, time.Now().UTC(), limits, func(page catalog.FrozenPage) error {
			return q.InsertCatalogCapturePage(ctx, sqlcgen.InsertCatalogCapturePageParams{
				Epoch: epoch, Generation: generation, PageIndex: page.PageIndex, EventID: page.EventID,
				RequestBody: page.RequestBody, DocumentCount: page.DocumentCount,
				CanonicalBytes: page.CanonicalBytes, ContentSha256: page.ContentSHA256,
			})
		})
		if err != nil {
			return err
		}
		var after pgtype.UUID // NULL is a real first page; zero IDs must fail preflight.
		for {
			rows, err := q.ListCatalogSourcePage(ctx, sqlcgen.ListCatalogSourcePageParams{After: after, PageSize: 200})
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				if err := full.Add(searchevents.CatalogVideoDoc(row)); err != nil {
					return err
				}
			}
			after = pgtype.UUID{Bytes: rows[len(rows)-1].ID, Valid: true}
		}
		end, err := full.Finish()
		if err != nil {
			return err
		}
		if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{
			Epoch: epoch, Generation: generation, PageCount: end.Manifest.PageCount,
			DocumentCount: end.Manifest.DocumentCount, CanonicalBytes: end.CanonicalBytes,
			MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
			EndEventID: end.EventID, EndRequestBody: end.RequestBody,
		}); err != nil {
			return err
		}
		acked, err := q.AckCatalogVisibleDirty(ctx)
		if err != nil {
			return err
		}
		if state.Dirty != (acked > 0) {
			return errors.New("catalog dirty membership changed within capture snapshot")
		}
		captured = identity
		return nil
	})
	if err != nil {
		return catalog.Identity{}, err
	}
	return captured, nil
}
