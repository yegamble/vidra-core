//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCatalogCurrentErrorRequiresPairedNonzeroEvidence(t *testing.T) {
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
	epoch, evidence := uuid.New(), uuid.New()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1)`} {
		if _, err := tx.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`UPDATE search_catalog_state SET last_error_code='delivery_failed'`,
		`UPDATE search_catalog_state SET last_error_id=uuid_generate_v4()`,
		`UPDATE search_catalog_state SET last_error_code='delivery_failed',last_error_id='00000000-0000-0000-0000-000000000000'`,
	} {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, rejected := sp.Exec(ctx, statement)
		_ = sp.Rollback(ctx)
		if rejected == nil {
			t.Errorf("accepted unpaired/zero error evidence: %s", statement)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET last_error_code='delivery_failed',last_error_id=$1`, evidence); err != nil {
		t.Fatal(err)
	}
	var got uuid.UUID
	var code string
	if err := tx.QueryRow(ctx, `SELECT last_error_code,last_error_id FROM search_catalog_state WHERE scope='local'`).Scan(&code, &got); err != nil || code != "delivery_failed" || got != evidence {
		t.Fatalf("current error evidence changed: %s/%s, %v", code, got, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET last_error_code=NULL,last_error_id=NULL`); err != nil {
		t.Fatal(err)
	}
}
