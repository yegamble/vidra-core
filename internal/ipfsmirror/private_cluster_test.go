package ipfsmirror

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
)

// P19.P3 private-tier cluster tests: the optional IPFS Cluster on the PRIVATE swarm
// replicates private-network pins and is fail-closed exactly like the node client —
// a private row is provably incapable of being replicated through the PUBLIC cluster
// (and vice versa). Cluster health surfaces per-swarm in the admin status block.
// See .ralph/specs/ipfs-media-private.md §1/§5/§9.

// privateClusterConfig wires BOTH swarms with their OWN node + cluster fakes so a
// test can assert routing goes to exactly one swarm's cluster.
func privateClusterConfig(publicCluster, privateCluster ipfs.ClusterClient, privateClient ipfs.Client) Config {
	cfg := testConfig()
	cfg.ClusterEnabled = true
	cfg.Cluster = publicCluster
	cfg.PrivateEnabled = true
	cfg.PrivateClient = privateClient
	cfg.PrivateCluster = privateCluster
	return cfg
}

// TestPrivateRowReplicatesViaPrivateClusterNeverPublic: a private-network pin is
// replicated to the PRIVATE cluster after the private node pin, and the PUBLIC
// cluster is never touched — the cardinal fail-closed invariant extended to the
// replication layer.
func TestPrivateRowReplicatesViaPrivateClusterNeverPublic(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	publicClient := ipfs.NewFakeIPFSClient()
	privateClient := ipfs.NewFakeIPFSClient()
	publicCluster := ipfs.NewFakeClusterClient()
	privateCluster := ipfs.NewFakeClusterClient()

	key := "private-videos/" + uuid.NewString() + ".mp4"
	putBlob(t, blobs, key, "secret-bytes")
	seedPendingNet(repo, key, string(ClassVideoOriginal), networkPrivate)

	svc := New(repo, &fakeLookups{}, blobs, publicClient, privateClusterConfig(publicCluster, privateCluster, privateClient))
	if n, err := svc.DrainDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainDue = %d, %v; want 1, nil", n, err)
	}

	cid := repo.rows[key].Cid
	if cid == "" {
		t.Fatal("private node pin did not record a CID")
	}
	if privateCluster.PinCount != 1 || !privateCluster.IsClusterPinned(cid) {
		t.Errorf("private cluster PinCount=%d pinned=%v, want 1/true", privateCluster.PinCount, privateCluster.IsClusterPinned(cid))
	}
	if publicCluster.PinCount != 0 {
		t.Errorf("public cluster PinCount = %d for a private row, want 0 (cardinal invariant)", publicCluster.PinCount)
	}
	if publicClient.AddCount != 0 {
		t.Errorf("public node Add count = %d for a private row, want 0", publicClient.AddCount)
	}
}

// TestPublicRowReplicatesViaPublicClusterNeverPrivate is the reverse direction: a
// public-network pin replicates only to the PUBLIC cluster; the private cluster (and
// private node) are never invoked.
func TestPublicRowReplicatesViaPublicClusterNeverPrivate(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	publicClient := ipfs.NewFakeIPFSClient()
	privateClient := ipfs.NewFakeIPFSClient()
	publicCluster := ipfs.NewFakeClusterClient()
	privateCluster := ipfs.NewFakeClusterClient()

	key := "avatars/users/" + uuid.NewString() + ".png"
	putBlob(t, blobs, key, "avatar-bytes")
	seedPendingNet(repo, key, string(ClassUserAvatar), networkPublic)

	svc := New(repo, &fakeLookups{}, blobs, publicClient, privateClusterConfig(publicCluster, privateCluster, privateClient))
	if n, err := svc.DrainDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainDue = %d, %v; want 1, nil", n, err)
	}

	cid := repo.rows[key].Cid
	if cid == "" {
		t.Fatal("public node pin did not record a CID")
	}
	if publicCluster.PinCount != 1 || !publicCluster.IsClusterPinned(cid) {
		t.Errorf("public cluster PinCount=%d pinned=%v, want 1/true", publicCluster.PinCount, publicCluster.IsClusterPinned(cid))
	}
	if privateCluster.PinCount != 0 {
		t.Errorf("private cluster PinCount = %d for a public row, want 0 (cardinal invariant)", privateCluster.PinCount)
	}
	if privateClient.AddCount != 0 {
		t.Errorf("private node Add count = %d for a public row, want 0", privateClient.AddCount)
	}
}

