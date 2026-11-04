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
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogCaptureFinalizationProtectsFreshnessAndPeriodicVerifiedFull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	epoch, at := uuid.New(), time.Now().UTC()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch,allocated_generation,force_full) VALUES ('local',$1,1,false)`} {
		if _, err := tx.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlcgen.New(tx)
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	insert := func(g int64, end catalog.FrozenEnd) {
		t.Helper()
		if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: g,
			PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount, CanonicalBytes: end.CanonicalBytes,
			MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
			EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
			t.Fatal(err)
		}
	}
	empty, _ := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, at, limits, func(catalog.FrozenPage) error { return nil })
	end, err := empty.Finish()
	if err != nil {
		t.Fatal(err)
	}
	insert(1, end)
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_snapshots SET pending=false;
		UPDATE search_catalog_state SET verified_generation=1,verified_at=now();
		INSERT INTO search_catalog_dirty(scope) VALUES ('local')`); err != nil {
		t.Fatal(err)
	}
	advance := func(g int64, dirty bool) (int64, error) {
		return q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{Epoch: epoch, ExpectedAllocatedGeneration: g, Dirty: dirty})
	}
	if g, err := advance(1, true); err != nil || g != 2 {
		t.Fatalf("allocate dirty full: %d/%v", g, err)
	}
	if _, err := advance(1, true); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale allocator accepted: %v", err)
	}
	full, _ := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 2}, at, limits, func(page catalog.FrozenPage) error {
		return q.InsertCatalogCapturePage(ctx, sqlcgen.InsertCatalogCapturePageParams{Epoch: epoch, Generation: 2,
			PageIndex: page.PageIndex, EventID: page.EventID, RequestBody: page.RequestBody,
			DocumentCount: page.DocumentCount, CanonicalBytes: page.CanonicalBytes, ContentSha256: page.ContentSHA256})
	})
	for range 2 {
		if err := full.Add(searchevents.VideoDoc{ID: uuid.New(), Kind: "local", OwnerID: uuid.New(), ChannelID: uuid.New(),
			Privacy: "public", State: "published", CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	end, err = full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	insert(2, end)
	if _, err := q.AckCatalogVisibleDirty(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var force bool
	var after int64
	if err := tx.QueryRow(ctx, `SELECT force_full,force_full_after_generation FROM search_catalog_state`).Scan(&force, &after); err != nil || !force || after != 1 {
		t.Fatalf("dirty ACK reopened old verified full: %v/%d, %v", force, after, err)
	}
	if _, err := advance(2, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pending observation was not coalesced: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE search_catalog_snapshots SET pending=false;
		UPDATE search_catalog_state SET verified_generation=2,verified_at=now(),force_full=false`); err != nil {
		t.Fatal(err)
	}
	if g, err := advance(2, false); err != nil || g != 3 {
		t.Fatalf("periodic allocation: %d/%v", g, err)
	}
	if err := tx.QueryRow(ctx, `SELECT force_full,force_full_after_generation FROM search_catalog_state`).Scan(&force, &after); err != nil || force || after != 1 {
		t.Fatalf("no-dirty periodic full invalidated verified authority: %v/%d, %v", force, after, err)
	}
}
