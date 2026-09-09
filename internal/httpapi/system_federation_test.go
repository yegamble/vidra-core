package httpapi

import (
	"strings"
	"testing"
	"time"
)

// A29-F10: /admin/system had no federation component at all, so it reported
// `ok` across ten components with two dead-lettered deliveries on the books.

func TestFederationComponentReportsDeadLettersWithoutFailingThePage(t *testing.T) {
	got := federationComponent(FederationHealth{
		Pending:                 3,
		DeadLettered:            2,
		OldestPendingAgeSeconds: 40,
		LastDeliveredAt:         time.Now(),
	}, nil)
	if got.Status != "degraded" {
		t.Fatalf("status = %q, want degraded — a peer that will not accept our activities is not this instance being down", got.Status)
	}
	if !strings.Contains(got.Error, "2 outbound deliveries") {
		t.Errorf("message does not name the dead letters: %q", got.Error)
	}
	if !strings.Contains(got.Error, "3 still pending") {
		t.Errorf("message does not name the backlog: %q", got.Error)
	}
}

// A backlog older than the whole retry ladder is not a slow peer — it is a drain
// loop that is not running, and that IS the instance being down.
func TestFederationComponentIsDownWhenNothingIsDraining(t *testing.T) {
	got := federationComponent(FederationHealth{
		Pending:                 12,
		OldestPendingAgeSeconds: federationStallSeconds + 1,
	}, nil)
	if got.Status != "down" {
		t.Fatalf("status = %q, want down", got.Status)
	}
	if !strings.Contains(got.Error, "nothing is draining") {
		t.Errorf("message does not say what is wrong: %q", got.Error)
	}
}

// A queue walking its backoff honestly is never called stalled.
func TestFederationComponentToleratesTheRetryLadder(t *testing.T) {
	got := federationComponent(FederationHealth{
		Pending:                 1,
		OldestPendingAgeSeconds: 900, // inside the ladder
	}, nil)
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok while the ladder is still walking", got.Status)
	}
}

func TestFederationComponentIsDownWhenItCannotRead(t *testing.T) {
	got := federationComponent(FederationHealth{}, errReadFailed)
	if got.Status != "down" {
		t.Fatalf("status = %q, want down", got.Status)
	}
	if !strings.Contains(got.Error, "could not be read") {
		t.Errorf("message does not say the page cannot see: %q", got.Error)
	}
}

// Absent, not "ok", when federation is not wired: claiming health for a feature
// the instance does not run is a claim, not a measurement.
func TestFederationComponentIsAbsentWhenUnwired(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	if _, ok := srv.federationStatus(t.Context()); ok {
		t.Error("an instance with no federation wiring reported a federation component")
	}
}

var errReadFailed = errStub("connection refused")

type errStub string

func (e errStub) Error() string { return string(e) }

// TestFederationComponentRendersTheLastDelivery is the rehearsal's finding (c).
// LastDeliveredAt was computed on every probe in cmd/api and rendered into
// nothing, so the one number that separates a DRAINED federation queue from an
// ABANDONED one — both of which read as zero pending — never reached the page
// that asks the question.
func TestFederationComponentRendersTheLastDelivery(t *testing.T) {
	last := time.Date(2026, 9, 8, 17, 41, 9, 0, time.UTC)
	got := federationComponent(FederationHealth{Pending: 0, LastDeliveredAt: last}, nil)
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Detail["last_delivered_at"] != "2026-09-08T17:41:09Z" {
		t.Errorf("last_delivered_at = %q", got.Detail["last_delivered_at"])
	}
	if got.Detail["pending"] != "0" || got.Detail["dead_lettered"] != "0" {
		t.Errorf("detail = %+v, want the queue numbers beside the verdict", got.Detail)
	}

	// A failing verdict carries it too: an operator reading a stalled queue wants
	// "and the last thing that DID leave was at …" more than anyone reading a
	// healthy one does.
	stalled := federationComponent(FederationHealth{
		OldestPendingAgeSeconds: federationStallSeconds + 1, Pending: 3, LastDeliveredAt: last,
	}, nil)
	if stalled.Status != "down" || stalled.Detail["last_delivered_at"] == "" {
		t.Errorf("stalled component = %+v, want down WITH the last delivery", stalled)
	}

	// And an instance that has never delivered anything says nothing rather than
	// printing a zero timestamp, which looks like a bug and reads like a date.
	fresh := federationComponent(FederationHealth{}, nil)
	if _, present := fresh.Detail["last_delivered_at"]; present {
		t.Errorf("a brand-new instance must not claim a last delivery: %+v", fresh.Detail)
	}
}

// A29 follow-ups: a delivery this instance cancelled because its destination was
// blocked is NOT a dead letter, and reporting it as one sent the rehearsal-3
// operator hunting a peer failure that never happened. The lab watched six of
// them under "one or more peers did not accept what it sent"; every one of them
// went out intact the moment the block was lifted.
func TestBlockCancelledDeliveriesAreNotReportedAsDeadLetters(t *testing.T) {
	got := federationComponent(FederationHealth{
		Pending:           1,
		CancelledByPolicy: 6,
		LastDeliveredAt:   time.Now(),
	}, nil)
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok — a blocklist doing its job is not a degradation", got.Status)
	}
	if strings.Contains(got.Error, "dead-lettered") || strings.Contains(got.Error, "did not accept") {
		t.Errorf("cancelled deliveries were reported as a peer failure: %q", got.Error)
	}
	if got.Detail["cancelled_by_policy"] != "6" {
		t.Errorf("detail = %v, want the cancellations stated as their own fact", got.Detail)
	}
	if got.Detail["dead_lettered"] != "0" {
		t.Errorf("dead_lettered = %q, want 0 — none of these exhausted anything", got.Detail["dead_lettered"])
	}
}

// And an instance that has blocked nobody carries no line about blocks, on the
// same doctrine as last_delivered_at: a zero that is always there is noise.
func TestNoCancellationsMeansNoCancellationLine(t *testing.T) {
	got := federationComponent(FederationHealth{Pending: 1}, nil)
	if _, ok := got.Detail["cancelled_by_policy"]; ok {
		t.Errorf("detail = %v, want no cancellation line at all", got.Detail)
	}
}
