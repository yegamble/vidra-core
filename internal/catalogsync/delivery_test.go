package catalogsync

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchevents"
)

type deliverySource struct {
	pages map[int32][]byte
	reads []int32
	acks  int
	match bool
}

func (s *deliverySource) LoadCatalogPage(_ context.Context, _ catalog.Identity, index int32) ([]byte, error) {
	s.reads = append(s.reads, index)
	if body, ok := s.pages[index]; ok {
		return bytes.Clone(body), nil
	}
	return nil, errors.New("missing frozen page")
}
func (s *deliverySource) AcknowledgeCatalogWork(_ context.Context, w catalog.SourceWork, completed catalog.FullProof) (bool, error) {
	s.acks++
	if w.Evidence.Pending == nil || *w.Evidence.Pending != completed {
		return false, errors.New("ACK did not use exact pending completion")
	}
	return s.match, nil
}

type deliveryReceiver struct {
	status catalog.Status
	sent   [][]byte
	failAt int
	reject bool
}

func (r *deliveryReceiver) CatalogStatus(context.Context) (catalog.Status, error) {
	return r.status, nil
}
func (r *deliveryReceiver) SendCatalogBody(_ context.Context, body []byte) (searchevents.EventBatchResult, error) {
	r.sent = append(r.sent, bytes.Clone(body))
	if len(r.sent) == r.failAt {
		return searchevents.EventBatchResult{}, errors.New("lost response")
	}
	if r.reject {
		return searchevents.EventBatchResult{Failed: []searchevents.FailedEvent{{Code: "invalid", Message: "private upstream detail"}}}, nil
	}
	return searchevents.EventBatchResult{Accepted: 1}, nil
}

func deliveryFixture(t *testing.T, documents int) (*deliverySource, catalog.SourceWork, catalog.Status, catalog.Status) {
	t.Helper()
	epoch, at := uuid.New(), time.Now().UTC()
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	s := &deliverySource{pages: make(map[int32][]byte), match: true}
	full, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, at, limits,
		func(p catalog.FrozenPage) error { s.pages[p.PageIndex] = p.RequestBody; return nil })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < documents; i++ {
		doc := searchevents.VideoDoc{ID: uuid.New(), Kind: "local", OwnerID: uuid.New(), ChannelID: uuid.New(),
			ChannelHandle: "channel", ChannelName: "Channel", Tags: []string{}, Title: "video", Privacy: "public", State: "published", CreatedAt: at, UpdatedAt: at}
		if i == 200 {
			doc.Description = strings.Repeat("x", 200000)
		}
		if err := full.Add(doc); err != nil {
			t.Fatal(err)
		}
	}
	end, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	w := catalog.SourceWork{Evidence: catalog.SourceEvidence{Epoch: epoch, AllocatedGeneration: 1,
		Pending: &catalog.FullProof{Identity: catalog.Identity{Scope: "local", Epoch: epoch, Generation: 1}, Manifest: end.Manifest}},
		ForceFull: true, PendingCanonicalBytes: end.CanonicalBytes, PendingEndRequestBody: end.RequestBody}
	before := catalog.Status{ProtocolVersion: 2, Scope: "local", Epoch: &epoch, Limits: limits,
		SupportedScopes: []string{"local"}, SupportedEventTypes: []string{catalog.TypeSnapshotPage, catalog.TypeSnapshotEnd},
		HighestObservedGeneration: "0", CompletedGeneration: "0"}
	done := before
	done.Ready, done.HighestObservedGeneration, done.CompletedGeneration = true, "1", "1"
	done.CompletedManifest, done.CompletedAt = &end.Manifest, &at
	return s, w, before, done
}

func TestDeliverPreflightsThenReplaysAllFrozenBodiesAndVerifiesCompletion(t *testing.T) {
	for _, count := range []int{0, 201} {
		s, w, before, done := deliveryFixture(t, count)
		r := &deliveryReceiver{status: done}
		if acked, err := Deliver(context.Background(), s, r, w, before); err != nil || !acked || s.acks != 1 {
			t.Fatalf("full was not independently verified and ACKed: %v/%v", acked, err)
		}
		if len(r.sent) != len(s.pages)+1 || !bytes.Equal(r.sent[len(r.sent)-1], w.PendingEndRequestBody) {
			t.Fatal("did not send every page and exact frozen END")
		}
		for i := range s.pages {
			if !bytes.Equal(r.sent[i], s.pages[i]) {
				t.Fatal("frozen page bytes changed")
			}
		}
		if count != 0 && !reflect.DeepEqual(s.reads, []int32{0, 1, 0, 1}) {
			t.Fatalf("all pages were not preflighted before replay: %v", s.reads)
		}
	}
}

func TestDeliverRefusesLateOversizeMissingPageOrQuotaBeforeAnyPOST(t *testing.T) {
	for _, change := range []func(*deliverySource, *catalog.SourceWork, *catalog.Status){
		func(s *deliverySource, _ *catalog.SourceWork, r *catalog.Status) {
			if len(s.pages[1]) <= len(s.pages[0]) {
				t.Fatal("late-page budget fixture does not fit its first page")
			}
			r.Limits.MaxRequestBytes = int64(len(s.pages[1]) - 1)
		},
		func(s *deliverySource, _ *catalog.SourceWork, _ *catalog.Status) { delete(s.pages, 1) },
		func(_ *deliverySource, w *catalog.SourceWork, r *catalog.Status) {
			r.Limits.MaxSnapshotBytes = w.PendingCanonicalBytes - 1
		},
		func(_ *deliverySource, w *catalog.SourceWork, _ *catalog.Status) {
			w.PendingEndRequestBody = []byte(`{}`)
		},
	} {
		s, w, before, done := deliveryFixture(t, 201)
		change(s, &w, &before)
		r := &deliveryReceiver{status: done}
		if acked, err := Deliver(context.Background(), s, r, w, before); err == nil || acked || len(r.sent) != 0 || s.acks != 0 {
			t.Fatalf("preflight failure sent/ACKed work: %v/%v", acked, err)
		}
	}
}

func TestDeliverReceiptsNeverCompleteButFreshGETCanResolveLostResponse(t *testing.T) {
	s, w, before, done := deliveryFixture(t, 201)
	r := &deliveryReceiver{status: before}
	if acked, err := Deliver(context.Background(), s, r, w, before); err == nil || acked || s.acks != 0 {
		t.Fatalf("accepted staging granted completion: %v/%v", acked, err)
	}
	r.sent, r.reject = nil, true
	if acked, err := Deliver(context.Background(), s, r, w, before); err == nil || acked || len(r.sent) != 1 || strings.Contains(err.Error(), "private upstream detail") {
		t.Fatalf("rejected staging granted completion or leaked upstream detail: %v/%v", acked, err)
	}
	r.reject = false
	s.reads, r.sent, r.status, r.failAt = nil, nil, done, 1
	if acked, err := Deliver(context.Background(), s, r, w, before); err != nil || !acked || s.acks != 1 || len(r.sent) != 1 {
		t.Fatalf("lost response was not resolved by exact fresh completion: %v/%v", acked, err)
	}
	s.match, r.sent = false, nil
	if acked, err := Deliver(context.Background(), s, r, w, done); err != nil || acked || len(r.sent) != 0 {
		t.Fatalf("stale source ACK or completed page replay: %v/%v", acked, err)
	}
}
