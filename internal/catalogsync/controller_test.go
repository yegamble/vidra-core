package catalogsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchevents"
)

type cycleSource struct {
	*deliverySource
	work                         catalog.SourceWork
	demand                       catalog.CaptureDemand
	converted                    int64
	missing                      bool
	captures, loads, conversions int
	code                         string
	captureErr                   error
	deadline                     bool
}

func (s *cycleSource) LoadCatalogWork(ctx context.Context) (catalog.SourceWork, error) {
	s.loads++
	end, ok := ctx.Deadline()
	s.deadline = ok && time.Until(end) <= 2*time.Minute
	if s.missing {
		return catalog.SourceWork{}, pgx.ErrNoRows
	}
	return s.work, nil
}
func (s *cycleSource) ConvertLegacyCatalog(context.Context, int32) (int64, error) {
	s.conversions++
	return s.converted, nil
}
func (s *cycleSource) CatalogCaptureDemand(context.Context) (catalog.CaptureDemand, error) {
	return s.demand, nil
}
func (s *cycleSource) CaptureCatalog(context.Context, uuid.UUID, catalog.Limits) (catalog.Identity, error) {
	s.captures++
	return catalog.Identity{}, s.captureErr
}
func (s *cycleSource) RecordCatalogCaptureFailure(_ context.Context, w catalog.SourceWork, code string) (bool, error) {
	s.code = code
	return true, nil
}
func (s *cycleSource) RecordCatalogDeliveryFailure(_ context.Context, w catalog.SourceWork, code string) (bool, error) {
	s.code = code
	return true, nil
}

func TestCycleNormalPendingAndBoundedCaptureDemand(t *testing.T) {
	pages, w, _, done := deliveryFixture(t, 0)
	now := time.Now().UTC()
	base := &cycleSource{deliverySource: pages, work: w}
	receiver := &cycleReceiver{status: done}
	if err := Cycle(context.Background(), base, receiver, now, time.Hour); err != nil || pages.acks != 1 || base.captures != 0 || !base.deadline {
		t.Fatalf("pending cycle: %v %+v", err, base)
	}
	w.Evidence.Verified, w.Evidence.Pending = w.Evidence.Pending, nil
	w.PendingEndRequestBody, w.PendingCanonicalBytes = nil, 0
	w.VerifiedAt = &now
	w.ForceFull = false
	for _, test := range []struct {
		dirty, force, failure, repair bool
		want                          int
	}{
		{want: 0}, {dirty: true, want: 1}, {force: true, want: 1}, {failure: true, want: 1}, {repair: true, want: 1},
	} {
		s := &cycleSource{deliverySource: pages, work: w, demand: catalog.CaptureDemand{Dirty: test.dirty}}
		s.work.ForceFull = test.force
		if test.failure {
			id := uuid.New()
			s.work.LastErrorCode, s.work.LastErrorID = "delivery_failed", &id
		}
		at := now
		if test.repair {
			at = now.Add(2 * time.Hour)
		}
		if err := Cycle(context.Background(), s, receiver, at, time.Hour); err != nil || s.captures != test.want || s.code != "" {
			t.Fatalf("capture demand %+v: %v %+v", test, err, s)
		}
	}
}

type cycleReceiver struct {
	status catalog.Status
	getErr error
	gets   int
}

func (r *cycleReceiver) CatalogStatus(context.Context) (catalog.Status, error) {
	r.gets++
	return r.status, r.getErr
}
func (r *cycleReceiver) SendCatalogBody(context.Context, []byte) (searchevents.EventBatchResult, error) {
	return searchevents.EventBatchResult{Accepted: 1}, nil
}

func TestCycleNeverClearsErrorsThroughMissingPendingOrBypassesBarriers(t *testing.T) {
	pages, w, _, done := deliveryFixture(t, 0)
	w.Evidence.Verified, w.Evidence.Pending = w.Evidence.Pending, nil
	w.PendingEndRequestBody, w.PendingCanonicalBytes = nil, 0
	w.ForceFull = false
	now := time.Now().UTC()
	w.VerifiedAt = &now
	r := &cycleReceiver{status: done, getErr: errors.New("private transport detail")}
	s := &cycleSource{deliverySource: pages, work: w}
	err := Cycle(context.Background(), s, r, now, time.Hour)
	if err == nil || s.code != "verification_failed" || s.captures != 0 || strings.Contains(err.Error(), "private transport detail") {
		t.Fatalf("GET failure lost durable no-pending barrier: %v %+v", err, s)
	}
	r.getErr = nil
	s = &cycleSource{deliverySource: pages, work: w, demand: catalog.CaptureDemand{Dirty: true}, captureErr: catalog.ErrQuotaExceeded}
	if err := Cycle(context.Background(), s, r, now, time.Hour); !errors.Is(err, catalog.ErrQuotaExceeded) || s.code != "quota_exceeded" {
		t.Fatalf("capture error not durably classified: %v %+v", err, s)
	}
	for _, mode := range []string{"missing", "conflict", "legacy", "converted", "initial"} {
		s = &cycleSource{deliverySource: pages, work: w}
		r.gets = 0
		switch mode {
		case "missing":
			s.missing = true
		case "conflict":
			id := uuid.New()
			s.work.LastErrorCode, s.work.LastErrorID = "epoch_conflict", &id
		case "legacy":
			s.demand.LegacyOutstanding = true
		case "converted":
			s.converted = 1
		case "initial":
			s.work.Evidence.Verified = nil
			s.work.Evidence.AllocatedGeneration = 0
			s.work.ForceFull = true
		}
		err := Cycle(context.Background(), s, r, now, time.Hour)
		if s.captures != 0 || r.gets != 0 || s.code != "" {
			t.Fatalf("%s bypassed barrier: %v %+v", mode, err, s)
		}
		if mode == "conflict" && !errors.Is(err, catalog.ErrEpochConflict) {
			t.Fatal(err)
		}
		if mode == "initial" && !errors.Is(err, ErrBootstrapPending) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = &cycleSource{deliverySource: pages, work: w}
	if err := Cycle(ctx, s, r, now, time.Hour); !errors.Is(err, context.Canceled) || s.loads != 0 {
		t.Fatalf("cancelled cycle performed work: %v", err)
	}
}
