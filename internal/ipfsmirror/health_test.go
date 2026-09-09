package ipfsmirror

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
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
