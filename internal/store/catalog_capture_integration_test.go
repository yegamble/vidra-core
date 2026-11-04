//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
)

func TestCaptureCatalogCommitsExactFullAndRollsBackQuotaAndCommitFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, channel, epoch := uuid.New(), uuid.New(), uuid.New()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,username,email,password_hash) VALUES ($1,$2,$3,'x')`, owner, owner.String(), owner.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, owner) }()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO channels(id,owner_id,handle,display_name) VALUES ($1,$2,$3,'channel')`, channel, owner, channel.String()); err != nil {
		t.Fatal(err)
	}
	var own []uuid.UUID
	if err := st.Pool.QueryRow(ctx, `WITH seeded AS (
		INSERT INTO videos(channel_id,title,privacy,state)
		SELECT $1,'capture'||n::text,'public','published' FROM generate_series(1,201) n RETURNING id)
		SELECT array_agg(id) FROM seeded`, channel).Scan(&own); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_state WHERE epoch=$1`, epoch)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM search_catalog_epochs WHERE epoch=$1`, epoch)
	}()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO search_catalog_state(scope,epoch,force_full) VALUES ('local',$1,false)`, epoch); err != nil {
		t.Fatal(err)
	}
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	var initialHints []uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT array_agg(id) FROM search_catalog_dirty`).Scan(&initialHints); err != nil || len(initialHints) == 0 {
		t.Fatalf("missing initial transactional hints: %v", err)
	}
	id, err := st.CaptureCatalog(ctx, epoch, limits)
	if err != nil || id.Epoch != epoch || id.Generation != 1 {
		t.Fatalf("capture did not commit: %+v, %v", id, err)
	}
	var members []uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT array_agg((d->'video'->>'id')::uuid)
		FROM search_catalog_pages p,
		jsonb_array_elements(convert_from(p.request_body,'UTF8')::jsonb->'events'->0->'payload'->'documents') d
		WHERE p.epoch=$1`, epoch).Scan(&members); err != nil {
		t.Fatal(err)
	}
	seen := make(map[uuid.UUID]bool)
	for _, member := range members {
		seen[member] = true
	}
	for _, member := range own {
		if !seen[member] {
			t.Fatalf("capture omitted paginated canonical member %s", member)
		}
	}
	var exact bool
	if err := st.Pool.QueryRow(ctx, `SELECT s.allocated_generation=1 AND s.force_full
		AND s.force_full_after_generation=0 AND h.document_count=$2
		AND NOT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=ANY($3::uuid[]))
		FROM search_catalog_state s JOIN search_catalog_snapshots h USING(scope,epoch)
		WHERE s.epoch=$1 AND h.generation=1`, epoch, int64(len(seen)), initialHints).Scan(&exact); err != nil || !exact || len(seen) != len(members) {
		t.Fatalf("inexact committed capture: %v, %v", exact, err)
	}
	if _, err := st.CaptureCatalog(ctx, epoch, limits); !errors.Is(err, ErrCatalogCapturePending) {
		t.Fatalf("pending capture not coalesced: %v", err)
	}
	// Model verified G1 before the next dirty capture; actual ACK is separate.
	for _, statement := range []string{`UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`,
		`UPDATE search_catalog_state SET verified_generation=1,verified_at=now(),force_full=false WHERE epoch=$1`} {
		if _, err := st.Pool.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET title='changed' WHERE id=$1`, own[0]); err != nil {
		t.Fatal(err)
	}
	var dirty []uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT array_agg(id) FROM search_catalog_dirty`).Scan(&dirty); err != nil || len(dirty) == 0 {
		t.Fatalf("missing transactional dirty hint: %v", err)
	}
	unchanged := func() {
		t.Helper()
		var intact bool
		if err := st.Pool.QueryRow(ctx, `SELECT allocated_generation=1 AND NOT force_full
			AND force_full_after_generation=0
			AND (SELECT count(*) FROM search_catalog_snapshots WHERE epoch=$1)=1
			AND NOT EXISTS(SELECT 1 FROM search_catalog_pages WHERE epoch=$1 AND generation<>1)
			AND (SELECT count(*) FROM search_catalog_dirty WHERE id=ANY($2::uuid[]))=$3::bigint
			FROM search_catalog_state WHERE epoch=$1`, epoch, dirty, int64(len(dirty))).Scan(&intact); err != nil || !intact {
			t.Fatalf("failed capture leaked allocation/force/header/ACK: %v, %v", intact, err)
		}
	}
	limits.MaxSnapshotBytes = 1
	if got, err := st.CaptureCatalog(ctx, epoch, limits); !errors.Is(err, catalog.ErrQuotaExceeded) || got.Generation != 0 {
		t.Fatalf("over-quota capture did not fail closed: %+v, %v", got, err)
	}
	unchanged()
	limits.MaxSnapshotBytes = catalog.MaxSnapshotBytes
	// A deferred FK fails COMMIT after pages, header and dirty ACK have executed.
	// It proves rollback without adding a production fault-injection hook.
	if _, err := st.Pool.Exec(ctx, `CREATE TABLE catalog_capture_commit_guard(scope text,generation bigint,PRIMARY KEY(scope,generation));
		INSERT INTO catalog_capture_commit_guard VALUES ('local',1);
		ALTER TABLE search_catalog_state ADD CONSTRAINT catalog_capture_commit_guard_fk
		FOREIGN KEY(scope,allocated_generation) REFERENCES catalog_capture_commit_guard(scope,generation) DEFERRABLE INITIALLY DEFERRED`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `ALTER TABLE search_catalog_state DROP CONSTRAINT IF EXISTS catalog_capture_commit_guard_fk;
			DROP TABLE IF EXISTS catalog_capture_commit_guard`)
	}()
	if got, err := st.CaptureCatalog(ctx, epoch, limits); err == nil || got.Generation != 0 {
		t.Fatalf("deferred commit failure reported success: %+v, %v", got, err)
	}
	unchanged()
}
