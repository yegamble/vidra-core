//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestExpiredCatalogPendingRetirementFencesEvidenceAndCapturesHigherSameEpoch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	epoch, oldError, newError := uuid.New(), uuid.New(), uuid.New()
	defer func() {
		for _, table := range []string{"search_catalog_state", "search_catalog_epochs"} {
			if _, err := s.Pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE epoch=$1`, epoch); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO search_catalog_state(scope,epoch,allocated_generation,force_full,last_error_code,last_error_id)
		VALUES ('local',$1,1,false,'delivery_failed',$2)`, epoch, oldError); err != nil {
		t.Fatal(err)
	}
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, time.Now().UTC(), limits, func(catalog.FrozenPage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	end, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Queries().InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: 1,
		MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256, EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
		t.Fatal(err)
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
	receiver := catalog.Status{ProtocolVersion: 2, SupportedScopes: []string{"local"},
		SupportedEventTypes: []string{catalog.TypeSnapshotPage, catalog.TypeSnapshotEnd}, Scope: "local", Epoch: &epoch,
		HighestObservedGeneration: "0", CompletedGeneration: "0", Limits: limits}
	if changed, err := s.RetireExpiredCatalogPending(ctx, w, receiver); err == nil || changed {
		t.Fatalf("live pending retired without expiry proof: %v/%v", changed, err)
	}
	receiver.HighestObservedGeneration = "1"
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET last_error_code='epoch_conflict' WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.RetireExpiredCatalogPending(ctx, load(), receiver); err == nil || changed {
		t.Fatalf("same-epoch retirement bypassed durable epoch conflict: %v/%v", changed, err)
	}
	// Restore ordinary fixture evidence for the independent stale-token cases.
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET last_error_code='delivery_failed' WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET last_error_id=$1 WHERE epoch=$2`, newError, epoch); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.RetireExpiredCatalogPending(ctx, w, receiver); err != nil || changed {
		t.Fatalf("stale same-code error retired current pending: %v/%v", changed, err)
	}
	w = load()
	if _, err := s.Queries().RequestCatalogFull(ctx, sqlcgen.RequestCatalogFullParams{Epoch: epoch, ExpectedAllocatedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.RetireExpiredCatalogPending(ctx, w, receiver); err != nil || changed {
		t.Fatalf("stale force evidence retired pending: %v/%v", changed, err)
	}
	w = load()
	if changed, err := s.RetireExpiredCatalogPending(ctx, w, receiver); err != nil || !changed {
		t.Fatalf("exact expired pending could not retire: %v/%v", changed, err)
	}
	if changed, err := s.RetireExpiredCatalogPending(ctx, w, receiver); err != nil || changed {
		t.Fatalf("retirement replay mutated state: %v/%v", changed, err)
	}
	w = load()
	if w.Evidence.Pending != nil || w.Evidence.AllocatedGeneration != 1 || !w.ForceFull || w.ForceFullAfterGeneration != 1 || w.LastErrorID == nil || *w.LastErrorID != newError {
		t.Fatalf("retirement lost allocator/freshness/error evidence: %+v", w)
	}
	id, err := s.CaptureCatalog(ctx, epoch, limits)
	if err != nil || id.Epoch != epoch || id.Generation != 2 {
		t.Fatalf("expired generation stranded same-epoch capture: %+v/%v", id, err)
	}
	if assessment, err := catalog.AssessReceiver(load().Evidence, receiver); err != nil || !assessment.CanDeliver {
		t.Fatalf("higher capture cannot progress past permanent receiver fence: %+v/%v", assessment, err)
	}
}
