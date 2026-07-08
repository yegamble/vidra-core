package ipfsmirror

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// P19.P2 eligibility-v2 routing + privacy-flip network transitions. These exercise the
// two-tier service (public AND private clients wired) — the routing the default-config
// tests (private tier off) cannot reach. The cardinal invariant runs through all of
// them: a privacy flip TRANSITIONS the same ledger row between swarms (unpin current →
// re-arm target), never a cross-network double-pin, and the failure mode is always
// "content unreachable", never "private content pinned public". Spec §3/§4/§8.

// twoTierConfig returns a testConfig with BOTH tiers active and the given private
// client, so routePin/effectiveNetwork resolve real public+private swarms.
func twoTierConfig(privateClient ipfs.Client) Config {
	cfg := testConfig() // Enabled=true ⇒ public tier on
	cfg.PrivateEnabled = true
	cfg.PrivateClient = privateClient
	return cfg
}

// drainToConvergence loops DrainDue until nothing is due (as the production worker
// does), so a multi-pass cross-network transition (unpin one swarm on pass N, pin the
// other on pass N+1) settles. Bounded to avoid an infinite loop on a bug.
func drainToConvergence(t *testing.T, ctx context.Context, svc *Service) {
	t.Helper()
	for i := 0; i < 8; i++ {
		n, err := svc.DrainDue(ctx, 10)
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("drain did not converge in 8 passes")
}

// TestPrivateVideoRoutesToPrivateNetworkOnly: with both tiers on, publishing a PRIVATE
// video routes every derivative to the private swarm — pinned via the PRIVATE client,
// the public client never touched — and the pin never surfaces in the public read model.
func TestPrivateVideoRoutesToPrivateNetworkOnly(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	vid := uuid.New()
	origKey := "web-videos/" + vid.String() + ".mp4"
	thumbKey := "thumbnails/" + vid.String() + ".jpg"
	putBlob(t, blobs, origKey, "private-video")
	putBlob(t, blobs, thumbKey, "private-thumb")

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	lk := &fakeLookups{
		videoPrivacy: "private", videoState: "published", videoOK: true,
		userOK: true, userUnlisted: false,
		videoFiles: []VideoFileRef{
			{Kind: "original", StorageKey: origKey},
			{Kind: "thumbnail", StorageKey: thumbKey},
		},
	}
	svc := New(repo, lk, blobs, publicFake, twoTierConfig(privateFake))

	if err := svc.SyncVideo(ctx, vid); err != nil {
		t.Fatalf("SyncVideo (private publish): %v", err)
	}
	for _, key := range []string{origKey, thumbKey} {
		row := repo.rows[key]
		if row == nil || normalizeNetwork(row.Network) != networkPrivate || row.State != "pending" {
			t.Fatalf("%s routed to %+v, want a pending PRIVATE row", key, row)
		}
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	for _, key := range []string{origKey, thumbKey} {
		if repo.state(key) != "pinned" || normalizeNetwork(repo.rows[key].Network) != networkPrivate {
			t.Errorf("%s state=%q net=%q, want pinned on private", key, repo.state(key), repo.rows[key].Network)
		}
	}
	if privateFake.AddCount != 2 {
		t.Errorf("private AddCount = %d, want 2 (original+thumbnail replicated privately)", privateFake.AddCount)
	}
	if publicFake.AddCount != 0 {
		t.Errorf("public AddCount = %d, want 0 (a private video is never pinned on the public swarm)", publicFake.AddCount)
	}
	// The public read model never surfaces a private-swarm pin.
	if _, ok, _ := svc.VideoPins(ctx, vid); ok {
		t.Error("VideoPins surfaced a private-swarm pin, want none")
	}
	if got, _ := svc.PinnedVideoIDs(ctx, []uuid.UUID{vid}); got[vid] {
		t.Error("ipfs_pinned lit up for a private-swarm-only video, want false")
	}
}

// TestSyncVideoPublicToPrivateTransition is the headline flip: a public video pinned on
// the PUBLIC swarm is made private; SyncVideo transitions the SAME row — unpin public →
// re-arm private — and a single drain removes the bytes from the public node and pins
// them on the private node. No durable public pin of the now-private object survives.
func TestSyncVideoPublicToPrivateTransition(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	vid := uuid.New()
	origKey := "web-videos/" + vid.String() + ".mp4"
	putBlob(t, blobs, origKey, "flip-bytes")

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true,
		userOK: true, userUnlisted: false,
		videoFiles: []VideoFileRef{{Kind: "original", StorageKey: origKey}},
	}
	svc := New(repo, lk, blobs, publicFake, twoTierConfig(privateFake))

	// Publish public → pinned on the public swarm.
	if err := svc.SyncVideo(ctx, vid); err != nil {
		t.Fatalf("SyncVideo (public publish): %v", err)
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain publish: %v", err)
	}
	cid := repo.rows[origKey].Cid
	if repo.state(origKey) != "pinned" || normalizeNetwork(repo.rows[origKey].Network) != networkPublic {
		t.Fatalf("after publish: state=%q net=%q, want pinned public", repo.state(origKey), repo.rows[origKey].Network)
	}
	if pinned, _ := publicFake.IsPinned(ctx, cid); !pinned {
		t.Fatal("public node not holding the public video's bytes after publish")
	}

	// Flip to private → SyncVideo transitions the row: unpinning on public, target private.
	lk.videoPrivacy = "private"
	if err := svc.SyncVideo(ctx, vid); err != nil {
		t.Fatalf("SyncVideo (flip private): %v", err)
	}
	row := repo.rows[origKey]
	if row.State != "unpinning" || normalizeNetwork(row.Network) != networkPublic ||
		row.TargetNetwork == nil || *row.TargetNetwork != networkPrivate {
		t.Fatalf("after flip: state=%q net=%q target=%v, want unpinning on public → private", row.State, row.Network, row.TargetNetwork)
	}

	// One drain: public unpins + re-arms private, then the private drain pins it.
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain transition: %v", err)
	}
	if repo.state(origKey) != "pinned" || normalizeNetwork(repo.rows[origKey].Network) != networkPrivate {
		t.Fatalf("after transition: state=%q net=%q, want pinned on private", repo.state(origKey), repo.rows[origKey].Network)
	}
	if pinned, _ := publicFake.IsPinned(ctx, cid); pinned {
		t.Error("public node STILL holds the now-private bytes (privacy leak — cardinal invariant breach)")
	}
	if publicFake.UnpinCount != 1 {
		t.Errorf("public UnpinCount = %d, want 1 (bytes removed from the public swarm on the flip)", publicFake.UnpinCount)
	}
	if privateFake.AddCount != 1 {
		t.Errorf("private AddCount = %d, want 1 (bytes replicated to the private swarm)", privateFake.AddCount)
	}
	if _, ok, _ := svc.VideoPins(ctx, vid); ok {
		t.Error("VideoPins surfaced the video after it went private, want none")
	}
}

