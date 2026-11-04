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

func TestCatalogFullRequestRequiresCurrentAllocatorAndNewerVerifiedFull(t *testing.T) {
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
	epoch, errorID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1);
		INSERT INTO search_catalog_state(scope,epoch,force_full,last_error_code,last_error_id)
		VALUES ('local',$1,false,'delivery_failed',$2)`, epoch, errorID); err != nil {
		t.Fatal(err)
	}
	q := sqlcgen.New(tx)
	p := sqlcgen.RequestCatalogFullParams{Epoch: epoch}
	request := func(p sqlcgen.RequestCatalogFullParams, want int64) {
		t.Helper()
		if n, err := q.RequestCatalogFull(ctx, p); err != nil || n != want {
			t.Fatalf("full request changed %d rows, want %d: %v", n, want, err)
		}
	}
	wrong := p
	wrong.Epoch = uuid.New()
	request(wrong, 0)
	wrong = p
	wrong.ExpectedAllocatedGeneration = 1
	request(wrong, 0)
	request(p, 1) // Allocator zero is valid before the first capture.
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes,
		MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	freeze := func(g int64) catalog.Manifest {
		t.Helper()
		if _, err := q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{Epoch: epoch, ExpectedAllocatedGeneration: g - 1}); err != nil {
			t.Fatal(err)
		}
		full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: catalog.Generation(g)}, time.Now().UTC(), limits, func(catalog.FrozenPage) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		end, err := full.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: g,
			MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
			EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
			t.Fatal(err)
		}
		return end.Manifest
	}
	manifest := freeze(1)
	p.ExpectedAllocatedGeneration = 1
	request(p, 1) // A pending frozen G1 cannot satisfy this newer request.
	request(p, 1) // Exact retries coalesce without replacing pending bytes.
	wrong.ExpectedAllocatedGeneration = 0
	request(wrong, 0)
	var intact bool
	if err := tx.QueryRow(ctx, `SELECT force_full AND force_full_after_generation=1
		AND last_error_code='delivery_failed' AND last_error_id=$2
		AND EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE epoch=$1 AND generation=1 AND pending)
		FROM search_catalog_state WHERE epoch=$1`, epoch, errorID).Scan(&intact); err != nil || !intact {
		t.Fatalf("request lost pending/error evidence: %v/%v", intact, err)
	}
	ack := sqlcgen.AcknowledgeCatalogFullParams{Epoch: epoch, Generation: 1, ExpectedForceFull: true,
		ExpectedForceFullAfterGeneration: 1, ExpectedErrorID: pgtype.UUID{Bytes: errorID, Valid: true},
		MembershipSha256: manifest.MembershipSHA256, PagesSha256: manifest.PagesSHA256}
	if n, err := q.AcknowledgeCatalogFull(ctx, ack); err != nil || n != 1 {
		t.Fatalf("ACK old pending: %d/%v", n, err)
	}
	if err := tx.QueryRow(ctx, `SELECT verified_generation=1 AND force_full AND force_full_after_generation=1
		FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&intact); err != nil || !intact {
		t.Fatalf("old full satisfied newer request: %v/%v", intact, err)
	}
	freeze(2)
	ack.Generation, ack.ExpectedErrorID = 2, pgtype.UUID{}
	if n, err := q.AcknowledgeCatalogFull(ctx, ack); err != nil || n != 1 {
		t.Fatalf("ACK fresh pending: %d/%v", n, err)
	}
	if err := tx.QueryRow(ctx, `SELECT verified_generation=2 AND NOT force_full
		AND force_full_after_generation=1 FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&intact); err != nil || !intact {
		t.Fatalf("newer verified full did not satisfy request: %v/%v", intact, err)
	}
}
