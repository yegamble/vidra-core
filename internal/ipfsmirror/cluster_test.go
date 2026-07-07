package ipfsmirror

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// clusterConfig is testConfig plus a fake IPFS Cluster wired in (STOR-05).
func clusterConfig(cluster ipfs.ClusterClient) Config {
	cfg := testConfig()
	cfg.ClusterEnabled = true
	cfg.Cluster = cluster
	return cfg
}

// TestWorkerClusterReplicatesPin: when a cluster is configured, a node-pinned
// object is ALSO replicated to the cluster (same CID) after the node pin.
func TestWorkerClusterReplicatesPin(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cluster := ipfs.NewFakeClusterClient()
	key := "avatars/users/" + uuid.NewString() + ".png"
	putBlob(t, blobs, key, "avatar-bytes")
	seedPending(repo, key, string(ClassUserAvatar))

	svc := New(repo, &fakeLookups{}, blobs, client, clusterConfig(cluster))
	if n, err := svc.DrainDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainDue = %d, %v; want 1, nil", n, err)
	}
	cid := repo.rows[key].Cid
	if cid == "" {
		t.Fatal("node pin did not record a CID")
	}
	if cluster.PinCount != 1 {
		t.Errorf("cluster PinCount = %d, want 1", cluster.PinCount)
	}
	if !cluster.IsClusterPinned(cid) {
		t.Errorf("cluster did not replicate the node-pinned CID %s", cid)
	}
}

// TestWorkerClusterReplicatesDirectoryPin: an HLS directory add is likewise
// replicated to the cluster by its car_root CID.
func TestWorkerClusterReplicatesDirectoryPin(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cluster := ipfs.NewFakeClusterClient()
	vid := uuid.New()
	prefix := "streaming-playlists/" + vid.String() + "/"
	putBlob(t, blobs, prefix+"master.m3u8", "#EXTM3U")
	putBlob(t, blobs, prefix+"720p/playlist.m3u8", "#EXTM3U 720")
	putBlob(t, blobs, prefix+"720p/seg_00000.ts", "...ts...")
	repo.rows[prefix] = &sqlcgen.MediaIpfsPin{ObjectKey: prefix, MediaClass: string(ClassHLS), State: "pending"}

	svc := New(repo, &fakeLookups{}, blobs, client, clusterConfig(cluster))
	if n, err := svc.DrainDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainDue = %d, %v; want 1, nil", n, err)
	}
	carRoot := repo.rows[prefix].Cid
	if carRoot == "" {
		t.Fatal("directory pin did not record a car_root CID")
	}
	if !cluster.IsClusterPinned(carRoot) {
		t.Errorf("cluster did not replicate the car_root %s", carRoot)
	}
}

// TestWorkerClusterUnpinMirrorsNodeUnpin: unpinning the last reference to a CID
// removes it from BOTH the node and the cluster.
func TestWorkerClusterUnpinMirrorsNodeUnpin(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cluster := ipfs.NewFakeClusterClient()
	cid := ipfs.RawLeafCIDv1([]byte("only-ref"))
	key := "web-videos/solo.mp4"
	repo.rows[key] = &sqlcgen.MediaIpfsPin{ObjectKey: key, MediaClass: "video_original", State: "pinned", Cid: cid}
	// Pre-seed the cluster as replicating it (as a real pin would have).
	_ = cluster.ClusterPin(context.Background(), cid)
	cluster.UnpinCount = 0

	svc := New(repo, &fakeLookups{}, blobs, client, clusterConfig(cluster))
	_ = repo.EnqueueIPFSUnpin(context.Background(), key)
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if repo.state(key) != "unpinned" {
		t.Fatalf("state = %q, want unpinned", repo.state(key))
	}
	if client.UnpinCount != 1 {
		t.Errorf("node UnpinCount = %d, want 1", client.UnpinCount)
	}
	if cluster.UnpinCount != 1 {
		t.Errorf("cluster UnpinCount = %d, want 1", cluster.UnpinCount)
	}
	if cluster.IsClusterPinned(cid) {
		t.Error("cluster still replicates the CID after the last reference was unpinned")
	}
}

// TestWorkerClusterUnpinSkippedWhenStillReferenced: the cluster unpin, like the
// node unpin, is reference-checked — a CID still shared by another live row is not
// removed from the cluster.
func TestWorkerClusterUnpinSkippedWhenStillReferenced(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cluster := ipfs.NewFakeClusterClient()
	cid := ipfs.RawLeafCIDv1([]byte("shared"))
	keyA, keyB := "web-videos/a.mp4", "web-videos/b.mp4"
	repo.rows[keyA] = &sqlcgen.MediaIpfsPin{ObjectKey: keyA, MediaClass: "video_original", State: "pinned", Cid: cid}
	repo.rows[keyB] = &sqlcgen.MediaIpfsPin{ObjectKey: keyB, MediaClass: "video_original", State: "pinned", Cid: cid}
	_ = cluster.ClusterPin(context.Background(), cid)
	cluster.UnpinCount = 0

	svc := New(repo, &fakeLookups{}, blobs, client, clusterConfig(cluster))
	_ = repo.EnqueueIPFSUnpin(context.Background(), keyA)
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("drain A: %v", err)
	}
	if cluster.UnpinCount != 0 {
		t.Errorf("cluster unpin called %d times while CID still referenced, want 0", cluster.UnpinCount)
	}
	if !cluster.IsClusterPinned(cid) {
		t.Error("cluster dropped a still-referenced CID")
	}
}

// TestWorkerClusterErrorDoesNotFailPin: cluster replication is best-effort — with
// the cluster unreachable the node pin still succeeds and the row reaches 'pinned'.
func TestWorkerClusterErrorDoesNotFailPin(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cluster := ipfs.NewFakeClusterClient()
	cluster.Down = true // cluster unreachable
	key := "thumbnails/" + uuid.NewString() + ".jpg"
	putBlob(t, blobs, key, "thumb")
	seedPending(repo, key, string(ClassThumbnail))

	svc := New(repo, &fakeLookups{}, blobs, client, clusterConfig(cluster))
	if n, err := svc.DrainDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainDue = %d, %v; want 1, nil (cluster outage must not fail the node pin)", n, err)
	}
	if repo.state(key) != "pinned" {
		t.Errorf("state = %q, want pinned despite the cluster being down", repo.state(key))
	}
}

// TestStatusClusterReachable: Status probes the cluster's /id health and reports
// cluster_enabled + cluster_reachable truthfully.
func TestStatusClusterReachable(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cluster := ipfs.NewFakeClusterClient()

	svc := New(repo, &fakeLookups{}, blobs, client, clusterConfig(cluster))
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.ClusterEnabled {
		t.Error("ClusterEnabled = false, want true")
	}
	if !st.ClusterReachable {
		t.Error("ClusterReachable = false, want true (healthy fake cluster)")
	}

	cluster.Down = true
	st, err = svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status (down): %v", err)
	}
	if !st.ClusterEnabled {
		t.Error("ClusterEnabled = false after down, want true (still configured)")
	}
	if st.ClusterReachable {
		t.Error("ClusterReachable = true, want false (cluster unreachable)")
	}
}

// TestStatusNoClusterConfigured: with no cluster wired, both cluster flags are
// false and no probe happens.
func TestStatusNoClusterConfigured(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.ClusterEnabled || st.ClusterReachable {
		t.Errorf("cluster flags = (%v,%v), want (false,false)", st.ClusterEnabled, st.ClusterReachable)
	}
}
