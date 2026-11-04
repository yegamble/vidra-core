//go:build integration

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
)

func TestLoadCatalogWorkReleasesSingleConnectionAndRefusesAbsentState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if w, err := s.LoadCatalogWork(ctx); !errors.Is(err, pgx.ErrNoRows) || !reflect.DeepEqual(w, catalog.SourceWork{}) {
		t.Fatalf("absent state manufactured work: %+v/%v", w, err)
	}
	epoch := uuid.New()
	defer func() {
		for _, table := range []string{"search_catalog_state", "search_catalog_epochs"} {
			if _, err := s.Pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE epoch=$1`, epoch); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, table := range []string{"search_catalog_epochs", "search_catalog_state"} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO `+table+`(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.LoadCatalogWork(ctx)
	if err != nil || w.Evidence.Epoch != epoch || !w.ForceFull || w.Evidence.Pending != nil || w.Evidence.Verified != nil {
		t.Fatalf("initial work read failed: %+v/%v", w, err)
	}
	var one int
	if err := s.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("QueryRow/Scan retained the sole connection: %d/%v", one, err)
	}
}
