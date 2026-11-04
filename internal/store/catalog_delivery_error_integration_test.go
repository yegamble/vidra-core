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

func TestCatalogDeliveryErrorsRotateEvidenceAndRejectStaleWork(t *testing.T) {
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
	epoch := uuid.New()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch,allocated_generation,force_full) VALUES ('local',$1,1,false)`} {
		if _, err := tx.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlcgen.New(tx)
	full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, time.Now().UTC(),
		catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes,
			MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}, func(catalog.FrozenPage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	end, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: 1,
		PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount,
		MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
		EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
		t.Fatal(err)
	}
	p := sqlcgen.RecordCatalogDeliveryErrorParams{Epoch: epoch, Generation: 1,
		PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount,
		MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
		ErrorCode: "delivery_failed"}
	record := func(p sqlcgen.RecordCatalogDeliveryErrorParams, want int64) uuid.UUID {
		t.Helper()
		if n, err := q.RecordCatalogDeliveryError(ctx, p); err != nil || n != want {
			t.Fatalf("error changed %d rows, want %d: %v", n, want, err)
		}
		var id uuid.UUID
		var code string
		if err := tx.QueryRow(ctx, `SELECT last_error_id,last_error_code FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&id, &code); err != nil || id == uuid.Nil || code != "delivery_failed" {
			t.Fatalf("missing sanitized current error evidence: %s/%s/%v", id, code, err)
		}
		return id
	}
	first := record(p, 1)
	if got := record(p, 0); got != first {
		t.Fatal("stale NULL error evidence overwrote a newer error")
	}
	p.ExpectedErrorID = pgtype.UUID{Bytes: first, Valid: true}
	second := record(p, 1)
	if first == second {
		t.Fatal("same-code error did not rotate its evidence UUID")
	}
	if got := record(p, 0); got != second {
		t.Fatal("repeated code hid stale error evidence")
	}
	p.ExpectedErrorID = pgtype.UUID{Bytes: second, Valid: true}
	wrong := p
	wrong.Epoch = uuid.New()
	record(wrong, 0)
	wrong = p
	wrong.PagesSha256 = "0000000000000000000000000000000000000000000000000000000000000000"
	record(wrong, 0)
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET force_full=true,force_full_after_generation=1 WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	record(p, 0)
	p.ExpectedForceFull, p.ExpectedForceFullAfterGeneration = true, 1
	third := record(p, 1)
	if third == second {
		t.Fatal("new current error reused evidence UUID")
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	p.ExpectedErrorID = pgtype.UUID{Bytes: third, Valid: true}
	record(p, 0)
}
