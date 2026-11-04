package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var errCatalogReadProof = errors.New("catalog source read proof is invalid")

// LoadCatalogReadProof completes QueryRow/Scan before returning. A nil UUID
// means anonymous; a nonzero UUID is the actual authenticated viewer principal.
// No connection, cursor, transaction or lock survives into receiver HTTP work.
func (s *Store) LoadCatalogReadProof(ctx context.Context, viewerID uuid.UUID) (catalog.ReadProof, error) {
	r, err := s.Queries().ReadCatalogPublicProof(ctx, pgtype.UUID{Bytes: viewerID, Valid: viewerID != uuid.Nil})
	if err != nil {
		return catalog.ReadProof{}, err
	}
	return mapCatalogReadProof(r)
}

func mapCatalogReadProof(r sqlcgen.ReadCatalogPublicProofRow) (catalog.ReadProof, error) {
	p := catalog.FullProof{Identity: catalog.Identity{Scope: catalog.ScopeLocal, Epoch: r.Epoch, Generation: catalog.Generation(r.Generation)},
		Manifest: catalog.Manifest{PageCount: r.PageCount, DocumentCount: r.DocumentCount, MembershipSHA256: r.MembershipSha256, PagesSHA256: r.PagesSha256}}
	if p.Identity.Validate() != nil || p.Manifest.Validate() != nil || r.PageCount > catalog.MaxPages || r.DocumentCount > catalog.MaxDocuments {
		return catalog.ReadProof{}, errCatalogReadProof
	}
	return catalog.ReadProof{Full: p, HideSensitive: r.HideSensitive}, nil
}
