package ipfsmirror

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// P19.P1 second-network foundation tests: the worker routes each ledger row to
// EXACTLY one network's node client, fail-closed — a private row is provably
// incapable of being pinned via the public client (and vice versa) — and the
// PUBLIC read models (ipfs_pinned / the detail ipfs{} object) never expose a
// private-swarm CID. See .ralph/specs/ipfs-media-private.md §1/§5/§8.

func seedPendingNet(r *fakeRepo, key, class, network string) {
	r.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: class, State: "pending", Network: network,
		NextAttemptAt: time.Now().UTC().Add(-time.Second),
	}
}

func seedPinnedNet(r *fakeRepo, key, class, cid, network string, videoID uuid.UUID) {
	r.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: class, State: "pinned", Cid: cid, Network: network,
		VideoID: pgtype.UUID{Bytes: videoID, Valid: videoID != uuid.Nil},
	}
}

// TestPrivateRowRoutedToPrivateClientNeverPublic is the cardinal fail-closed
// invariant in the private direction: with the private node DOWN, a private row
// stays pending and the PUBLIC client is never invoked; on recovery it pins via the
// PRIVATE client — the public client is still never touched.
func TestPrivateRowRoutedToPrivateClientNeverPublic(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	putBlob(t, blobs, "private-videos/p.mp4", "secret-bytes")

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	privateFake.Down = true // private node unreachable

	cfg := testConfig()
	cfg.PrivateEnabled = true
	cfg.PrivateClient = privateFake
	svc := New(repo, &fakeLookups{}, blobs, publicFake, cfg)

	seedPendingNet(repo, "private-videos/p.mp4", string(ClassVideoOriginal), networkPrivate)

	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := repo.state("private-videos/p.mp4"); got != "pending" {
		t.Fatalf("private row state = %q, want pending (private node down, no cross-network fallback)", got)
	}
	if publicFake.AddCount != 0 {
		t.Fatalf("public client saw %d Add calls for a private row, want 0 (cardinal invariant)", publicFake.AddCount)
	}

	// Recovery: the private node comes back → the row pins via the PRIVATE client.
	privateFake.Down = false
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain after recovery: %v", err)
	}
	if got := repo.state("private-videos/p.mp4"); got != "pinned" {
		t.Fatalf("private row state = %q, want pinned via the private client", got)
	}
	if privateFake.AddCount != 1 {
		t.Errorf("private client Add count = %d, want 1", privateFake.AddCount)
	}
	if publicFake.AddCount != 0 {
		t.Errorf("public client Add count = %d, want 0 (never routes a private row)", publicFake.AddCount)
	}
}

// TestPublicRowRoutedToPublicClientNeverPrivate is the reverse fail-closed
// direction: a public row with the public node DOWN stays pending and the PRIVATE
// client is never invoked; on recovery it pins via the public client.
func TestPublicRowRoutedToPublicClientNeverPrivate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	putBlob(t, blobs, "web-videos/pub.mp4", "public-bytes")

	publicFake := ipfs.NewFakeIPFSClient()
	publicFake.Down = true // public node unreachable
	privateFake := ipfs.NewFakeIPFSClient()

	cfg := testConfig()
	cfg.PrivateEnabled = true
	cfg.PrivateClient = privateFake
	svc := New(repo, &fakeLookups{}, blobs, publicFake, cfg)

	seedPendingNet(repo, "web-videos/pub.mp4", string(ClassVideoOriginal), networkPublic)

	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := repo.state("web-videos/pub.mp4"); got != "pending" {
		t.Fatalf("public row state = %q, want pending (public node down)", got)
	}
	if privateFake.AddCount != 0 {
		t.Fatalf("private client saw %d Add calls for a public row, want 0", privateFake.AddCount)
	}

	publicFake.Down = false
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain after recovery: %v", err)
	}
	if got := repo.state("web-videos/pub.mp4"); got != "pinned" {
		t.Fatalf("public row state = %q, want pinned via the public client", got)
	}
	if publicFake.AddCount != 1 {
		t.Errorf("public client Add count = %d, want 1", publicFake.AddCount)
	}
	if privateFake.AddCount != 0 {
		t.Errorf("private client Add count = %d, want 0 (never routes a public row)", privateFake.AddCount)
	}
}

