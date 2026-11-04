//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
)

func TestLoadCatalogReadProofReleasesSingleConnectionAndRefusesAbsentState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, viewer := range []uuid.UUID{uuid.Nil, uuid.New()} {
		if proof, err := s.LoadCatalogReadProof(ctx, viewer); !errors.Is(err, pgx.ErrNoRows) || proof != (catalog.ReadProof{}) {
			t.Fatalf("absent source state manufactured proof: %+v/%v", proof, err)
		}
		var one int
		if err := s.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatalf("read proof retained the sole connection: %d/%v", one, err)
		}
	}
}
