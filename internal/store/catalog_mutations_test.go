package store

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
)

func TestCatalogMutationAdaptersRejectInvalidInputsBeforeDB(t *testing.T) {
	ctx := context.Background()
	empty, _ := catalog.MembershipDigest(nil)
	p := catalog.FullProof{Identity: catalog.Identity{Scope: "local", Epoch: uuid.New(), Generation: 1},
		Manifest: catalog.Manifest{MembershipSHA256: empty, PagesSHA256: empty}}
	w := catalog.SourceWork{Evidence: catalog.SourceEvidence{Epoch: p.Identity.Epoch, AllocatedGeneration: 1, Pending: &p}}
	s := new(Store) // Invalid inputs must refuse without acquiring a connection.
	for _, change := range []func(*catalog.SourceWork){
		func(w *catalog.SourceWork) { w.Evidence.Epoch = uuid.Nil },
		func(w *catalog.SourceWork) { w.Evidence.AllocatedGeneration = -1 },
		func(w *catalog.SourceWork) { w.ForceFullAfterGeneration = -1 },
		func(w *catalog.SourceWork) { w.ForceFullAfterGeneration = 2 },
		func(w *catalog.SourceWork) { w.LastErrorCode = "capture_failed" },
		func(w *catalog.SourceWork) { id := uuid.Nil; w.LastErrorID, w.LastErrorCode = &id, "capture_failed" },
		func(w *catalog.SourceWork) {
			id := uuid.New()
			w.LastErrorID, w.LastErrorCode = &id, "raw private failure"
		},
	} {
		bad := w
		change(&bad)
		if changed, err := s.AcknowledgeCatalogWork(ctx, bad, p); changed || err == nil {
			t.Fatalf("invalid ACK input accepted: %v/%v", changed, err)
		}
		if changed, err := s.RecordCatalogDeliveryFailure(ctx, bad, "delivery_failed"); changed || err == nil {
			t.Fatalf("invalid delivery input accepted: %v/%v", changed, err)
		}
		bad.Evidence.Pending = nil
		if changed, err := s.RecordCatalogCaptureFailure(ctx, bad, "capture_failed"); changed || err == nil {
			t.Fatalf("invalid capture input accepted: %v/%v", changed, err)
		}
	}
	for _, id := range []catalog.Identity{{"remote", p.Identity.Epoch, 1}, {"local", uuid.New(), 1}, {"local", p.Identity.Epoch, 2}} {
		badProof := p
		badProof.Identity = id
		bad := w
		bad.Evidence.Pending = &badProof
		if changed, err := s.AcknowledgeCatalogWork(ctx, bad, badProof); changed || err == nil {
			t.Fatalf("invalid pending ACK identity accepted: %v/%v", changed, err)
		}
		if changed, err := s.RecordCatalogDeliveryFailure(ctx, bad, "delivery_failed"); changed || err == nil {
			t.Fatalf("invalid pending delivery identity accepted: %v/%v", changed, err)
		}
	}
}
