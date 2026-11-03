//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCatalogProducerSchemaSupportsFencesAndExactDirtyMembership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	epoch, predecessor := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_epochs(scope, epoch, receiver_predecessor)
		VALUES ('local', $1, $2)`, epoch, predecessor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_state(scope, epoch) VALUES ('local', $1)`, epoch); err != nil {
		t.Fatal(err)
	}
	var allocated int64
	var verified *int64
	var force bool
	if err := tx.QueryRow(ctx, `SELECT allocated_generation, verified_generation, force_full
		FROM search_catalog_state WHERE scope='local'`).Scan(&allocated, &verified, &force); err != nil || allocated != 0 || verified != nil || !force {
		t.Fatalf("initial producer fence %d/%v/%v: %v", allocated, verified, force, err)
	}
	for _, statement := range []string{
		`INSERT INTO search_catalog_epochs(scope, epoch) VALUES ('remote', uuid_generate_v4())`,
		`INSERT INTO search_catalog_epochs(scope, epoch) VALUES ('local', '00000000-0000-0000-0000-000000000000')`,
		`UPDATE search_catalog_epochs SET receiver_predecessor=epoch`,
		`UPDATE search_catalog_state SET epoch=uuid_generate_v4()`,
		`UPDATE search_catalog_state SET allocated_generation=-1`,
		`UPDATE search_catalog_state SET verified_generation=0, verified_at=now()`,
		`UPDATE search_catalog_state SET verified_generation=1, verified_at=now()`,
		`UPDATE search_catalog_state SET verified_at=now()`,
		`UPDATE search_catalog_state SET allocated_generation=7, verified_generation=3`,
		`INSERT INTO search_catalog_dirty(scope) VALUES ('remote')`,
	} {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, rejected := savepoint.Exec(ctx, statement)
		_ = savepoint.Rollback(ctx)
		if rejected == nil {
			t.Errorf("accepted invalid producer state: %s", statement)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET allocated_generation=7,
		verified_generation=3, verified_at=now()`); err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 3)
	for i := range ids {
		if err := tx.QueryRow(ctx, `INSERT INTO search_catalog_dirty(scope) VALUES ('local') RETURNING id`).Scan(&ids[i]); err != nil || ids[i] == uuid.Nil {
			t.Fatalf("immutable dirty identity: %s, %v", ids[i], err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM search_catalog_dirty WHERE scope='local' AND id=ANY($1::uuid[])`, ids[:2]); err != nil {
		t.Fatal(err)
	}
	var count int64
	var remaining string
	if err := tx.QueryRow(ctx, `SELECT count(*), min(id::text) FROM search_catalog_dirty WHERE scope='local'`).Scan(&count, &remaining); err != nil || count != 1 || remaining != ids[2].String() {
		t.Fatalf("exact membership acknowledgement changed retained intent: %d/%s, %v", count, remaining, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM search_catalog_state WHERE scope='local'`); err != nil {
		t.Fatal(err)
	}
	var retained bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_epochs WHERE scope='local' AND epoch=$1)`, epoch).Scan(&retained); err != nil || !retained {
		t.Fatalf("epoch history lost with mutable producer state: %v, %v", retained, err)
	}
}
