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
