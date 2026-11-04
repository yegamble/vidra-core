//go:build integration

package store

import (
	"bytes"
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

func TestCatalogDeliveryReadReturnsExactCurrentFrozenWorkAndProof(t *testing.T) {
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
	if _, err := q.ReadCatalogDeliveryState(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("uninitialized source produced delivery authority: %v", err)
	}
	epoch, predecessor := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_epochs(scope,epoch,receiver_predecessor) VALUES ('local',$1,$2)`, epoch, predecessor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
		t.Fatal(err)
	}
	row, err := q.ReadCatalogDeliveryState(ctx)
	if err != nil || row.Epoch != epoch || row.AllocatedGeneration != 0 || !row.ForceFull || row.PendingGeneration != nil || row.VerifiedGeneration != nil || !row.ReceiverPredecessor.Valid || uuid.UUID(row.ReceiverPredecessor.Bytes) != predecessor {
		t.Fatalf("uninitialized observation evidence: %+v/%v", row, err)
	}
	if _, err := q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{Epoch: epoch}); err != nil {
		t.Fatal(err)
	}
	var frozen []byte
	full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, time.Now().UTC(),
		catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes,
			MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}, func(p catalog.FrozenPage) error {
			frozen = p.RequestBody
			return q.InsertCatalogCapturePage(ctx, sqlcgen.InsertCatalogCapturePageParams{Epoch: epoch, Generation: 1,
				PageIndex: p.PageIndex, EventID: p.EventID, RequestBody: p.RequestBody, DocumentCount: p.DocumentCount,
				CanonicalBytes: p.CanonicalBytes, ContentSha256: p.ContentSHA256})
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Add(searchevents.VideoDoc{ID: uuid.New(), Kind: "local", OwnerID: uuid.New(), ChannelID: uuid.New(),
		Privacy: "public", State: "published", Title: "<frozen>", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	end, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: 1,
		PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount, CanonicalBytes: end.CanonicalBytes,
		MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256,
		EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
		t.Fatal(err)
	}
	row, err = q.ReadCatalogDeliveryState(ctx)
	if err != nil || row.PendingGeneration == nil || *row.PendingGeneration != 1 || row.VerifiedGeneration != nil || row.PendingPagesSha256 == nil || *row.PendingPagesSha256 != end.Manifest.PagesSHA256 || !bytes.Equal(row.PendingEndRequestBody, end.RequestBody) {
		t.Fatalf("pending manifest/body changed: %+v/%v", row, err)
	}
	p := sqlcgen.ReadCatalogDeliveryPageParams{Epoch: epoch, Generation: 1}
	got, err := q.ReadCatalogDeliveryPage(ctx, p)
	if err != nil || !bytes.Equal(got, frozen) {
		t.Fatalf("page bytes were re-encoded or omitted: %v", err)
	}
	p.Epoch = uuid.New()
	if _, err := q.ReadCatalogDeliveryPage(ctx, p); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign epoch returned frozen work: %v", err)
	}
	for _, statement := range []string{`UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`,
		`UPDATE search_catalog_state SET verified_generation=1,verified_at=now() WHERE epoch=$1`} {
		if _, err := tx.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	row, err = q.ReadCatalogDeliveryState(ctx)
	if err != nil || row.PendingGeneration != nil || row.VerifiedGeneration == nil || *row.VerifiedGeneration != 1 || row.VerifiedMembershipSha256 == nil || *row.VerifiedMembershipSha256 != end.Manifest.MembershipSHA256 {
		t.Fatalf("verified proof missing or still pending: %+v/%v", row, err)
	}
	p.Epoch = epoch
	if _, err := q.ReadCatalogDeliveryPage(ctx, p); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("completed page was offered for redelivery: %v", err)
	}
}
