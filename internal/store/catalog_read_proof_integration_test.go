//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogReadProofIncludesStaticAndViewerObligationsInOneSnapshot(t *testing.T) {
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
	epoch, viewer, owner := uuid.New(), uuid.New(), uuid.New()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	proof := func(id pgtype.UUID, ready, hide bool) {
		t.Helper()
		row, err := q.ReadCatalogPublicProof(ctx, id)
		if !ready {
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("unsupported/unready source returned proof: %+v/%v", row, err)
			}
			return
		}
		if err != nil || row.Epoch != epoch || row.Generation != 1 || row.HideSensitive != hide || row.PageCount != 0 || row.MembershipSha256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Fatalf("inexact source proof: %+v/%v", row, err)
		}
	}
	principal := pgtype.UUID{Bytes: viewer, Valid: true}
	proof(principal, false, false)
	for _, id := range []uuid.UUID{viewer, owner} {
		exec(`INSERT INTO users(id,username,email,password_hash) VALUES ($1,$2,$3,'x')`, id, id.String(), id.String()+"@example.test")
	}
	exec(`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`, epoch)
	exec(`INSERT INTO search_catalog_state(scope,epoch,allocated_generation,force_full) VALUES ('local',$1,1,false)`, epoch)
	// Model retained exact verification; capture/HTTP proofs are separate tests.
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
	exec(`UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`, epoch)
	exec(`UPDATE search_catalog_state SET verified_generation=1,verified_at=now() WHERE epoch=$1`, epoch)
	// Isolate existing synthetic queue residue inside this rolled-back fixture.
	exec(`DELETE FROM search_catalog_dirty`)
	exec(`DELETE FROM search_outbox WHERE state IN ('pending','dead') AND event_type IN
		('video.upsert','video.suppress','channel.upsert','channel.delete','user.suppress','reconcile.begin','reconcile.page','reconcile.end')`)
	exec(`DELETE FROM instance_settings WHERE key='sensitive_content_policy'`)
	proof(principal, true, true)
	exec(`INSERT INTO search_catalog_dirty(id,scope) VALUES ($1,'local')`, epoch)
	proof(principal, false, false)
	exec(`DELETE FROM search_catalog_dirty WHERE id=$1`, epoch)
	for _, kind := range []string{"video.upsert", "video.suppress", "channel.upsert", "channel.delete",
		"user.suppress", "reconcile.begin", "reconcile.page", "reconcile.end"} {
		exec(`INSERT INTO search_outbox(event_id,event_type,payload,next_attempt_at) VALUES ($1,$2,'{}',now()+interval '1 day')`, epoch, kind)
		proof(principal, false, false)
		exec(`UPDATE search_outbox SET state='dead' WHERE event_id=$1`, epoch)
		proof(principal, false, false)
		exec(`UPDATE search_outbox SET state='delivered' WHERE event_id=$1`, epoch)
		proof(principal, true, true)
		exec(`DELETE FROM search_outbox WHERE event_id=$1`, epoch)
	}
	exec(`INSERT INTO search_outbox(event_type,payload,state) VALUES ('video.stats','{}','dead'),('future.unknown','{}','pending')`)
	proof(principal, true, true)
	exec(`UPDATE search_catalog_state SET force_full=true WHERE epoch=$1`, epoch)
	proof(principal, false, false)
	exec(`UPDATE search_catalog_state SET force_full=false,last_error_code='capture_failed',last_error_id=$2 WHERE epoch=$1`, epoch, uuid.New())
	proof(principal, false, false)
	exec(`UPDATE search_catalog_state SET last_error_code=NULL,last_error_id=NULL WHERE epoch=$1`, epoch)
	exec(`UPDATE users SET sensitive_content_policy='warn' WHERE id=$1`, viewer)
	proof(principal, true, false)
	exec(`INSERT INTO user_content_prefs(user_id,sensitive_flag_policies) VALUES ($1,'{"sex":"warn"}')`, viewer)
	proof(principal, false, false)
	proof(pgtype.UUID{}, true, true)
	exec(`DELETE FROM user_content_prefs WHERE user_id=$1`, viewer)
	for _, statement := range []string{`INSERT INTO user_blocks(blocker_id,blocked_id) VALUES ($1,$2)`,
		`INSERT INTO muted_accounts(muter_id,muted_id) VALUES ($1,$2)`} {
		exec(statement, viewer, owner)
		proof(principal, false, false)
		exec(`DELETE FROM user_blocks WHERE blocker_id IN ($1,$2)`, viewer, owner)
		exec(`DELETE FROM muted_accounts WHERE muter_id=$1`, viewer)
	}
	// Public video SQL filters outgoing blocks; an incoming messaging block
	// does not change this viewer's public catalog eligibility.
	exec(`INSERT INTO user_blocks(blocker_id,blocked_id) VALUES ($1,$2)`, owner, viewer)
	proof(principal, true, false)
	exec(`INSERT INTO instance_settings(key,value) VALUES ('sensitive_content_policy','display')`)
	proof(pgtype.UUID{}, true, false)
	exec(`UPDATE instance_settings SET value='hide' WHERE key='sensitive_content_policy'`)
	proof(pgtype.UUID{}, true, true)
	proof(pgtype.UUID{Bytes: uuid.New(), Valid: true}, false, false)
}
