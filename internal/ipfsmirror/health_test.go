package ipfsmirror

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeGateway stands in for a real gateway's answer about one CID. It records
// what it was asked for, because a probe that does not ask for the URL shape
// delivery mints is health data about a path no viewer takes.
type fakeGateway struct {
	err  error
	cids []string
}

func (g *fakeGateway) Fetch(_ context.Context, cid string) error {
	g.cids = append(g.cids, cid)
	return g.err
}

// A serving gateway makes the component ok and the redirect permitted.
func TestProbeHealthReportsAServingGateway(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	gw := &fakeGateway{}
	cfg := testConfig()
	cfg.Gateway = gw
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, cfg)
	cid := seedPinnedOnNode(t, repo, client, "thumbnails/a.jpg", "bytes")

	svc.ProbeHealth(context.Background())
	h := svc.GatewayHealth()
	if h.State != HealthOK || !h.Redirectable() {
		t.Fatalf("health = %+v, want ok and redirectable", h)
	}
	if len(gw.cids) != 1 || gw.cids[0] != cid {
		t.Errorf("probed %v, want the one CID the ledger publishes (%s)", gw.cids, cid)
	}
	if h.Pinned != 1 {
		t.Errorf("pinned = %d, want the ledger fact to ride with the verdict", h.Pinned)
	}
}

// THE A31 FAILURE. The node's RPC answers — the fake client is up — and the
// GATEWAY does not. The old probe read healthy here and the api kept minting
// 307s that died for five minutes per URL.
func TestProbeHealthReportsADeadGatewayBehindALiveNode(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.Gateway = &fakeGateway{err: errors.New("connection refused")}
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, cfg)
	seedPinnedOnNode(t, repo, client, "thumbnails/a.jpg", "bytes")

	// The node itself is perfectly reachable, which is the whole point.
	if _, err := client.Version(context.Background()); err != nil {
		t.Fatalf("fixture: the node must be UP for this test to mean anything: %v", err)
	}

	svc.ProbeHealth(context.Background())
	h := svc.GatewayHealth()
	if h.State != HealthDown || h.Redirectable() {
		t.Fatalf("health = %+v, want down and NOT redirectable", h)
	}
	if h.Reason == "" {
		t.Error("a down verdict must carry the sentence an operator acts on")
	}
}

// An unprobed monitor is not evidence: it must refuse the redirect rather than
// assume the gateway is fine.
func TestUnprobedHealthIsNotRedirectable(t *testing.T) {
	cfg := testConfig()
	cfg.Gateway = &fakeGateway{}
	svc := New(newFakeRepo(), &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), cfg)
	if h := svc.GatewayHealth(); h.Redirectable() {
		t.Fatalf("an unprobed monitor is redirectable: %+v", h)
	}
}

// Nothing pinned yet means there is no CID to ask for, so there is no verdict to
// take. Claiming "ok" here is the same invention the RPC probe was making.
func TestProbeHealthTakesNoVerdictWithAnEmptyLedger(t *testing.T) {
	gw := &fakeGateway{}
	cfg := testConfig()
	cfg.Gateway = gw
	svc := New(newFakeRepo(), &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), cfg)

	svc.ProbeHealth(context.Background())
	h := svc.GatewayHealth()
	if h.State != HealthNotConfigured || h.Probed || h.Redirectable() {
		t.Fatalf("health = %+v, want an untaken verdict", h)
	}
	if len(gw.cids) != 0 {
		t.Errorf("the gateway was asked for %v with nothing pinned", gw.cids)
	}
}

// A mirror with no gateway URL pins bytes nobody can fetch. Not a fault, but it
// must never be reported as a working delivery source.
func TestProbeHealthReportsNoGatewayAsNotConfigured(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.GatewayURL = ""
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, cfg)
	seedPinnedOnNode(t, repo, client, "thumbnails/a.jpg", "bytes")

	svc.ProbeHealth(context.Background())
	if h := svc.GatewayHealth(); h.State != HealthNotConfigured || h.Redirectable() {
		t.Fatalf("health = %+v, want not_configured", h)
	}
}

// ---- the block fence ------------------------------------------------------