// TestSyncVideoPrivateToPublicTransition is the reverse flip: a private video pinned on
// the PRIVATE swarm is made public; the same row transitions private→public, so the
// bytes leave the private node and land on the public node, and the public read model
// now surfaces the (public) CID.
func TestSyncVideoPrivateToPublicTransition(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	vid := uuid.New()
	origKey := "web-videos/" + vid.String() + ".mp4"
	putBlob(t, blobs, origKey, "reverse-flip")

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	lk := &fakeLookups{
		videoPrivacy: "private", videoState: "published", videoOK: true,
		userOK: true, userUnlisted: false,
		videoFiles: []VideoFileRef{{Kind: "original", StorageKey: origKey}},
	}
	svc := New(repo, lk, blobs, publicFake, twoTierConfig(privateFake))

	if err := svc.SyncVideo(ctx, vid); err != nil {
		t.Fatalf("SyncVideo (private publish): %v", err)
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	cid := repo.rows[origKey].Cid
	if pinned, _ := privateFake.IsPinned(ctx, cid); !pinned {
		t.Fatal("private node not holding the private video's bytes after publish")
	}

	// Flip to public.
	lk.videoPrivacy = "public"
	if err := svc.SyncVideo(ctx, vid); err != nil {
		t.Fatalf("SyncVideo (flip public): %v", err)
	}
	// Drain to convergence (the worker loops until nothing is due): private→public needs
	// two passes — the private unpin re-arms the row public AFTER this pass's public leg,
	// so the public pin lands on the next pass.
	drainToConvergence(t, ctx, svc)
	if repo.state(origKey) != "pinned" || normalizeNetwork(repo.rows[origKey].Network) != networkPublic {
		t.Fatalf("after transition: state=%q net=%q, want pinned public", repo.state(origKey), repo.rows[origKey].Network)
	}
	if pinned, _ := privateFake.IsPinned(ctx, cid); pinned {
		t.Error("private node still holds the bytes after the video went public")
	}
	if privateFake.UnpinCount != 1 {
		t.Errorf("private UnpinCount = %d, want 1", privateFake.UnpinCount)
	}
	// Now the public read model surfaces the public CID (the fence is selective).
	out, ok, err := svc.VideoPins(ctx, vid)
	if err != nil || !ok || out.OriginalCID != repo.rows[origKey].Cid {
		t.Errorf("VideoPins = %+v ok=%v err=%v, want the public CID surfaced", out, ok, err)
	}
}

// TestFlipToPrivateRacingInFlightPublicPinNeverPinsPublic is the cardinal race the
// fix_plan mandates: a flip to private lands WHILE the public Add is in flight. It must
// resolve as unpin-then-re-arm — the just-added public bytes are removed and the row is
// re-armed onto the private swarm — never leaving a private object durably pinned public.
func TestFlipToPrivateRacingInFlightPublicPinNeverPinsPublic(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	vid := uuid.New()
	origKey := "web-videos/" + vid.String() + ".mp4"
	putBlob(t, blobs, origKey, "secret-video")
	pubCID := ipfs.RawLeafCIDv1([]byte("secret-video"))

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, blobs, publicFake, twoTierConfig(privateFake))

	seedPendingNet(repo, origKey, string(ClassVideoOriginal), networkPublic)

	// Mid-Add on the PUBLIC node, a privacy flip routes the SAME row to the private
	// swarm (unpin public → re-arm private). AddHook fires in exactly that window.
	publicFake.AddHook = func() {
		_, _ = repo.RouteIPFSPinIntent(ctx, sqlcgen.RouteIPFSPinIntentParams{
			ObjectKey: origKey, MediaClass: string(ClassVideoOriginal),
			VideoID: pgUUID(vid), TargetNetwork: networkPrivate,
		})
	}

	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	publicFake.AddHook = nil

	// The public node must NOT durably hold the raced pin.
	if pinned, _ := publicFake.IsPinned(ctx, pubCID); pinned {
		t.Fatal("public node durably pinned a now-private object (cardinal invariant breach)")
	}
	if publicFake.UnpinCount != 1 {
		t.Errorf("public UnpinCount = %d, want 1 (the raced public pin was removed)", publicFake.UnpinCount)
	}
	// The row ended up bound for — and pinned on — the private swarm (the same drain's
	// private leg picks up the re-armed row).
	if repo.state(origKey) != "pinned" || normalizeNetwork(repo.rows[origKey].Network) != networkPrivate {
		t.Fatalf("final: state=%q net=%q, want pinned on private", repo.state(origKey), repo.rows[origKey].Network)
	}
	if privateFake.AddCount != 1 {
		t.Errorf("private AddCount = %d, want 1 (replicated to the private swarm after the flip)", privateFake.AddCount)
	}
	if publicFake.AddCount != 1 {
		t.Errorf("public AddCount = %d, want 1 (the single raced add, never re-added)", publicFake.AddCount)
	}
}

