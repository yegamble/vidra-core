package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var errCatalogWork = errors.New("catalog delivery state is incomplete or inconsistent")

// LoadCatalogWork finishes QueryRow/Scan before returning owned delivery data.
// No connection, Rows cursor or transaction survives into receiver HTTP work.
func (s *Store) LoadCatalogWork(ctx context.Context) (catalog.SourceWork, error) {
	r, err := s.Queries().ReadCatalogDeliveryState(ctx)
	if err != nil {
		return catalog.SourceWork{}, err
	}
	return mapCatalogWork(r)
}

func mapCatalogWork(r sqlcgen.ReadCatalogDeliveryStateRow) (catalog.SourceWork, error) {
	var w catalog.SourceWork
	if r.Epoch == uuid.Nil || r.AllocatedGeneration < 0 || r.ForceFullAfterGeneration < 0 || r.ForceFullAfterGeneration > r.AllocatedGeneration || (r.LastErrorCode != nil) != r.LastErrorID.Valid {
		return catalog.SourceWork{}, errCatalogWork
	}
	w.Evidence.Epoch, w.Evidence.AllocatedGeneration = r.Epoch, r.AllocatedGeneration
	w.ForceFull, w.ForceFullAfterGeneration = r.ForceFull, r.ForceFullAfterGeneration
	if r.LastErrorCode != nil {
		switch *r.LastErrorCode {
		case "quota_exceeded", "scalar_limit_exceeded", "capture_failed", "delivery_failed", "verification_failed", "epoch_conflict":
		default:
			return catalog.SourceWork{}, errCatalogWork
		}
		id := uuid.UUID(r.LastErrorID.Bytes)
		if id == uuid.Nil {
			return catalog.SourceWork{}, errCatalogWork
		}
		w.LastErrorCode, w.LastErrorID = *r.LastErrorCode, &id
	}
	if r.ReceiverPredecessor.Valid {
		id := uuid.UUID(r.ReceiverPredecessor.Bytes)
		if id == uuid.Nil || id == r.Epoch {
			return catalog.SourceWork{}, errCatalogWork
		}
		w.ReceiverPredecessor = &id
	}
	var err error
	w.Evidence.Verified, w.VerifiedAt, err = mapCatalogProof(r.Epoch, r.VerifiedGeneration, r.VerifiedPageCount, r.VerifiedDocumentCount, r.VerifiedMembershipSha256, r.VerifiedPagesSha256, r.VerifiedAt)
	if err != nil {
		return catalog.SourceWork{}, err
	}
	w.Evidence.Pending, w.PendingCapturedAt, err = mapCatalogProof(r.Epoch, r.PendingGeneration, r.PendingPageCount, r.PendingDocumentCount, r.PendingMembershipSha256, r.PendingPagesSha256, r.PendingCapturedAt)
	if err != nil {
		return catalog.SourceWork{}, err
	}
	for _, p := range []*catalog.FullProof{w.Evidence.Verified, w.Evidence.Pending} {
		if p != nil && int64(p.Identity.Generation) > r.AllocatedGeneration {
			return catalog.SourceWork{}, errCatalogWork
		}
	}
	if p := w.Evidence.Pending; p != nil {
		if int64(p.Identity.Generation) != r.AllocatedGeneration || (w.Evidence.Verified != nil && p.Identity.Generation <= w.Evidence.Verified.Identity.Generation) || r.PendingCanonicalBytes == nil {
			return catalog.SourceWork{}, errCatalogWork
		}
		b := *r.PendingCanonicalBytes
		if b < 0 || b > catalog.MaxSnapshotBytes || b > int64(p.Manifest.PageCount)*catalog.MaxRequestBytes || (p.Manifest.DocumentCount == 0) != (b == 0) || len(r.PendingEndRequestBody) == 0 || len(r.PendingEndRequestBody) > catalog.MaxRequestBytes {
			return catalog.SourceWork{}, errCatalogWork
		}
		w.PendingCanonicalBytes, w.PendingEndRequestBody = b, append([]byte(nil), r.PendingEndRequestBody...)
	} else if r.PendingCanonicalBytes != nil || r.PendingEndRequestBody != nil {
		return catalog.SourceWork{}, errCatalogWork
	}
	return w, nil
}

func mapCatalogProof(epoch uuid.UUID, g *int64, pages *int32, docs *int64, members, content *string, at pgtype.Timestamptz) (*catalog.FullProof, *time.Time, error) {
	if g == nil {
		if pages != nil || docs != nil || members != nil || content != nil || at.Valid {
			return nil, nil, errCatalogWork
		}
		return nil, nil, nil
	}
	if pages == nil || docs == nil || members == nil || content == nil || !at.Valid || at.Time.IsZero() || at.InfinityModifier != pgtype.Finite {
		return nil, nil, errCatalogWork
	}
	p := &catalog.FullProof{Identity: catalog.Identity{Scope: catalog.ScopeLocal, Epoch: epoch, Generation: catalog.Generation(*g)},
		Manifest: catalog.Manifest{PageCount: *pages, DocumentCount: *docs, MembershipSHA256: *members, PagesSHA256: *content}}
	if p.Identity.Validate() != nil || p.Manifest.Validate() != nil || *pages > catalog.MaxPages || *docs > catalog.MaxDocuments {
		return nil, nil, errCatalogWork
	}
	value := at.Time
	return p, &value, nil
}
