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

func TestCatalogDurableEpochConflictRequiresExplicitNewEpochAndCurrentToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	current, next, oldToken, newToken := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	user := seedDMUser(t, s, "catalog-conflict")
	defer func() {
		for _, statement := range []string{`DELETE FROM audit_log WHERE resource_id IN (SELECT unnest($1::uuid[])::text)`,
			`DELETE FROM search_catalog_state WHERE epoch=ANY($1)`, `DELETE FROM search_catalog_epochs WHERE epoch=ANY($1)`} {
			if _, err := s.Pool.Exec(context.Background(), statement, []uuid.UUID{current, next}); err != nil {
				t.Error(err)
			}
		}
		if _, err := s.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, user); err != nil {
			t.Error(err)
		}
	}()
	for _, statement := range []string{`INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`,
		`INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1)`} {
		if _, err := s.Pool.Exec(ctx, statement, current); err != nil {
			t.Fatal(err)
		}
	}
	load := func() catalog.SourceWork {
		t.Helper()
		w, err := s.LoadCatalogWork(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	w := load()
	p := &CatalogBootstrapPreflight{Source: &w, Receiver: catalog.Status{ProtocolVersion: 2, Scope: "local", Epoch: &current,
		SupportedScopes: []string{"local"}, SupportedEventTypes: []string{catalog.TypeSnapshotPage, catalog.TypeSnapshotEnd},
		HighestObservedGeneration: "0", CompletedGeneration: "0",
		Limits: catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}}}
	r := catalog.BootstrapRequest{Scope: "local", Epoch: next, ExpectedEpoch: &current, ExpectedReceiverEpoch: &current}
	actor := audit.ActorSnapshot{Kind: "user", ID: user.String(), Role: "admin"}
	if _, err := s.AcceptCatalogBootstrap(ctx, r, p, actor); !errors.Is(err, ErrCatalogBusy) {
		t.Fatalf("ordinary matching pair was reset: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET force_full=false,last_error_code='epoch_conflict',last_error_id=$1 WHERE epoch=$2`, oldToken, current); err != nil {
		t.Fatal(err)
	}
	var dirty int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM search_catalog_dirty`).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if id, err := s.CaptureCatalog(ctx, current, p.Receiver.Limits); !errors.Is(err, catalog.ErrEpochConflict) || id != (catalog.Identity{}) {
		t.Fatalf("capture crossed durable epoch conflict: %+v/%v", id, err)
	}
	var untouched bool
	if err := s.Pool.QueryRow(ctx, `SELECT allocated_generation=0 AND last_error_id=$2
		AND NOT EXISTS(SELECT 1 FROM search_catalog_snapshots WHERE epoch=$1)
		AND (SELECT count(*) FROM search_catalog_dirty)=$3 FROM search_catalog_state WHERE epoch=$1`,
		current, oldToken, dirty).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("refused capture changed state or hints: %v/%v", untouched, err)
	}
	if coalesced, err := s.AcceptCatalogFullRequest(ctx, catalog.FullRequest{Scope: "local", ExpectedEpoch: current}, actor); err == nil || coalesced {
		t.Fatalf("full request accepted across durable epoch conflict: %v/%v", coalesced, err)
	}
	var rejected bool
	if err := s.Pool.QueryRow(ctx, `SELECT NOT force_full AND last_error_id=$2 AND
		(SELECT count(*) FROM audit_log WHERE resource_id=$1::text AND result='failure'
		 AND metadata->>'outcome'='catalog_recovery_required')=1 FROM search_catalog_state WHERE epoch=$1::uuid`,
		current.String(), oldToken).Scan(&rejected); err != nil || !rejected {
		t.Fatalf("conflicted full request changed intent or lost rejection audit: %v/%v", rejected, err)
	}
	w = load()
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET last_error_id=$1 WHERE epoch=$2`, newToken, current); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptCatalogBootstrap(ctx, r, p, actor); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("stale conflict token authorized recovery: %v", err)
	}
	w = load()
	if coalesced, err := s.AcceptCatalogBootstrap(ctx, r, p, actor); err != nil || coalesced {
		t.Fatalf("explicit matching-pair conflict recovery refused: %v/%v", coalesced, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET allocated_generation=1,last_error_code='epoch_conflict',last_error_id=$1 WHERE epoch=$2`, newToken, next); err != nil {
		t.Fatal(err)
	}
	if coalesced, err := s.AcceptCatalogBootstrap(ctx, r, nil, actor); err != nil || !coalesced {
		t.Fatalf("exact retry lost original tuple: %v/%v", coalesced, err)
	}
	w = load()
	if w.Evidence.Epoch != next || w.Evidence.AllocatedGeneration != 1 || w.LastErrorCode != "epoch_conflict" || w.LastErrorID == nil || *w.LastErrorID != newToken {
		t.Fatalf("coalesced retry cleared conflict/progress: %+v", w)
	}
}
