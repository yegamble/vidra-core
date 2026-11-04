//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogBootstrapQueriesRetainOriginalTupleAndIndependentDirtyIntent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	q := sqlcgen.New(tx)
	first, second, receiver, dirty := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_dirty(id,scope) VALUES ($1,'local')`, dirty); err != nil {
		t.Fatal(err)
	}
	row, err := q.ReadCatalogBootstrapState(ctx, first)
	if err != nil || row.CurrentEpoch.Valid || row.RequestedEpoch.Valid {
		t.Fatalf("uninitialized bootstrap evidence: %+v/%v", row, err)
	}
	if err := q.InsertCatalogEpoch(ctx, sqlcgen.InsertCatalogEpochParams{Epoch: first,
		ReceiverPredecessor: pgtype.UUID{Bytes: receiver, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := q.InitializeCatalogState(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET allocated_generation=7,force_full_after_generation=4 WHERE epoch=$1`, first); err != nil {
		t.Fatal(err)
	}
	row, err = q.ReadCatalogBootstrapState(ctx, first)
	if err != nil || !row.CurrentEpoch.Valid || uuid.UUID(row.CurrentEpoch.Bytes) != first ||
		!row.RequestedEpoch.Valid || uuid.UUID(row.RequestedEpoch.Bytes) != first || row.SourcePredecessor.Valid ||
		!row.ReceiverPredecessor.Valid || uuid.UUID(row.ReceiverPredecessor.Bytes) != receiver {
		t.Fatalf("progress erased original retry tuple: %+v/%v", row, err)
	}
	row, err = q.ReadCatalogBootstrapState(ctx, second)
	if err != nil || !row.CurrentEpoch.Valid || row.RequestedEpoch.Valid {
		t.Fatalf("unused new epoch was not distinguished: %+v/%v", row, err)
	}
	full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: first, Generation: 7}, time.Now().UTC(),
		catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes,
			MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}, func(catalog.FrozenPage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	end, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: first, Generation: 7,
		PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount,
		MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
		EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET verified_generation=7,verified_at=now(),force_full=false WHERE epoch=$1`, first); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCatalogEpoch(ctx, sqlcgen.InsertCatalogEpochParams{Epoch: second,
		SourcePredecessor: pgtype.UUID{Bytes: first, Valid: true}, ReceiverPredecessor: pgtype.UUID{Bytes: first, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if n, err := q.DeleteCatalogCurrentState(ctx, first); err != nil || n != 1 {
		t.Fatalf("current authority reset failed: %d/%v", n, err)
	}
	if err := q.InitializeCatalogState(ctx, second); err != nil {
		t.Fatal(err)
	}
	if n, err := q.DeleteCatalogCurrentState(ctx, first); err != nil || n != 0 {
		t.Fatalf("stale reset erased newer epoch: %d/%v", n, err)
	}
	row, err = q.ReadCatalogBootstrapState(ctx, first)
	if err != nil || uuid.UUID(row.CurrentEpoch.Bytes) != second || !row.RequestedEpoch.Valid {
		t.Fatalf("retired epoch lost durable history: %+v/%v", row, err)
	}
	var clean bool
	if err := tx.QueryRow(ctx, `SELECT allocated_generation=0 AND force_full AND force_full_after_generation=0
		AND verified_generation IS NULL AND last_error_id IS NULL
		AND NOT EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE epoch=$2)
		AND EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$3)
		AND (SELECT count(*) FROM search_catalog_epochs WHERE epoch IN ($1,$2))=2
		FROM search_catalog_state WHERE epoch=$1`, second, first, dirty).Scan(&clean); err != nil || !clean {
		t.Fatalf("bootstrap reset lost obligations/history or retained obsolete proof: %v/%v", clean, err)
	}
}
