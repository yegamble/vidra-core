package instancesettings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// countingBump records how many times the cross-replica invalidation counter
// was advanced, and can be made to fail.
type countingBump struct {
	calls int
	err   error
}

func (c *countingBump) bump(context.Context) error {
	c.calls++
	return c.err
}

// TestApplyBumpsSettingsVersion is the multi-replica proof at unit tier: a
// write MUST advance the shared counter, because that number is the only thing
// that tells the other N-1 api replicas their in-memory overlay is stale. A
// write that reloads its own cache and bumps nothing is exactly the 1-of-N
// staleness bug this seam exists to close.
func TestApplyBumpsSettingsVersion(t *testing.T) {
	ctx := context.Background()
	bumper := &countingBump{}
	svc := NewService(newFakeRepo(), testDefaults(), WithVersionBump(bumper.bump))
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := svc.Apply(ctx, map[string]Update{KeyInstanceName: {Value: "Two Replicas"}}, uuid.Nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if bumper.calls != 1 {
		t.Fatalf("bump calls after one Apply = %d, want 1", bumper.calls)
	}

	// A batch is ONE change from a reader's point of view, so it is one bump,
	// not one per key: the poller only ever compares the number for inequality.
	if err := svc.Apply(ctx, map[string]Update{
		KeyInstanceName:   {Value: "Three Replicas"},
		KeyUploadsEnabled: {Value: "false"},
	}, uuid.Nil); err != nil {
		t.Fatalf("apply batch: %v", err)
	}
	if bumper.calls != 2 {
		t.Fatalf("bump calls after a 2-key batch = %d, want 2 (one per Apply)", bumper.calls)
	}

	// Clearing an override is a change like any other.
	if err := svc.Apply(ctx, map[string]Update{KeyInstanceName: {Delete: true}}, uuid.Nil); err != nil {
		t.Fatalf("apply delete: %v", err)
	}
	if bumper.calls != 3 {
		t.Fatalf("bump calls after a delete = %d, want 3", bumper.calls)
	}
}

// TestApplyNoBumpWithoutChanges: an empty batch writes nothing, so it must not
// wake every replica in the fleet for a no-op.
func TestApplyNoBumpWithoutChanges(t *testing.T) {
	bumper := &countingBump{}
	svc := NewService(newFakeRepo(), testDefaults(), WithVersionBump(bumper.bump))
	if err := svc.Apply(context.Background(), nil, uuid.Nil); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if bumper.calls != 0 {
		t.Fatalf("bump calls for an empty batch = %d, want 0", bumper.calls)
	}
}

// TestApplyBumpFailureSurfaces: a failed bump is NOT swallowed. The row landed,
// but the rest of the fleet will not learn about it until restart — the exact
// silent failure this counter exists to remove — so the caller is told and can
// retry (the same PATCH is idempotent).
func TestApplyBumpFailureSurfaces(t *testing.T) {
	sentinel := errors.New("counter unavailable")
	bumper := &countingBump{err: sentinel}
	svc := NewService(newFakeRepo(), testDefaults(), WithVersionBump(bumper.bump))
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	err := svc.Apply(context.Background(), map[string]Update{KeyInstanceName: {Value: "x"}}, uuid.Nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("apply error = %v, want the bump error", err)
	}
}

// TestApplyWithoutBumperStillWorks: single-process installs and the many tests
// that build the service with two arguments must be unaffected — a nil seam is
// simply not called.
func TestApplyWithoutBumperStillWorks(t *testing.T) {
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := svc.Apply(context.Background(), map[string]Update{KeyInstanceName: {Value: "Solo"}}, uuid.Nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := svc.String(KeyInstanceName); got != "Solo" {
		t.Fatalf("instance_name = %q, want Solo", got)
	}
}