// A moderator block unpins on the PUBLIC swarm, exactly as a privacy flip does.
// A31 measured the opposite: the rows stayed pinned and the CIDs stayed
// retrievable from the gateway while every Vidra surface 404'd the video.
func TestSyncVideoUnpinsABlockedVideo(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	videoID := uuid.New()
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true,
		userActive: true, userOK: true,
		videoFiles: []VideoFileRef{{Kind: "original", StorageKey: "web-videos/v.mp4"}},
	}
	svc := New(repo, lk, newBlobs(t), client, testConfig())

	if err := svc.SyncVideo(context.Background(), videoID); err != nil {
		t.Fatalf("SyncVideo (eligible): %v", err)
	}
	if got := repo.state("web-videos/v.mp4"); got != "pending" {
		t.Fatalf("before the block the row is %q, want pending", got)
	}

	// The block lands. Neither privacy nor state changes — that is the whole
	// reason the fence could not see one before.
	lk.videoBlocked = true
	res, err := svc.SyncVideoCounted(context.Background(), videoID)
	if err != nil {
		t.Fatalf("SyncVideoCounted (blocked): %v", err)
	}
	if got := repo.state("web-videos/v.mp4"); got != "unpinning" {
		t.Errorf("a blocked video's row is %q, want unpinning", got)
	}
	if res.Unpinned != 1 {
		t.Errorf("unpinned = %d, want 1 for the audit row", res.Unpinned)
	}
	if lk.videoPrivacy != "public" || lk.videoState != "published" {
		t.Error("fixture bug: a block must not change privacy or state")
	}
}

// Lifting the block re-arms the mirror so the CIDs are published again — the
// half A31 said "needs a path that does not exist today".
func TestSyncVideoRearmsAfterAnUnblock(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	videoID := uuid.New()
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true, videoBlocked: true,
		userActive: true, userOK: true,
		videoFiles: []VideoFileRef{{Kind: "original", StorageKey: "web-videos/v.mp4"}},
	}
	svc := New(repo, lk, newBlobs(t), client, testConfig())
	if _, err := svc.SyncVideoCounted(context.Background(), videoID); err != nil {
		t.Fatalf("SyncVideoCounted (blocked): %v", err)
	}

	lk.videoBlocked = false
	res, err := svc.SyncVideoCounted(context.Background(), videoID)
	if err != nil {
		t.Fatalf("SyncVideoCounted (unblocked): %v", err)
	}
	if got := repo.state("web-videos/v.mp4"); got != "pending" {
		t.Errorf("after the unblock the row is %q, want pending so the worker re-publishes it", got)
	}
	if res.Rearmed != 1 {
		t.Errorf("rearmed = %d, want 1 for the audit row", res.Rearmed)
	}
	if got := repo.network("web-videos/v.mp4"); got != networkPublic {
		t.Errorf("re-armed on the %q swarm, want public", got)
	}
}

// A block routes to NO swarm, not to the private one. Replicating blocked
// content onto the private swarm would keep publishing what a moderator withdrew.
func TestRouteRefusesABlockedVideoOnBothSwarms(t *testing.T) {
	for _, privacy := range []string{"public", "private", "unlisted"} {
		got := Route(Subject{
			Class: ClassVideoOriginal, VideoPrivacy: privacy,
			VideoState: statePublished, VideoBlocked: true,
		})
		if got != NetworkNone {
			t.Errorf("Route(privacy=%s, blocked) = %q, want NetworkNone", privacy, got)
		}
	}
}

