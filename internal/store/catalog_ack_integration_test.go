//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogFullACKRequiresCurrentManifestForceAndErrorEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	epoch, oldError, newError := uuid.New(), uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_state(scope,epoch,allocated_generation,force_full,last_error_code,last_error_id)
		VALUES ('local',$1,1,false,'delivery_failed',$2)`, epoch, oldError); err != nil {
		t.Fatal(err)
	}
	q := sqlcgen.New(tx)
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	insert := func(g int64) catalog.Manifest {
		t.Helper()
		full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: catalog.Generation(g)}, time.Now().UTC(), limits, func(catalog.FrozenPage) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		end, err := full.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: g,
			PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount, CanonicalBytes: end.CanonicalBytes,
			MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
			EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
			t.Fatal(err)
		}
		return end.Manifest
	}
	manifest := insert(1)
	p := sqlcgen.AcknowledgeCatalogFullParams{Epoch: epoch, Generation: 1,
		PageCount: manifest.PageCount, DocumentCount: manifest.DocumentCount,
		MembershipSha256: manifest.MembershipSHA256, PagesSha256: manifest.PagesSHA256,
		ExpectedErrorID: pgtype.UUID{Bytes: oldError, Valid: true}}
	ack := func(p sqlcgen.AcknowledgeCatalogFullParams, want int64) {
		t.Helper()
		if n, err := q.AcknowledgeCatalogFull(ctx, p); err != nil || n != want {
			t.Fatalf("ACK changed %d rows, want %d: %v", n, want, err)
		}
	}
	wrong := p
	wrong.PagesSha256 = "0000000000000000000000000000000000000000000000000000000000000000"
	ack(wrong, 0)
	// Repeating the same code still creates distinct current error evidence.
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET last_error_id=$1 WHERE epoch=$2`, newError, epoch); err != nil {
		t.Fatal(err)
	}
	ack(p, 0)
	p.ExpectedErrorID = pgtype.UUID{Bytes: newError, Valid: true}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET force_full=true,force_full_after_generation=1 WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	ack(p, 0)
	p.ExpectedForceFull, p.ExpectedForceFullAfterGeneration = true, 1
	ack(p, 1)
	ack(p, 0) // completed is not pending and cannot clear later evidence.
	var preserved bool
	if err := tx.QueryRow(ctx, `SELECT verified_generation=1 AND force_full
		AND force_full_after_generation=1 AND last_error_code IS NULL AND last_error_id IS NULL
		AND NOT EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE epoch=$1 AND pending)
		FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("old FULL cleared a newer full request: %v/%v", preserved, err)
	}
	if _, err := q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{Epoch: epoch, ExpectedAllocatedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	insert(2)
	p.Generation, p.ExpectedErrorID = 2, pgtype.UUID{}
	ack(p, 1)
	if err := tx.QueryRow(ctx, `SELECT verified_generation=2 AND NOT force_full
		AND verified_at IS NOT NULL FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("fresh verified FULL failed to satisfy force threshold: %v/%v", preserved, err)
	}
}
