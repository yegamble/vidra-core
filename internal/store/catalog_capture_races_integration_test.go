//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/catalog"
)

func TestCatalogCaptureRetainsPrivacyCommitFromPreBootstrapWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, channel, video, epoch := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,username,email,password_hash) VALUES ($1,$2,$3,'x');`, owner, owner.String(), owner.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, owner) }()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO channels(id,owner_id,handle,display_name) VALUES ($1,$2,$3,'channel')`, channel, owner, channel.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO videos(id,channel_id,title,privacy,state) VALUES ($1,$2,'before','public','published')`, video, channel); err != nil {
		t.Fatal(err)
	}
	var baseline []uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT array_agg(id) FROM search_catalog_dirty`).Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	writer, err := st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	var initialized bool
	if err := writer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_state)`).Scan(&initialized); err != nil || initialized {
		t.Fatalf("writer did not begin before bootstrap: %v/%v", initialized, err)
	}
	if _, err := writer.Exec(ctx, `UPDATE videos SET privacy='private' WHERE id=$1`, video); err != nil {
		t.Fatal(err)
	}
	var hint uuid.UUID
	if err := writer.QueryRow(ctx, `SELECT id FROM search_catalog_dirty WHERE NOT(id=ANY($1::uuid[]))`, baseline).Scan(&hint); err != nil {
		t.Fatalf("pre-bootstrap canonical write lacks its transactional hint: %v", err)
	}
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1)`} {
		if _, err := st.Pool.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_state WHERE epoch=$1`, epoch)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_epochs WHERE epoch=$1`, epoch)
	}()
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	if got, err := st.CaptureCatalog(ctx, epoch, limits); err != nil || got.Generation != 1 {
		t.Fatalf("capture before writer commit: %+v/%v", got, err)
	}
	contains := func(g int64) bool {
		t.Helper()
		var found bool
		if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_pages p,
			jsonb_array_elements(convert_from(p.request_body,'UTF8')::jsonb->'events'->0->'payload'->'documents') d
			WHERE p.epoch=$1 AND p.generation=$2 AND d->'video'->>'id'=$3)`, epoch, g, video.String()).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if !contains(1) {
		t.Fatal("uncommitted privacy update leaked into the earlier RR observation")
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var retained bool
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$1)`, hint).Scan(&retained); err != nil || !retained {
		t.Fatalf("late privacy commit lost its hint: %v/%v", retained, err)
	}
	// Model exact receiver verification separately from capture's local commit.
	for _, statement := range []string{`UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`,
		`UPDATE search_catalog_state SET verified_generation=1,verified_at=now(),force_full=false WHERE epoch=$1`} {
		if _, err := st.Pool.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.CaptureCatalog(ctx, epoch, limits); err != nil || got.Generation != 2 || contains(2) {
		t.Fatalf("next observation did not exclude private video: %+v/%v", got, err)
	}
	var fenced bool
	if err := st.Pool.QueryRow(ctx, `SELECT force_full AND force_full_after_generation=1
		AND NOT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$2)
		FROM search_catalog_state WHERE epoch=$1`, epoch, hint).Scan(&fenced); err != nil || !fenced {
		t.Fatalf("privacy hint ACK reopened prior FULL authority: %v/%v", fenced, err)
	}
}

func TestCatalogCaptureSerializationFailureRollsBackAllState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	worker, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	epoch, conflict, retained := uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch,force_full) VALUES ('local',$1,false)`} {
		if _, err := st.Pool.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_state WHERE epoch=$1`, epoch)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_epochs WHERE epoch=$1`, epoch)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_dirty WHERE id=ANY($1::uuid[])`, []uuid.UUID{conflict, retained})
	}()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO search_catalog_dirty(id,scope) VALUES ($1,'local'),($2,'local')`, conflict, retained); err != nil {
		t.Fatal(err)
	}
	locker, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(context.Background()) }()
	if _, err := locker.Exec(ctx, `DELETE FROM search_catalog_dirty WHERE id=$1`, conflict); err != nil {
		t.Fatal(err)
	}
	var pid int32
	if err := worker.Pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  catalog.Identity
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := worker.CaptureCatalog(ctx, epoch, catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes,
			MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages})
		done <- result{id, err}
	}()
	for {
		var waiting bool
		if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND NOT granted)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("capture failed to reach conflicting hint ACK: %+v", got)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := locker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	var pgerr *pgconn.PgError
	if got.id != (catalog.Identity{}) || !errors.As(got.err, &pgerr) || pgerr.Code != "40001" {
		t.Fatalf("serialization failure reported authority: %+v/%v", got.id, got.err)
	}
	var intact bool
	if err := st.Pool.QueryRow(ctx, `SELECT allocated_generation=0 AND NOT force_full
		AND force_full_after_generation=0
		AND NOT EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE epoch=$1)
		AND NOT EXISTS(SELECT 1 FROM search_catalog_pages WHERE epoch=$1)
		AND EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$2)
		FROM search_catalog_state WHERE epoch=$1`, epoch, retained).Scan(&intact); err != nil || !intact {
		t.Fatalf("serialization failure leaked allocation/force/payload/hint ACK: %v/%v", intact, err)
	}
}
