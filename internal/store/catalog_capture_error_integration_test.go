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

func TestCatalogCaptureErrorCannotOverwriteNewerIntentOrPendingFull(t *testing.T) {
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
		`INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1)`} {
		if _, err := tx.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlcgen.New(tx)
	p := sqlcgen.RecordCatalogCaptureErrorParams{Epoch: epoch, ExpectedForceFull: true, ErrorCode: "quota_exceeded"}
	record := func(p sqlcgen.RecordCatalogCaptureErrorParams, want int64) uuid.UUID {
		t.Helper()
		if n, err := q.RecordCatalogCaptureError(ctx, p); err != nil || n != want {
			t.Fatalf("capture error changed %d rows, want %d: %v", n, want, err)
		}
		var id uuid.UUID
		var code string
		if err := tx.QueryRow(ctx, `SELECT last_error_id,last_error_code FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&id, &code); err != nil || id == uuid.Nil || code != "quota_exceeded" {
			t.Fatalf("capture error evidence missing: %s/%s/%v", id, code, err)
		}
		return id
	}
	first := record(p, 1)
	if record(p, 0) != first {
		t.Fatal("stale capture error erased current evidence")
	}
	p.ExpectedErrorID = pgtype.UUID{Bytes: first, Valid: true}
	second := record(p, 1)
	if second == first {
		t.Fatal("same capture error code reused its identity")
	}
	p.ExpectedErrorID = pgtype.UUID{Bytes: second, Valid: true}
	if _, err := q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{Epoch: epoch}); err != nil {
		t.Fatal(err)
	}
	record(p, 0)
	p.ExpectedAllocatedGeneration = 1
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_state SET force_full_after_generation=1 WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	record(p, 0)
	p.ExpectedForceFullAfterGeneration = 1
	third := record(p, 1)
	p.ExpectedErrorID = pgtype.UUID{Bytes: third, Valid: true}
	wrong := p
	wrong.Epoch = uuid.New()
	record(wrong, 0)
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
	if record(p, 0) != third {
		t.Fatal("late capture error overwrote current pending delivery state")
	}
}
