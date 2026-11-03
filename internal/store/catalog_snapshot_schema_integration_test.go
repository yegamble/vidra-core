//go:build integration

package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFrozenCatalogSchemaDefersPagesAndProtectsVerifiedManifest(t *testing.T) {
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
	epoch := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_epochs(scope, epoch) VALUES ('local',$1)`, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_state(scope,epoch,allocated_generation) VALUES ('local',$1,2)`, epoch); err != nil {
		t.Fatal(err)
	}
	// Storage is byte-preserving; protocol validation belongs to capture, not a
	// JSONB re-encoding that would change the frozen request representation.
	body := []byte(`{"events":[{"payload":{"text":"<&>\u2028","counter":9007199254740993}}]}`)
	digest := strings.Repeat("a", 64)
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_pages
		(scope,epoch,generation,page_index,event_id,request_body,document_count,canonical_bytes,content_sha256)
		VALUES ('local',$1,1,0,$2,$3,1,12,$4)`, epoch, uuid.New(), body, digest); err != nil {
		t.Fatal(err) // A page can precede its final manifest within the capture TX.
	}
	insert := `INSERT INTO search_catalog_snapshots(scope,epoch,generation,page_count,document_count,
		canonical_bytes,membership_sha256,pages_sha256,end_event_id,end_request_body)
		VALUES ('local',$1,$2,1,1,12,$3,$3,$4,$5)`
	if _, err := tx.Exec(ctx, insert, epoch, int64(1), digest, uuid.New(), body); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var frozen []byte
	if err := tx.QueryRow(ctx, `SELECT request_body FROM search_catalog_pages WHERE scope='local'`).Scan(&frozen); err != nil || !bytes.Equal(body, frozen) {
		t.Fatalf("frozen request bytes changed: %q, %v", frozen, err)
	}
	reject := func(statement string, args ...any) {
		t.Helper()
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, rejected := sp.Exec(ctx, statement, args...)
		_ = sp.Rollback(ctx)
		if rejected == nil {
			t.Errorf("accepted invalid frozen snapshot: %s", statement)
		}
	}
	reject(insert, epoch, int64(2), digest, uuid.New(), body) // Only one pending full.
	for _, statement := range []string{
		`UPDATE search_catalog_pages SET page_index=-1`,
		`UPDATE search_catalog_pages SET page_index=1024`,
		`UPDATE search_catalog_pages SET document_count=0`,
		`UPDATE search_catalog_pages SET request_body=''::bytea`,
		`UPDATE search_catalog_pages SET request_body=decode(repeat('61',1048577),'hex')`,
		`UPDATE search_catalog_pages SET generation=2`,
		`UPDATE search_catalog_snapshots SET canonical_bytes=536870913`,
		`UPDATE search_catalog_snapshots SET document_count=100001`,
		`UPDATE search_catalog_snapshots SET page_count=2`,
		`UPDATE search_catalog_snapshots SET pages_sha256='invalid'`,
		`UPDATE search_catalog_state SET verified_generation=2,verified_at=now()`,
	} {
		reject(statement)
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_snapshots SET pending=false;
		UPDATE search_catalog_state SET verified_generation=1,verified_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, insert, epoch, int64(2), digest, uuid.New(), body); err != nil {
		t.Fatal(err)
	}
	reject(`DELETE FROM search_catalog_snapshots WHERE generation=1`)
	if _, err := tx.Exec(ctx, `DELETE FROM search_catalog_state WHERE scope='local'`); err != nil {
		t.Fatal(err)
	}
	var payloads int64
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM search_catalog_snapshots)+
		(SELECT count(*) FROM search_catalog_pages)`).Scan(&payloads); err != nil || payloads != 0 {
		t.Fatalf("state reset left frozen payloads: %d, %v", payloads, err)
	}
}
