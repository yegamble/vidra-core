//go:build integration

package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogCaptureQueriesRetainLateLowerUUIDAndMetadataSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, channel, video, epoch := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO users(id,username,email,password_hash) VALUES ($1,$2,$3,'x')`, []any{owner, owner.String(), owner.String() + "@example.test"}},
		{`INSERT INTO channels(id,owner_id,handle,display_name) VALUES ($1,$2,$3,'channel')`, []any{channel, owner, channel.String()}},
		{`INSERT INTO videos(id,channel_id,title,privacy,state) VALUES ($1,$2,'before','public','published')`, []any{video, channel}},
	} {
		if _, err := st.Pool.Exec(ctx, seed.statement, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, owner) }()
	late := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_dirty WHERE id=$1`, late)
	}()
	tx, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1)`} {
		if _, err := tx.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlcgen.New(tx)
	state, err := q.LockCatalogCaptureState(ctx)
	if err != nil || state.Epoch != epoch || state.AllocatedGeneration != 0 || !state.Dirty {
		t.Fatalf("capture inputs: %+v, %v", state, err)
	}
	var visible int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM search_catalog_dirty WHERE scope='local'`).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	writer, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	if _, err := writer.Exec(ctx, `UPDATE videos SET title='after' WHERE id=$1`, video); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(ctx, `INSERT INTO search_catalog_dirty(id,scope) VALUES ($1,'local')`, late); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := tx.QueryRow(ctx, `SELECT title FROM videos WHERE id=$1`, video).Scan(&title); err != nil || title != "before" {
		t.Fatalf("metadata did not share capture snapshot: %q, %v", title, err)
	}
	var current string
	if err := st.Pool.QueryRow(ctx, `SELECT title FROM videos WHERE id=$1`, video).Scan(&current); err != nil || current != "after" {
		t.Fatalf("late metadata did not commit: %q, %v", current, err)
	}
	if deleted, err := q.AckCatalogVisibleDirty(ctx); err != nil || deleted != visible {
		t.Fatalf("acknowledged outside exact snapshot: %d/%d, %v", deleted, visible, err)
	}
	var retained bool
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$1)`, late).Scan(&retained); err != nil || !retained {
		t.Fatalf("late lower UUID was lost: %v, %v", retained, err)
	}
	at := time.Now().UTC()
	p, err := catalog.NewPageBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, 0, uuid.New(), at,
		catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Add(searchevents.VideoDoc{ID: video, Kind: "local", ChannelID: channel, OwnerID: owner,
		Title: title, Privacy: "public", State: "published", CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertCatalogCapturePage(ctx, sqlcgen.InsertCatalogCapturePageParams{Epoch: epoch, Generation: 1,
		PageIndex: page.PageIndex, EventID: page.EventID, RequestBody: page.RequestBody,
		DocumentCount: page.DocumentCount, CanonicalBytes: page.CanonicalBytes, ContentSha256: page.ContentSHA256}); err != nil {
		t.Fatal(err)
	}
	var frozen []byte
	if err := tx.QueryRow(ctx, `SELECT request_body FROM search_catalog_pages WHERE epoch=$1`, epoch).Scan(&frozen); err != nil || !bytes.Equal(frozen, page.RequestBody) {
		t.Fatalf("frozen page bytes changed: %v", err)
	}
	// Header insertion/commit and allocation/force/ACK rollback are verified by
	// the capture coordinator; this foundation leaves the deferred page in TX.
}