// TestUnlistedOwnerImageRoutesToPrivate: with the private tier on, an unlisted owner's
// avatar routes to the PRIVATE swarm (not merely unpinned-from-public as in the default
// config) — pinned via the private client, the public client untouched.
func TestUnlistedOwnerImageRoutesToPrivate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	userID := uuid.New()
	key := "avatars/users/" + userID.String() + ".png"
	putBlob(t, blobs, key, "avatar")

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{userActive: true, userUnlisted: true, userOK: true}, blobs, publicFake, twoTierConfig(privateFake))

	if err := svc.EnqueueUserImage(ctx, userID, ClassUserAvatar, key); err != nil {
		t.Fatalf("EnqueueUserImage: %v", err)
	}
	row := repo.rows[key]
	if row == nil || normalizeNetwork(row.Network) != networkPrivate || row.State != "pending" {
		t.Fatalf("unlisted owner's avatar routed to %+v, want a pending PRIVATE row", row)
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if privateFake.AddCount != 1 {
		t.Errorf("private AddCount = %d, want 1", privateFake.AddCount)
	}
	if publicFake.AddCount != 0 {
		t.Errorf("public AddCount = %d, want 0 (an unlisted owner's image never pins public)", publicFake.AddCount)
	}
}

// TestReferenceCheckScopedPerNetwork: two ledger rows carry the SAME CID (identical
// bytes) but live on DIFFERENT swarms. Unpinning the public row must remove the bytes
// from the PUBLIC node — the private row sharing the CID is a DISTINCT physical pin on a
// distinct node and must NOT keep the public unpin from happening (and vice versa).
func TestReferenceCheckScopedPerNetwork(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, blobs, publicFake, twoTierConfig(privateFake))

	cid := ipfs.RawLeafCIDv1([]byte("shared-across-swarms"))
	pubKey := "web-videos/pub.mp4"
	privKey := "web-videos/priv.mp4"
	seedPinnedNet(repo, pubKey, string(ClassVideoOriginal), cid, networkPublic, uuid.Nil)
	seedPinnedNet(repo, privKey, string(ClassVideoOriginal), cid, networkPrivate, uuid.Nil)

	// Unpin the PUBLIC row: the per-network reference check sees zero OTHER public rows
	// sharing the CID (the private row is excluded), so the public node IS unpinned.
	_ = repo.EnqueueIPFSUnpin(ctx, pubKey)
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain public unpin: %v", err)
	}
	if repo.state(pubKey) != "unpinned" {
		t.Fatalf("public row state = %q, want unpinned", repo.state(pubKey))
	}
	if publicFake.UnpinCount != 1 {
		t.Errorf("public UnpinCount = %d, want 1 (per-network ref check must not be blocked by the private row)", publicFake.UnpinCount)
	}
	if privateFake.UnpinCount != 0 {
		t.Errorf("private UnpinCount = %d, want 0 (the private pin is untouched)", privateFake.UnpinCount)
	}
	// The private row still holds its (distinct) pin.
	if repo.state(privKey) != "pinned" {
		t.Errorf("private row state = %q, want pinned (unaffected by the public unpin)", repo.state(privKey))
	}
}