// The HLS TREE must come back too, and A31's rehearsal measured that it does not.
//
// A block unpins every row; the worker drains each to the terminal 'unpinned'.
// The single-file classes are re-armed on the unblock because videoMirrorRefs
// enumerates them from video_files and routePin re-claims whatever state the row
// is in. The HLS tree is not in that enumeration by design — it is a directory
// intent armed by OnTranscodeComplete — so it fell to the "leave a terminal row
// alone" rule and stayed unpinned for the life of the instance. The class the
// watch page's IPFS playback actually uses was the one class an unblock did not
// restore: 4 of 5 rows re-pinned, the video still unplayable from the gateway.
func TestSyncVideoRearmsTheHLSTreeAfterAnUnblock(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	videoID := uuid.New()
	hlsKey := media.HLSKeyPrefix(videoID) + "/"
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true,
		userActive: true, userOK: true,
		videoFiles: []VideoFileRef{{Kind: "original", StorageKey: "web-videos/v.mp4"}},
	}
	svc := New(repo, lk, newBlobs(t), client, testConfig())

	// A finished transcode is what arms the tree; there is no other path.
	if err := svc.OnTranscodeComplete(context.Background(), videoID); err != nil {
		t.Fatalf("OnTranscodeComplete: %v", err)
	}
	if got := repo.state(hlsKey); got != "pending" {
		t.Fatalf("fixture: the HLS row is %q, want pending", got)
	}

	lk.videoBlocked = true
	if _, err := svc.SyncVideoCounted(context.Background(), videoID); err != nil {
		t.Fatalf("SyncVideoCounted (blocked): %v", err)
	}
	// The worker finishes the unpin — the state the live lab was in when the
	// moderator lifted the block a minute later.
	if err := repo.MarkIPFSPinUnpinned(context.Background(), hlsKey); err != nil {
		t.Fatalf("MarkIPFSPinUnpinned: %v", err)
	}
	if err := repo.MarkIPFSPinUnpinned(context.Background(), "web-videos/v.mp4"); err != nil {
		t.Fatalf("MarkIPFSPinUnpinned (original): %v", err)
	}
	if got := repo.state(hlsKey); got != "unpinned" {
		t.Fatalf("fixture: the HLS row is %q, want the terminal unpinned", got)
	}

	lk.videoBlocked = false
	res, err := svc.SyncVideoCounted(context.Background(), videoID)
	if err != nil {
		t.Fatalf("SyncVideoCounted (unblocked): %v", err)
	}
	if got := repo.state(hlsKey); got != "pending" {
		t.Errorf("after the unblock the HLS tree is %q, want pending — without it the video is unplayable from the gateway", got)
	}
	if got := repo.network(hlsKey); got != networkPublic {
		t.Errorf("the HLS tree was re-armed on the %q swarm, want public", got)
	}
	if res.Rearmed != 2 {
		t.Errorf("rearmed = %d, want 2 (the original and the HLS tree) for the audit row", res.Rearmed)
	}
}

// A superseded generation's row must NOT be resurrected by the same rule. Only
// the video's CURRENT transcode-output keys come back; an orphan key left by an
// earlier run stays terminal, which is what releaseSupersededTranscodePins put
// it there for.
func TestSyncVideoLeavesASupersededTranscodeRowTerminal(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	videoID := uuid.New()
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true, videoBlocked: true,
		userActive: true, userOK: true,
		videoFiles: []VideoFileRef{{Kind: "original", StorageKey: "web-videos/v.mp4"}},
	}
	svc := New(repo, lk, newBlobs(t), client, testConfig())

	stale := media.HLSPrefixForGeneration(videoID, 1) + "/vp9.webm"
	if _, err := repo.UpsertIPFSPinIntent(context.Background(), sqlcgen.UpsertIPFSPinIntentParams{
		ObjectKey: stale, MediaClass: string(ClassWebM), VideoID: pgUUID(videoID),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.EnqueueIPFSUnpin(context.Background(), stale); err != nil {
		t.Fatalf("seed unpin: %v", err)
	}
	if err := repo.MarkIPFSPinUnpinned(context.Background(), stale); err != nil {
		t.Fatalf("seed terminal: %v", err)
	}

	lk.videoBlocked = false
	if _, err := svc.SyncVideoCounted(context.Background(), videoID); err != nil {
		t.Fatalf("SyncVideoCounted: %v", err)
	}
	if got := repo.state(stale); got != "unpinned" {
		t.Errorf("the superseded generation's row is %q, want it left at the terminal unpinned", got)
	}
}

// ---- the stray count, in the process that RENDERS it ----------------------
//
// A31's rehearsal, INT-07 clause (e). `unaccounted_node_pins` was a per-process
// sync.Map written only by VerifyPins — which runs under runWorkers behind the
// leader lock — and read by this process's probe. On the topology
// docker-compose.prod.yml renders, the api role serves /readyz and /admin/system
// and runs no workers, so its map was never written and the detail was absent
// forever; a single-process positive control rendered it, which is how the role
// split was identified as the cause. These tests pin the fix at the seam that
// broke: a process that never sweeps must still produce the number.

// THE REGRESSION. Nothing here ever calls VerifyPins — this Service is the api
// role — and the count still reaches the health record.
func TestProbeHealthCountsStraysInAProcessThatNeverSweeps(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.Gateway = &fakeGateway{}
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, cfg)
	seedPinnedOnNode(t, repo, client, "thumbnails/ours.jpg", "ours")
	if _, err := client.Add(context.Background(), "operators-own.bin", strings.NewReader("not ours")); err != nil {
		t.Fatalf("fake Add: %v", err)
	}

	svc.ProbeHealth(context.Background())

	if got := svc.GatewayHealth().Strays; got != 1 {
		t.Fatalf("unaccounted_node_pins = %d, want 1 from the probe's own comparison", got)
	}
	// The proof that no sweep ran: VerifyPins is the only thing that moves the
	// keyset cursor, and it is still where New left it.
	if c := svc.verifyCursor(networkPublic); c != "" {
		t.Errorf("verify cursor = %q; this fixture must not have swept", c)
	}
}

