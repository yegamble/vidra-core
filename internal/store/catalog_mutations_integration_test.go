//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogMutationAdaptersFenceSoleConnectionAndStaleEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	epoch := uuid.New()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, table := range []string{"search_catalog_state", "search_catalog_epochs"} {
			if _, err := s.Pool.Exec(cleanup, `DELETE FROM `+table+` WHERE epoch=$1`, epoch); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, table := range []string{"search_catalog_epochs", "search_catalog_state"} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO `+table+`(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
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
	expect := func(changed bool, err error, want bool) {
		t.Helper()
		if err != nil || changed != want {
			t.Fatalf("mutation changed=%v want=%v: %v", changed, want, err)
		}
	}
	w := load()
	if changed, err := s.RecordCatalogCaptureFailure(ctx, w, "private raw failure"); changed || err == nil {
		t.Fatal("unsanitized failure code accepted")
	}
	changed, err := s.RecordCatalogCaptureFailure(ctx, w, "capture_failed")
	expect(changed, err, true)
	changed, err = s.RecordCatalogCaptureFailure(ctx, w, "capture_failed")
	expect(changed, err, false)
	freeze := func(g int64) {
		t.Helper()
		empty, _ := catalog.MembershipDigest(nil)
		end, err := catalog.FreezeEnd(catalog.Identity{Scope: "local", Epoch: epoch, Generation: catalog.Generation(g)},
			catalog.Manifest{MembershipSHA256: empty, PagesSHA256: empty}, 0, uuid.New(), time.Now().UTC(),
			catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
			q := sqlcgen.New(tx)
			if _, err := q.AdvanceCatalogCapture(ctx, sqlcgen.AdvanceCatalogCaptureParams{Epoch: epoch, ExpectedAllocatedGeneration: g - 1}); err != nil {
				return err
			}
			return q.InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: g,
				MembershipSha256: end.Manifest.MembershipSHA256, PagesSha256: end.Manifest.PagesSHA256, EndEventID: end.EventID, EndRequestBody: end.RequestBody})
		}); err != nil {
			t.Fatal(err)
		}
	}
	freeze(1) // Model frozen source/receiver proof; no HTTP or capture claim.
	w = load()
	p := *w.Evidence.Pending
	wrong := p
	wrong.Identity.Generation++
	if changed, err := s.AcknowledgeCatalogWork(ctx, w, wrong); changed || err == nil {
		t.Fatal("mismatched completed proof accepted")
	}
	if changed, err := s.RecordCatalogCaptureFailure(ctx, w, "capture_failed"); changed || err == nil {
		t.Fatal("capture failure overwrote pending work")
	}
	changed, err = s.RecordCatalogDeliveryFailure(ctx, w, "delivery_failed")
	expect(changed, err, true)
	changed, err = s.RecordCatalogDeliveryFailure(ctx, w, "delivery_failed")
	expect(changed, err, false)
	w = load()
	prior := w
	changed, err = s.RecordCatalogDeliveryFailure(ctx, w, "delivery_failed")
	expect(changed, err, true)
	w = load()
	if w.LastErrorID == nil || prior.LastErrorID == nil || *w.LastErrorID == *prior.LastErrorID {
		t.Fatal("repeated same-code error did not rotate evidence")
	}
	changed, err = s.AcknowledgeCatalogWork(ctx, prior, p)
	expect(changed, err, false)
	// Model a newer committed full-request fence; operator auditing is separate.
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET force_full=true,force_full_after_generation=1 WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	changed, err = s.AcknowledgeCatalogWork(ctx, w, p)
	expect(changed, err, false)
	changed, err = s.RecordCatalogDeliveryFailure(ctx, w, "delivery_failed")
	expect(changed, err, false)
	w = load()
	changed, err = s.AcknowledgeCatalogWork(ctx, w, p)
	expect(changed, err, true)
	w = load()
	if w.Evidence.Pending != nil || !w.ForceFull || w.ForceFullAfterGeneration != 1 || w.LastErrorID != nil || w.Evidence.Verified == nil || *w.Evidence.Verified != p {
		t.Fatal("old ACK lost newer force or exact verified evidence")
	}
	freeze(2)
	w = load()
	changed, err = s.AcknowledgeCatalogWork(ctx, w, *w.Evidence.Pending)
	expect(changed, err, true)
	w = load()
	if w.ForceFull || w.Evidence.Pending != nil || w.Evidence.Verified.Identity.Generation != 2 {
		t.Fatal("fresh higher full failed to clear the force fence")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if changed, err := s.RecordCatalogCaptureFailure(canceled, w, "capture_failed"); changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mutation returned success: %v/%v", changed, err)
	}
}
