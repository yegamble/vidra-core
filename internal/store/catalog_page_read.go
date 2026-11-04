package store

import (
	"bytes"
	"context"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// LoadCatalogPage materializes one exact pending request and finishes Scan
// before returning owned bytes. No connection/cursor/lock crosses receiver HTTP.
func (s *Store) LoadCatalogPage(ctx context.Context, id catalog.Identity, index int32) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if index < 0 || index >= catalog.MaxPages {
		return nil, catalog.ErrQuotaExceeded
	}
	body, err := s.Queries().ReadCatalogDeliveryPage(ctx, sqlcgen.ReadCatalogDeliveryPageParams{
		Epoch: id.Epoch, Generation: int64(id.Generation), PageIndex: index,
	})
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > catalog.MaxRequestBytes {
		return nil, catalog.ErrQuotaExceeded
	}
	return bytes.Clone(body), nil
}
