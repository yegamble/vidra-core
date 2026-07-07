package ipfs

import (
	"context"
	"sync"
)

// FakeClusterClient is an in-memory ClusterClient for unit tests. Like
// FakeIPFSClient it lives in the package (not a _test file) so the mirror's unit
// tests can depend on it without touching a real cluster — the nodeless guardrail
// from .ralph/specs/ipfs-media.md §11. Set Down to simulate an unreachable cluster
// (every RPC then fails), letting a test assert the mirror degrades gracefully:
// cluster replication is best-effort, so a cluster outage must never fail a pin.
type FakeClusterClient struct {
	mu sync.Mutex

	// Down, when true, makes every RPC fail (cluster unreachable).
	Down bool

	// pins is the set of CIDs the cluster currently replicates.
	pins map[string]bool

	// PinCount / UnpinCount / HealthCount are call counters for assertions.
	PinCount    int
	UnpinCount  int
	HealthCount int
}

var _ ClusterClient = (*FakeClusterClient)(nil)

// NewFakeClusterClient returns a ready, healthy fake cluster.
func NewFakeClusterClient() *FakeClusterClient {
	return &FakeClusterClient{pins: map[string]bool{}}
}

func (f *FakeClusterClient) ClusterPin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pins == nil {
		f.pins = map[string]bool{}
	}
	if f.Down {
		return errNodeDown
	}
	f.pins[cid] = true
	f.PinCount++
	return nil
}

func (f *FakeClusterClient) ClusterUnpin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pins == nil {
		f.pins = map[string]bool{}
	}
	if f.Down {
		return errNodeDown
	}
	delete(f.pins, cid)
	f.UnpinCount++
	return nil
}

func (f *FakeClusterClient) ClusterHealth(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.HealthCount++
	if f.Down {
		return errNodeDown
	}
	return nil
}

// IsClusterPinned reports whether the fake cluster currently replicates a CID
// (test helper).
func (f *FakeClusterClient) IsClusterPinned(cid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pins[cid]
}
