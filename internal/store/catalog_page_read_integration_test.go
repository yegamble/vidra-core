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

func TestLoadCatalogPageOwnsFrozenBytesAndReleasesSingleConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := catalog.Identity{Scope: "local", Epoch: uuid.New(), Generation: 1}
	defer func() {
		for _, table := range []string{"search_catalog_state", "search_catalog_epochs"} {
			if _, err := s.Pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE epoch=$1`, id.Epoch); err != nil {
				t.Error(err)
			}
		}
	}()
	if body, err := s.LoadCatalogPage(ctx, id, 0); !errors.Is(err, pgx.ErrNoRows) || body != nil {
		t.Fatalf("absent frozen page returned bytes: %d/%v", len(body), err)
	}
	tx, err := s.Pool.Begin(ctx) // Also proves the failed read released its only connection.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch,allocated_generation) VALUES ('local',$1,1)`} {
		if _, err := tx.Exec(ctx, statement, id.Epoch); err != nil {
			t.Fatal(err)
		}
	}
	q, at := sqlcgen.New(tx), time.Now().UTC()
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	var frozen []byte
	full, err := catalog.NewFullBuilder(id, at, limits, func(p catalog.FrozenPage) error {
		frozen = bytes.Clone(p.RequestBody)
		return q.InsertCatalogCapturePage(ctx, sqlcgen.InsertCatalogCapturePageParams{Epoch: id.Epoch, Generation: 1,
			PageIndex: p.PageIndex, EventID: p.EventID, RequestBody: p.RequestBody, DocumentCount: p.DocumentCount,
			CanonicalBytes: p.CanonicalBytes, ContentSha256: p.ContentSHA256})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Add(searchevents.VideoDoc{ID: uuid.New(), OwnerID: uuid.New(), ChannelID: uuid.New(), Kind: "local",
		ChannelHandle: "channel", ChannelName: "Channel", Tags: []string{}, Title: "frozen", Privacy: "public", State: "published", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	end, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: id.Epoch, Generation: 1,
		PageCount: end.Manifest.PageCount, DocumentCount: end.Manifest.DocumentCount, CanonicalBytes: end.CanonicalBytes,
		MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256, EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	body, err := s.LoadCatalogPage(ctx, id, 0)
	if err != nil || !bytes.Equal(body, frozen) {
		t.Fatalf("frozen request changed: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `SELECT repeat('z',10000)`); err != nil || !bytes.Equal(body, frozen) {
		t.Fatalf("read retained the sole connection or borrowed its buffer: %v", err)
	}
	body[0] ^= 1
	if again, err := s.LoadCatalogPage(ctx, id, 0); err != nil || !bytes.Equal(again, frozen) {
		t.Fatalf("caller changed immutable stored bytes: %v", err)
	}
	for _, index := range []int32{-1, 1, catalog.MaxPages} {
		if body, err := s.LoadCatalogPage(ctx, id, index); err == nil || body != nil {
			t.Fatalf("invalid/missing index returned body: %d/%v", index, err)
		}
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`, id.Epoch); err != nil {
		t.Fatal(err)
	}
	if body, err := s.LoadCatalogPage(ctx, id, 0); !errors.Is(err, pgx.ErrNoRows) || body != nil {
		t.Fatalf("completed payload remained deliverable: %d/%v", len(body), err)
	}
}
