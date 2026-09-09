//go:build ipfs_integration

// Real-kubo coverage for the two primitives the A31 follow-ups added: the pin
// enumeration the ledger<->node reconciliation compares against, and the gateway
// probe the server-side redirect is gated on. Both are unit-tested against
// httptest, which proves the parsing and the policy; only a real node proves the
// WIRE FORMAT, which is the half a hand-written client gets wrong. Same tag,
// same self-skip and same env as the rest of this file's suite.
package ipfs

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ListPins must see a CID this test just added, and must not see one it did not.
// The negative half is what makes the positive half mean something: a parser that
// returned every line of anything would pass the first assertion alone.
func TestIntegrationListPinsSeesWhatTheNodeHolds(t *testing.T) {
	c, _ := testClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Two distinct payloads → two distinct CIDs. The freshness marker keeps a
	// re-run from colliding with the previous run's blocks.
	marker := time.Now().UTC().Format(time.RFC3339Nano)
	added, err := c.Add(ctx, "listpins.txt", strings.NewReader("vidra listpins probe "+marker))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Unpin(ctx, added.CID)
	})

	pins, err := c.ListPins(ctx, 0)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if _, held := pins[added.CID]; !held {
		t.Fatalf("ListPins returned %d pins and none of them is the CID just added; the pin/ls wire format is not what this client parses", len(pins))
	}
	// A CID the node has never seen. Valid shape, no bytes anywhere.
	const absent = "bafkreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy"
	if _, held := pins[absent]; held {
		t.Errorf("ListPins claims a pin on a CID this node was never given")
	}

	// And after an unpin the node stops reporting it — the transition the
	// reconciliation reads as "the ledger's row lost its pin".
	if err := c.Unpin(ctx, added.CID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	pins, err = c.ListPins(ctx, 0)
	if err != nil {
		t.Fatalf("ListPins after unpin: %v", err)
	}
	if _, held := pins[added.CID]; held {
		t.Error("ListPins still reports a CID that was unpinned")
	}
}

// The gateway probe against the real gateway, on a CID this node holds. This is
// the exact call the five-minute health probe makes, and the exact URL shape a
// server-side 307 carries.
func TestIntegrationGatewayProbeFetchesAPinnedCID(t *testing.T) {
	c, gateway := testClient(t)
	if gateway == "" {
		t.Skip("IPFS_TEST_GATEWAY_URL not set; skipping the gateway probe proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	marker := time.Now().UTC().Format(time.RFC3339Nano)
	added, err := c.Add(ctx, "probe.txt", strings.NewReader("vidra gateway probe "+marker))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Unpin(ctx, added.CID)
	})

	if err := NewGatewayProbe(gateway, nil).Fetch(ctx, added.CID); err != nil {
		t.Fatalf("the gateway did not serve a CID this node pins: %v", err)
	}
}

// A gateway that is not there must be reported as down. This is the A31 failure
// in its simplest form — the node's RPC is answering throughout, and the address
// probed is one nothing is listening on.
func TestIntegrationGatewayProbeReportsAnUnreachableGateway(t *testing.T) {
	c, _ := testClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The node is up: the whole point is that RPC health is not gateway health.
	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("fixture: the node must be reachable for this test to mean anything: %v", err)
	}
	// Port 1 is reserved and never a gateway.
	const absent = "bafkreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy"
	if err := NewGatewayProbe("http://127.0.0.1:1", nil).Fetch(ctx, absent); err == nil {
		t.Fatal("the probe reported a gateway nothing is listening on as healthy")
	}
}

// UNPINNING IS NOT FORGETTING, against a real node and a real gateway.
//
// A31's rehearsal measured this by hand: after a moderator block unpinned a
// video's CIDs, the instance's own gateway kept serving all of them, and only
// `ipfs repo gc` turned them into 404 while a still-pinned control stayed 200.
// This is that measurement as a test, and it is the only thing that proves the
// repo/gc wire format — the half a hand-written client gets wrong.
//
// It needs a node started WITHOUT --enable-gc (the local proof lane's node is),
// or the daemon's own collector may win the race and the "still retrievable"
// assertion becomes flaky. That assertion is therefore a t.Log rather than a
// failure: what this test is here to prove is that RepoGC runs, parses, and
// removes — not that some other node's GC schedule stayed out of the way.
func TestIntegrationRepoGCRemovesUnpinnedBlocksOnly(t *testing.T) {
	c, gateway := testClient(t)
	if gateway == "" {
		t.Skip("IPFS_TEST_GATEWAY_URL not set; skipping the repo-gc proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	probe := NewGatewayProbe(gateway, nil)

	marker := time.Now().UTC().Format(time.RFC3339Nano)
	withdrawn, err := c.Add(ctx, "withdrawn.txt", strings.NewReader("vidra takedown "+marker))
	if err != nil {
		t.Fatalf("Add (withdrawn): %v", err)
	}
	kept, err := c.Add(ctx, "kept.txt", strings.NewReader("vidra kept "+marker))
	if err != nil {
		t.Fatalf("Add (kept): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Unpin(ctx, kept.CID)
	})

	if err := probe.Fetch(ctx, withdrawn.CID); err != nil {
		t.Fatalf("fixture: the gateway does not serve a freshly pinned CID: %v", err)
	}

	// The takedown: the pin goes, and the bytes do not.
	if err := c.Unpin(ctx, withdrawn.CID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if err := probe.Fetch(ctx, withdrawn.CID); err != nil {
		t.Logf("the gateway stopped serving the unpinned CID before any GC ran (%v); a node started with --enable-gc will do that, and it does not affect what follows", err)
	} else {
		t.Log("the unpinned CID is STILL served by this node's own gateway — the fact IPFS_GC_AFTER_UNPIN exists for")
	}

	removed, err := c.RepoGC(ctx)
	if err != nil {
		t.Fatalf("RepoGC: %v", err)
	}
	t.Logf("repo gc removed %d blocks", removed)

	if err := probe.Fetch(ctx, withdrawn.CID); err == nil {
		t.Error("the gateway still serves the withdrawn CID after a collection; the takedown did not complete")
	}
	// The control: GC must never touch a pinned CID. Without this the test would
	// pass just as well against a client that wiped the datastore.
	if err := probe.Fetch(ctx, kept.CID); err != nil {
		t.Errorf("the collection took a CID that is still pinned: %v", err)
	}
}