// TestPrivateRowNotDrainedWhenPrivateTierDisabled proves NO behavior change with
// the private tier off (the default): a private row is never claimed (no private
// client exists) while public rows pin exactly as before.
func TestPrivateRowNotDrainedWhenPrivateTierDisabled(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	putBlob(t, blobs, "private-videos/p.mp4", "secret")
	putBlob(t, blobs, "web-videos/pub.mp4", "public")
	publicFake := ipfs.NewFakeIPFSClient()

	// Default testConfig: PrivateEnabled=false, no private client.
	svc := New(repo, &fakeLookups{}, blobs, publicFake, testConfig())
	seedPendingNet(repo, "private-videos/p.mp4", string(ClassVideoOriginal), networkPrivate)
	seedPendingNet(repo, "web-videos/pub.mp4", string(ClassVideoOriginal), networkPublic)

	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := repo.state("private-videos/p.mp4"); got != "pending" {
		t.Errorf("private row state = %q, want pending (private tier off, never drained)", got)
	}
	if got := repo.state("web-videos/pub.mp4"); got != "pinned" {
		t.Errorf("public row state = %q, want pinned (no behavior change)", got)
	}
	if publicFake.AddCount != 1 {
		t.Errorf("public client Add count = %d, want 1 (only the public object)", publicFake.AddCount)
	}
}

// TestVideoPinsNeverExposesPrivateNetworkCID fences the detail `ipfs` object: a
// private-swarm pin is NEVER surfaced; only a public-network pin is. The second leg
// (a public pin lighting up) proves the fence is selective, not vacuously empty.
func TestVideoPinsNeverExposesPrivateNetworkCID(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	vid := uuid.New()

	privCID := ipfs.RawLeafCIDv1([]byte("private-original"))
	seedPinnedNet(repo, "private-videos/"+vid.String()+".mp4", string(ClassVideoOriginal), privCID, networkPrivate, vid)

	out, ok, err := svc.VideoPins(ctx, vid)
	if err != nil {
		t.Fatalf("VideoPins: %v", err)
	}
	if ok || out.OriginalCID != "" || out.HLSCID != "" {
		t.Fatalf("VideoPins exposed a private-swarm CID %+v (ok=%v), want none", out, ok)
	}

	pubCID := ipfs.RawLeafCIDv1([]byte("public-original"))
	seedPinnedNet(repo, "web-videos/"+vid.String()+".mp4", string(ClassVideoOriginal), pubCID, networkPublic, vid)
	out, ok, err = svc.VideoPins(ctx, vid)
	if err != nil {
		t.Fatalf("VideoPins (with public pin): %v", err)
	}
	if !ok || out.OriginalCID != pubCID {
		t.Fatalf("VideoPins = %+v (ok=%v), want the public CID %s only", out, ok, pubCID)
	}
}

// TestPinnedVideoIDsNeverCountsPrivateNetwork fences the card/feed `ipfs_pinned`
// badge: a video whose media is pinned ONLY on the private swarm reports NOT pinned;
// a public pin still lights the badge.
func TestPinnedVideoIDsNeverCountsPrivateNetwork(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	privOnly := uuid.New()
	pubVid := uuid.New()

	seedPinnedNet(repo, "private-videos/"+privOnly.String()+".mp4", string(ClassVideoOriginal), ipfs.RawLeafCIDv1([]byte("a")), networkPrivate, privOnly)
	seedPinnedNet(repo, "web-videos/"+pubVid.String()+".mp4", string(ClassVideoOriginal), ipfs.RawLeafCIDv1([]byte("b")), networkPublic, pubVid)

	got, err := svc.PinnedVideoIDs(ctx, []uuid.UUID{privOnly, pubVid})
	if err != nil {
		t.Fatalf("PinnedVideoIDs: %v", err)
	}
	if got[privOnly] {
		t.Error("ipfs_pinned badge lit up for a private-swarm-only video, want false")
	}
	if !got[pubVid] {
		t.Error("ipfs_pinned badge missing for a public pinned video, want true")
	}
}
