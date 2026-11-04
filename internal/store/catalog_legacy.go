package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ConvertLegacyCatalog replaces exact legacy catalog rows with one durable
// dirty obligation. Payloads and retry times cannot establish current truth.
func (s *Store) ConvertLegacyCatalog(ctx context.Context, batchSize int32) (int64, error) {
	if batchSize <= 0 || batchSize > 1000 {
		return 0, fmt.Errorf("catalog conversion batch must be 1..1000")
	}
	var retired int64
	err := s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		var err error
		retired, err = sqlcgen.New(tx).ConvertLegacyCatalog(ctx, batchSize)
		return err
	})
	if err != nil {
		return 0, err
	}
	return retired, nil
}