// The node refusing to list its pins is ABSENT (-1), never 0. "We could not look"
// and "there is nothing unaccounted for" are the two answers A31 caught the old
// reconcile conflating by silence, and the renderer can only distinguish them if
// the probe does.
func TestProbeHealthReportsStraysAbsentWhenTheNodeWillNotList(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.Gateway = &fakeGateway{}
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, cfg)
	seedPinnedOnNode(t, repo, client, "thumbnails/ours.jpg", "ours")

	client.Down = true
	svc.ProbeHealth(context.Background())

	if got := svc.GatewayHealth().Strays; got != -1 {
		t.Fatalf("unaccounted_node_pins = %d with the node refusing pin/ls, want -1 (absent)", got)
	}
}

// A Vidra pin is never a stray — not while it is pinned, and not during the
// windows in which the worker is mid-flight on it. Reporting one would fire on
// every drain and teach an operator to ignore the number.
func TestProbeHealthNeverCountsAVidraPinAsAStray(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.Gateway = &fakeGateway{}
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, cfg)

	seedPinnedOnNode(t, repo, client, "thumbnails/pinned.jpg", "pinned-bytes")
	seedPinnedOnNode(t, repo, client, "thumbnails/pending.jpg", "pending-bytes")
	repo.rows["thumbnails/pending.jpg"].State = "pending"
	seedPinnedOnNode(t, repo, client, "thumbnails/leaving.jpg", "leaving-bytes")
	repo.rows["thumbnails/leaving.jpg"].State = "unpinning"

	svc.ProbeHealth(context.Background())
	if got := svc.GatewayHealth().Strays; got != 0 {
		t.Fatalf("unaccounted_node_pins = %d with only Vidra pins on the node, want 0", got)
	}

	// And it does find a real one, so the 0 above is a verdict rather than a
	// comparison that silently did nothing.
	if _, err := client.Add(context.Background(), "operators-own.bin", strings.NewReader("not ours")); err != nil {
		t.Fatalf("fake Add: %v", err)
	}
	svc.ProbeHealth(context.Background())
	if got := svc.GatewayHealth().Strays; got != 1 {
		t.Errorf("unaccounted_node_pins = %d after a real stray appeared, want 1", got)
	}
}

// The private tier gets its own comparison against its OWN node. A count taken
// from the public node would be a privacy-relevant lie about a swarm that never
// serves viewers.
func TestProbeHealthCountsPrivateStraysAgainstThePrivateNode(t *testing.T) {
	repo := newFakeRepo()
	pub, priv := ipfs.NewFakeIPFSClient(), ipfs.NewFakeIPFSClient()
	cfg := testConfig()
	cfg.Gateway = &fakeGateway{}
	cfg.PrivateEnabled = true
	cfg.PrivateClient = priv
	svc := New(repo, &fakeLookups{}, newBlobs(t), pub, cfg)

	// One accounted-for private pin, one stray on the private node, nothing on the
	// public one.
	res, err := priv.Add(context.Background(), "web-videos/p.mp4", strings.NewReader("private-bytes"))
	if err != nil {
		t.Fatalf("fake Add: %v", err)
	}
	repo.rows["web-videos/p.mp4"] = &sqlcgen.MediaIpfsPin{
		ObjectKey: "web-videos/p.mp4", MediaClass: string(ClassVideoOriginal),
		Cid: res.CID, State: "pinned", Network: networkPrivate,
	}
	if _, err := priv.Add(context.Background(), "outsider.bin", strings.NewReader("not ours")); err != nil {
		t.Fatalf("fake Add: %v", err)
	}

	svc.ProbeHealth(context.Background())
	if got := svc.PrivateHealth().Strays; got != 1 {
		t.Errorf("private unaccounted_node_pins = %d, want 1", got)
	}
	if got := svc.GatewayHealth().Strays; got != 0 {
		t.Errorf("public unaccounted_node_pins = %d, want 0 — the private node's pins are not the public node's", got)
	}
}
