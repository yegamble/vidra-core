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

func TestCatalogBootstrapControlRechecksRecoveryAndAuditsExactRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user := seedDMUser(t, s, "catalog-bootstrap")
	first, second, refused, failed := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ids := []uuid.UUID{first, second, refused, failed}
	defer func() {
		for _, statement := range []string{`DELETE FROM audit_log WHERE resource_id::uuid=ANY($1) AND resource_type='search_catalog'`,
			`DELETE FROM search_catalog_state WHERE epoch=ANY($1)`, `DELETE FROM search_catalog_epochs WHERE epoch=ANY($1)`} {
			if _, err := s.Pool.Exec(context.Background(), statement, ids); err != nil {
				t.Error(err)
			}
		}
		if _, err := s.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, user); err != nil {
			t.Error(err)
		}
	}()
	actor := audit.ActorSnapshot{Kind: "user", ID: user.String(), Role: "admin"}
	receiver := catalog.Status{ProtocolVersion: 2, SupportedScopes: []string{"local"},
		SupportedEventTypes: []string{catalog.TypeSnapshotPage, catalog.TypeSnapshotEnd}, Scope: "local",
		HighestObservedGeneration: "0", CompletedGeneration: "0",
		Limits: catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}}
	r := catalog.BootstrapRequest{Scope: "local", Epoch: first}
	if _, err := s.AcceptCatalogBootstrap(ctx, r, nil, actor); !errors.Is(err, ErrCatalogPreflightRequired) {
		t.Fatalf("new intent skipped preflight: %v", err)
	}
	preflight := &CatalogBootstrapPreflight{Receiver: receiver}
	if coalesced, err := s.AcceptCatalogBootstrap(ctx, r, preflight, actor); err != nil || coalesced {
		t.Fatalf("initial intent: %v/%v", coalesced, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET allocated_generation=7 WHERE epoch=$1`, first); err != nil {
		t.Fatal(err)
	}
	if coalesced, err := s.AcceptCatalogBootstrap(ctx, r, nil, actor); err != nil || !coalesced {
		t.Fatalf("lost-response retry required receiver or reset progress: %v/%v", coalesced, err)
	}
	wrong := r
	wrong.ExpectedReceiverEpoch = &second
	if _, err := s.AcceptCatalogBootstrap(ctx, wrong, nil, actor); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("inconsistent retry accepted: %v", err)
	}
	work, err := s.LoadCatalogWork(ctx)
	if err != nil || work.Evidence.AllocatedGeneration != 7 {
		t.Fatalf("retry changed durable progress: %+v/%v", work, err)
	}
	r = catalog.BootstrapRequest{Scope: "local", Epoch: second, ExpectedEpoch: &first}
	preflight.Source = &work
	if coalesced, err := s.AcceptCatalogBootstrap(ctx, r, preflight, actor); err != nil || coalesced {
		t.Fatalf("explicit epoch recovery refused: %v/%v", coalesced, err)
	}
	if _, err := s.AcceptCatalogBootstrap(ctx, catalog.BootstrapRequest{Scope: "local", Epoch: first}, nil, actor); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("retired original tuple reused: %v", err)
	}
	work, err = s.LoadCatalogWork(ctx)
	if err != nil || work.Evidence.Epoch != second || work.Evidence.AllocatedGeneration != 0 || !work.ForceFull {
		t.Fatalf("new epoch did not require fresh full: %+v/%v", work, err)
	}
	preflight.Source, preflight.Receiver.Epoch = &work, &second
	r = catalog.BootstrapRequest{Scope: "local", Epoch: refused, ExpectedEpoch: &second, ExpectedReceiverEpoch: &second}
	if _, err := s.AcceptCatalogBootstrap(ctx, r, preflight, actor); !errors.Is(err, ErrCatalogBusy) {
		t.Fatalf("matching in-progress pair reset: %v", err)
	}
	preflight.Receiver.Epoch, r.ExpectedReceiverEpoch = nil, nil
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET allocated_generation=1 WHERE epoch=$1`, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptCatalogBootstrap(ctx, r, preflight, actor); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("stale source recovery observation accepted: %v", err)
	}
	work, err = s.LoadCatalogWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preflight.Source, r.Epoch = &work, failed
	constraint := "catalog_boot_audit_" + failed.String()[:8]
	if _, err := s.Pool.Exec(ctx, `ALTER TABLE audit_log ADD CONSTRAINT `+constraint+` CHECK(resource_id<>'`+failed.String()+`') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = s.Pool.Exec(context.Background(), `ALTER TABLE audit_log DROP CONSTRAINT `+constraint) }()
	if coalesced, err := s.AcceptCatalogBootstrap(ctx, r, preflight, actor); err == nil || coalesced {
		t.Fatalf("failed bootstrap audit accepted: %v/%v", coalesced, err)
	}
	var intact bool
	if err := s.Pool.QueryRow(ctx, `SELECT epoch=$1 AND allocated_generation=1
		AND NOT EXISTS(SELECT 1 FROM search_catalog_epochs WHERE epoch=$2)
		AND (SELECT count(*) FROM audit_log WHERE resource_id=$3 AND result='success')=2
		AND (SELECT count(*) FROM audit_log WHERE resource_id=$3 AND result='failure')=2
		AND EXISTS(SELECT 1 FROM audit_log WHERE resource_id=$3 AND metadata->>'coalesced'='true' AND changes='[]'::jsonb)
		FROM search_catalog_state WHERE scope='local'`, second, failed, first.String()).Scan(&intact); err != nil || !intact {
		t.Fatalf("bootstrap lost source state or retry/rejection audit: %v/%v", intact, err)
	}
}
