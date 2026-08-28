package settingsversion

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The poller's only failure signal used to be a slog line, which is the exact
// class of invisible staleness this package exists to close, one level up: a
// replica whose every Tick fails keeps serving the settings it booted with and
// nothing an admin can reach says so. Health is what the admin status page
// reads.
func TestPollerHealthRecording(t *testing.T) {
	ctx := context.Background()
	counter := &fakeCounter{version: 3}
	p := newTestPoller(counter, Cache{Name: "settings", Reload: (&countingCache{}).reload})

	// Before anything has run there is nothing to report: zero time, no error.
	if last, err := p.Health(); !last.IsZero() || err != nil {
		t.Fatalf("Health before any poll = (%v, %v), want (zero, nil)", last, err)
	}

	// Prime reads the counter successfully, and that IS a successful poll — the
	// first ~interval of a replica's life must not read as "never synced".
	before := time.Now()
	if err := p.Prime(ctx); err != nil {
		t.Fatalf("prime: %v", err)
	}
	last, herr := p.Health()
	if herr != nil || last.Before(before) {
		t.Fatalf("Health after Prime = (%v, %v), want a fresh success", last, herr)
	}

	// A failing tick records the error and keeps the last success where it was.
	counter.err = errors.New("connection refused")
	if _, err := p.Tick(ctx); err == nil {
		t.Fatal("Tick with a failing counter reported no error")
	}
	gotLast, gotErr := p.Health()
	if gotErr == nil || gotErr.Error() != "connection refused" {
		t.Fatalf("Health error = %v, want the tick's failure", gotErr)
	}
	if !gotLast.Equal(last) {
		t.Errorf("last success moved on a failed tick: %v -> %v", last, gotLast)
	}

	// Recovery clears the error and advances the success time — a page must
	// never keep showing an outage the next tick already healed.
	counter.err = nil
	if _, err := p.Tick(ctx); err != nil {
		t.Fatalf("recovered tick: %v", err)
	}
	recLast, recErr := p.Health()
	if recErr != nil || recLast.Before(gotLast) {
		t.Fatalf("Health after recovery = (%v, %v), want a fresh success and no error", recLast, recErr)
	}
}

// A reload failure is a health failure even though the counter read succeeded:
// the replica is still stale, which is the fact the page reports.
func TestPollerHealthReportsReloadFailures(t *testing.T) {
	ctx := context.Background()
	counter := &fakeCounter{version: 1}
	broken := &countingCache{err: errors.New("settings table unreadable")}
	p := newTestPoller(counter, Cache{Name: "instance settings", Reload: broken.reload})

	if err := p.Prime(ctx); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if _, err := counter.BumpSettingsVersion(ctx); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if _, err := p.Tick(ctx); err == nil {
		t.Fatal("Tick with a failing reload reported no error")
	}
	if _, herr := p.Health(); herr == nil {
		t.Fatal("Health after a failed reload = nil error, want the failure recorded")
	}
}