// TestDeleteUnpinsOnHoldingNetwork: deleting a private video unpins its rows on the
// swarm that actually holds them (the private node), never the public client.
func TestDeleteUnpinsOnHoldingNetwork(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	vid := uuid.New()
	origKey := "web-videos/" + vid.String() + ".mp4"
	cid := ipfs.RawLeafCIDv1([]byte("priv-del"))

	publicFake := ipfs.NewFakeIPFSClient()
	privateFake := ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, blobs, publicFake, twoTierConfig(privateFake))

	seedPinnedNet(repo, origKey, string(ClassVideoOriginal), cid, networkPrivate, vid)

	if err := svc.UnpinVideo(ctx, vid); err != nil {
		t.Fatalf("UnpinVideo: %v", err)
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("drain delete: %v", err)
	}
	if repo.state(origKey) != "unpinned" {
		t.Fatalf("row state = %q, want unpinned", repo.state(origKey))
	}
	if privateFake.UnpinCount != 1 {
		t.Errorf("private UnpinCount = %d, want 1 (delete unpins on the holding swarm)", privateFake.UnpinCount)
	}
	if publicFake.UnpinCount != 0 {
		t.Errorf("public UnpinCount = %d, want 0 (the public client never touches a private row)", publicFake.UnpinCount)
	}
}
