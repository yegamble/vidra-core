//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/catalog"
)

func TestCatalogFullControlCommitsIntentWithAuditAndRollsBackAuditFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(2))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	epoch, user := uuid.New(), seedDMUser(t, s, "catalog-admin")
	defer func() {
		for _, statement := range []string{`DELETE FROM audit_log WHERE resource_id=$1`,
			`DELETE FROM search_catalog_state WHERE epoch=$1::uuid`, `DELETE FROM search_catalog_epochs WHERE epoch=$1::uuid`} {
			if _, err := s.Pool.Exec(context.Background(), statement, epoch.String()); err != nil {
				t.Error(err)
			}
		}
		if _, err := s.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, user); err != nil {
			t.Error(err)
		}
	}()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch,force_full) VALUES ('local',$1,false)`} {
		if _, err := s.Pool.Exec(ctx, statement, epoch); err != nil {
			t.Fatal(err)
		}
	}
	actor := audit.ActorSnapshot{Kind: "user", ID: user.String(), Role: "admin"}
	r := catalog.FullRequest{Scope: "local", ExpectedEpoch: epoch}
	for _, want := range []bool{false, true} {
		if coalesced, err := s.AcceptCatalogFullRequest(ctx, r, actor); err != nil || coalesced != want {
			t.Fatalf("accepted/coalesced receipt: %v want%v/%v", coalesced, want, err)
		}
	}
	counts := func(success, conflict int) {
		t.Helper()
		var gotSuccess, gotConflict int
		if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE result='success'),
			count(*) FILTER(WHERE result='failure' AND metadata->>'outcome'='catalog_conflict')
			FROM audit_log WHERE resource_id=$1 AND action='search.catalog.full_requested'`, epoch.String()).Scan(&gotSuccess, &gotConflict); err != nil || gotSuccess != success || gotConflict != conflict {
			t.Fatalf("durable audit counts: %d/%d want%d/%d: %v", gotSuccess, gotConflict, success, conflict, err)
		}
	}
	counts(2, 0)
	r.ExpectedAllocatedGeneration = 1
	if coalesced, err := s.AcceptCatalogFullRequest(ctx, r, actor); !errors.Is(err, ErrCatalogConflict) || coalesced {
		t.Fatalf("stale allocator accepted: %v/%v", coalesced, err)
	}
	counts(2, 1)
	r.ExpectedAllocatedGeneration = 0
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET force_full=false WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	// Inject an actual INSERT failure after the business UPDATE, scoped to this
	// synthetic resource. A failed audit must leave no accepted force intent.
	constraint := "catalog_audit_fail_" + epoch.String()[:8]
	if _, err := s.Pool.Exec(ctx, `ALTER TABLE audit_log ADD CONSTRAINT `+constraint+
		` CHECK(resource_id<>'`+epoch.String()+`') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := s.Pool.Exec(context.Background(), `ALTER TABLE audit_log DROP CONSTRAINT `+constraint); err != nil {
			t.Error(err)
		}
	}()
	if coalesced, err := s.AcceptCatalogFullRequest(ctx, r, actor); err == nil || coalesced {
		t.Fatalf("failed audit returned an accepted receipt: %v/%v", coalesced, err)
	}
	var forced bool
	if err := s.Pool.QueryRow(ctx, `SELECT force_full FROM search_catalog_state WHERE epoch=$1`, epoch).Scan(&forced); err != nil || forced {
		t.Fatalf("audit failure committed intent: %v/%v", forced, err)
	}
	counts(2, 1)
}