// TestPrivateClusterUnpinMirrorsPrivateNodeUnpin: unpinning the last reference to a
// private CID removes it from the private node AND the private cluster, never the
// public cluster.
func TestPrivateClusterUnpinMirrorsPrivateNodeUnpin(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	publicClient := ipfs.NewFakeIPFSClient()
	privateClient := ipfs.NewFakeIPFSClient()
	publicCluster := ipfs.NewFakeClusterClient()
	privateCluster := ipfs.NewFakeClusterClient()

	cid := ipfs.RawLeafCIDv1([]byte("private-only-ref"))
	key := "private-videos/solo.mp4"
	seedPinnedNet(repo, key, string(ClassVideoOriginal), cid, networkPrivate, uuid.Nil)
	// Pre-seed the private cluster as replicating it (as a real pin would have).
	_ = privateCluster.ClusterPin(context.Background(), cid)
	privateCluster.UnpinCount = 0

	svc := New(repo, &fakeLookups{}, blobs, publicClient, privateClusterConfig(publicCluster, privateCluster, privateClient))
	_ = repo.EnqueueIPFSUnpin(context.Background(), key)
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if repo.state(key) != "unpinned" {
		t.Fatalf("state = %q, want unpinned", repo.state(key))
	}
	if privateClient.UnpinCount != 1 {
		t.Errorf("private node UnpinCount = %d, want 1", privateClient.UnpinCount)
	}
	if privateCluster.UnpinCount != 1 || privateCluster.IsClusterPinned(cid) {
		t.Errorf("private cluster UnpinCount=%d pinned=%v, want 1/false", privateCluster.UnpinCount, privateCluster.IsClusterPinned(cid))
	}
	if publicCluster.UnpinCount != 0 {
		t.Errorf("public cluster UnpinCount = %d for a private CID, want 0", publicCluster.UnpinCount)
	}
}

// TestStatusPrivateClusterHealthPerSwarm: the admin status reports EACH swarm's
// cluster health in its own network block, independently. The private cluster being
// down must not flip the public block's cluster_reachable (and vice versa), and the
// top-level cluster_reachable stays the PUBLIC cluster's (back-compat).
func TestStatusPrivateClusterHealthPerSwarm(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	publicClient := ipfs.NewFakeIPFSClient()
	privateClient := ipfs.NewFakeIPFSClient()
	publicCluster := ipfs.NewFakeClusterClient()
	privateCluster := ipfs.NewFakeClusterClient()

	svc := New(repo, &fakeLookups{}, blobs, publicClient, privateClusterConfig(publicCluster, privateCluster, privateClient))

	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	pub := st.Networks[NetworkPublic]
	priv := st.Networks[NetworkPrivate]
	if !pub.ClusterEnabled || !pub.ClusterReachable {
		t.Errorf("public block cluster = (%v,%v), want (true,true)", pub.ClusterEnabled, pub.ClusterReachable)
	}
	if !priv.ClusterEnabled || !priv.ClusterReachable {
		t.Errorf("private block cluster = (%v,%v), want (true,true)", priv.ClusterEnabled, priv.ClusterReachable)
	}

	// Take ONLY the private cluster down: the public block stays healthy, the private
	// block reports unreachable-but-enabled, and the top-level stays the public one.
	privateCluster.Down = true
	st, err = svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status (private cluster down): %v", err)
	}
	pub = st.Networks[NetworkPublic]
	priv = st.Networks[NetworkPrivate]
	if !pub.ClusterReachable {
		t.Error("public block cluster_reachable flipped false when only the PRIVATE cluster is down")
	}
	if !priv.ClusterEnabled || priv.ClusterReachable {
		t.Errorf("private block cluster = (%v,%v), want (true,false)", priv.ClusterEnabled, priv.ClusterReachable)
	}
	if !st.ClusterReachable {
		t.Error("top-level cluster_reachable must stay the PUBLIC cluster's (back-compat), not the private one's")
	}
}

// TestStatusNoPrivateClusterConfigured: with the private tier on but no private
// cluster wired, the private block's cluster flags are both false and no probe runs.
func TestStatusNoPrivateClusterConfigured(t *testing.T) {
	repo := newFakeRepo()
	cfg := testConfig()
	cfg.PrivateEnabled = true
	cfg.PrivateClient = ipfs.NewFakeIPFSClient()

	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), cfg)
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	priv := st.Networks[NetworkPrivate]
	if priv.ClusterEnabled || priv.ClusterReachable {
		t.Errorf("private block cluster flags = (%v,%v), want (false,false)", priv.ClusterEnabled, priv.ClusterReachable)
	}
}
