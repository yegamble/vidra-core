package catalogsync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchclient"
)

type epochReceiver struct {
	status         catalog.Status
	putErr, getErr error
	puts, gets     int
	epoch          uuid.UUID
	predecessor    *uuid.UUID
}

func (r *epochReceiver) CatalogStatus(context.Context) (catalog.Status, error) {
	r.gets++
	return r.status, r.getErr
}
func (r *epochReceiver) SetCatalogEpoch(_ context.Context, epoch uuid.UUID, predecessor *uuid.UUID) error {
	r.puts++
	r.epoch, r.predecessor = epoch, predecessor
	return r.putErr
}

func initialEpochFixture() (catalog.SourceWork, catalog.Status, catalog.Status) {
	epoch, predecessor := uuid.New(), uuid.New()
	w := catalog.SourceWork{Evidence: catalog.SourceEvidence{Epoch: epoch}, ForceFull: true, ReceiverPredecessor: &predecessor}
	before := catalog.Status{ProtocolVersion: 2, Scope: "local", Epoch: &predecessor,
		SupportedScopes: []string{"local"}, SupportedEventTypes: []string{catalog.TypeSnapshotPage, catalog.TypeSnapshotEnd},
		HighestObservedGeneration: "0", CompletedGeneration: "0",
		Limits: catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}}
	current := before
	current.Epoch = &epoch
	return w, before, current
}

func TestApplyBootstrapEpochUsesExactIntentAndIndependentAssessment(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		w, before, current := initialEpochFixture()
		if initialize {
			w.ReceiverPredecessor, before.Epoch = nil, nil
		}
		r := &epochReceiver{status: current}
		matched, err := ApplyBootstrapEpoch(context.Background(), r, w, before)
		if err != nil || !matched || r.puts != 1 || r.gets != 1 || r.epoch != w.Evidence.Epoch || !reflect.DeepEqual(r.predecessor, w.ReceiverPredecessor) {
			t.Fatalf("epoch intent not independently observed: %v/%v, %+v", matched, err, r)
		}
		r.puts, r.gets = 0, 0
		if matched, err := ApplyBootstrapEpoch(context.Background(), r, w, current); err != nil || !matched || r.puts != 0 || r.gets != 0 {
			t.Fatalf("matching observed epoch was reset: %v/%v", matched, err)
		}
	}
}

func TestApplyBootstrapEpochLostResponseMayResolveBut409NeverDoes(t *testing.T) {
	w, before, current := initialEpochFixture()
	r := &epochReceiver{status: current, putErr: errors.New("lost response")}
	if matched, err := ApplyBootstrapEpoch(context.Background(), r, w, before); err != nil || !matched || r.gets != 1 {
		t.Fatalf("lost response not independently resolved: %v/%v", matched, err)
	}
	r = &epochReceiver{status: current, putErr: &searchclient.HTTPError{Status: 409}}
	if matched, err := ApplyBootstrapEpoch(context.Background(), r, w, before); !errors.Is(err, catalog.ErrEpochConflict) || matched || r.gets != 0 {
		t.Fatalf("PUT409 inferred recovery from matching GET: %v/%v", matched, err)
	}
	for _, completed := range []bool{false, true} {
		ahead := current
		ahead.HighestObservedGeneration = "1"
		if completed {
			at := time.Now().UTC()
			ahead.Ready, ahead.CompletedGeneration, ahead.CompletedAt = true, "1", &at
			const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
			ahead.CompletedManifest = &catalog.Manifest{MembershipSHA256: empty, PagesSHA256: empty}
		}
		r = &epochReceiver{status: ahead}
		if matched, err := ApplyBootstrapEpoch(context.Background(), r, w, before); !errors.Is(err, catalog.ErrProducerBehindReceiver) || matched {
			t.Fatalf("matching epoch with ahead receiver allowed capture: %v/%v", matched, err)
		}
	}
}

func TestApplyBootstrapEpochRefusesProgressConflictOrChangedPredecessor(t *testing.T) {
	for _, change := range []func(*catalog.SourceWork, *catalog.Status){
		func(w *catalog.SourceWork, _ *catalog.Status) { w.Evidence.AllocatedGeneration = 1 },
		func(w *catalog.SourceWork, _ *catalog.Status) { w.ForceFull = false },
		func(w *catalog.SourceWork, _ *catalog.Status) { w.ForceFullAfterGeneration = 1 },
		func(w *catalog.SourceWork, s *catalog.Status) {
			id := uuid.Nil
			w.ReceiverPredecessor, s.Epoch = &id, &id
		},
		func(_ *catalog.SourceWork, s *catalog.Status) { id := uuid.New(); s.Epoch = &id },
		func(_ *catalog.SourceWork, s *catalog.Status) { s.ProtocolVersion = 1 },
		func(w *catalog.SourceWork, s *catalog.Status) {
			id := uuid.New()
			w.LastErrorCode, w.LastErrorID, s.Epoch = "epoch_conflict", &id, &w.Evidence.Epoch
		},
	} {
		w, before, current := initialEpochFixture()
		change(&w, &before)
		r := &epochReceiver{status: current}
		if matched, err := ApplyBootstrapEpoch(context.Background(), r, w, before); err == nil || matched || r.puts != 0 || r.gets != 0 {
			t.Fatalf("refused intent performed receiver I/O: %v/%v, %+v", matched, err, r)
		}
	}
}
