package store

import (
	"context"

	"github.com/vidra/vidra-core/internal/catalog"
)

// CatalogCaptureDemand finishes QueryRow/Scan before returning. It may become
// stale immediately; only capture's transactional membership can consume hints.
func (s *Store) CatalogCaptureDemand(ctx context.Context) (catalog.CaptureDemand, error) {
	r, err := s.Queries().ReadCatalogCaptureDemand(ctx)
	if err != nil {
		return catalog.CaptureDemand{}, err
	}
	return catalog.CaptureDemand{Dirty: r.Dirty, LegacyOutstanding: r.LegacyOutstanding}, nil
}
