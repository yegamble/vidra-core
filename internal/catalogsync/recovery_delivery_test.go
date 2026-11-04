package catalogsync

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
)

func recoveryDeliveryFixture(t *testing.T) (*deliverySource, catalog.SourceWork, catalog.Status, catalog.Status) {
	t.Helper()
	s, w, _, done := deliveryFixture(t, 0, 3)
	verified := *w.Evidence.Pending
	verified.Identity.Generation = 2
	w.Evidence.Verified = &verified
	w.ForceFullAfterGeneration = 3
	older := done
	older.HighestObservedGeneration, older.CompletedGeneration = "1", "1"
	return s, w, older, done
}

func TestDeliverRecoveryRequiresCommittedCurrentForceFenceAndExactCompletion(t *testing.T) {
	for _, lost := range []bool{false, true} {
		s, w, older, done := recoveryDeliveryFixture(t)
		r := &deliveryReceiver{status: done}
		if lost {
			r.failAt = 1
		}
		if acked, err := Deliver(context.Background(), s, r, w, older); !errors.Is(err, catalog.ErrReceiverBehindVerified) || acked || len(r.sent) != 0 {
			t.Fatalf("normal path bypassed restore: %v/%v", acked, err)
		}
		if acked, err := DeliverRecovery(context.Background(), s, r, w, older); err != nil || !acked || s.acks != 1 || len(r.sent) != 1 || r.gets != 1 {
			t.Fatalf("fenced recovery failed exact completion: %v/%v", acked, err)
		}
		if s.ackedWork == nil || !reflect.DeepEqual(*s.ackedWork, w) {
			t.Fatal("recovery ACK lost original verified/force evidence")
		}
	}
	s, w, older, _ := recoveryDeliveryFixture(t)
	r := &deliveryReceiver{status: older}
	if acked, err := DeliverRecovery(context.Background(), s, r, w, older); err == nil || acked || s.acks != 0 {
		t.Fatalf("staging receipt granted recovery: %v/%v", acked, err)
	}
}

func TestDeliverRecoveryNeverWeakensOtherFencesOrUncommittedForce(t *testing.T) {
	for _, change := range []func(*catalog.SourceWork, *catalog.Status){
		func(w *catalog.SourceWork, _ *catalog.Status) { w.ForceFull = false },
		func(w *catalog.SourceWork, _ *catalog.Status) { w.ForceFullAfterGeneration = 2 },
		func(w *catalog.SourceWork, _ *catalog.Status) { w.Evidence.Pending = nil },
		func(w *catalog.SourceWork, _ *catalog.Status) {
			id := uuid.New()
			w.LastErrorCode, w.LastErrorID = "epoch_conflict", &id
		},
		func(_ *catalog.SourceWork, r *catalog.Status) { id := uuid.New(); r.Epoch = &id },
		func(_ *catalog.SourceWork, r *catalog.Status) { r.HighestObservedGeneration = "4" },
		func(_ *catalog.SourceWork, r *catalog.Status) { r.HighestObservedGeneration = "3" }, // permanently expired
		func(w *catalog.SourceWork, r *catalog.Status) {
			r.CompletedGeneration, r.HighestObservedGeneration = "2", "2"
			m := w.Evidence.Verified.Manifest
			m.DocumentCount, m.PageCount = 1, 1
			r.CompletedManifest = &m
		},
	} {
		s, w, older, done := recoveryDeliveryFixture(t)
		change(&w, &older)
		r := &deliveryReceiver{status: done}
		if acked, err := DeliverRecovery(context.Background(), s, r, w, older); err == nil || acked || len(s.reads) != 0 || s.acks != 0 || len(r.sent) != 0 || r.gets != 0 {
			t.Fatalf("recovery weakened another fence: %v/%v", acked, err)
		}
	}
}
