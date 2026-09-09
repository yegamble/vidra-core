package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// syncingIPFSMirror records the mirror re-evaluations a moderation decision
// triggers, and what each one reported back for the audit row.
type syncingIPFSMirror struct {
	fakeIPFSMirror
	syncs    []uuid.UUID
	unpinned int
	rearmed  int
}

func (f *syncingIPFSMirror) SyncVideoCounted(_ context.Context, videoID uuid.UUID) (ipfsmirror.SyncResult, error) {
	f.syncs = append(f.syncs, videoID)
	return ipfsmirror.SyncResult{Unpinned: f.unpinned, Rearmed: f.rearmed}, nil
}

func blockIPFSServer(t *testing.T, mirror *syncingIPFSMirror) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSGatewayURL = "https://ipfs.example.org"
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{WithIPFSMirrorService(mirror)})
	return srv
}

// THE RULING (2026-09-09). A31 measured that a block left the video's ledger
// rows 'pinned' and its CIDs retrievable from the public gateway while every
// Vidra surface 404'd it, because a block changes neither privacy nor state and
// the mirror's fence had nothing else to read. The block must now take the same
// unpin path a privacy flip takes, and say so on its audit row.
func TestBlockingAVideoUnpinsItAndUnblockingRePublishesIt(t *testing.T) {
	mirror := &syncingIPFSMirror{unpinned: 5}
	mirror.videoPins = map[uuid.UUID]ipfsmirror.VideoIPFS{}
	srv := blockIPFSServer(t, mirror)

	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createPublishedVideo(t, srv, owner, "bobtube", `{"title":"Clip","privacy":"public"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"tos"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(mirror.syncs) != 1 || mirror.syncs[0].String() != vid {
		t.Fatalf("block triggered %v mirror syncs, want exactly the blocked video", mirror.syncs)
	}

	// Lifting it re-arms — the half A31 said "needs a path that does not exist
	// today".
	mirror.rearmed = 5
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(mirror.syncs) != 2 {
		t.Fatalf("unblock triggered %d total syncs, want 2", len(mirror.syncs))
	}

	// The route is idempotent and a second DELETE lifted nothing, so it must not
	// re-arm a mirror nothing changed.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("second unblock = %d", rec.Code)
	}
	if len(mirror.syncs) != 2 {
		t.Errorf("a no-op unblock re-armed the mirror: %d syncs", len(mirror.syncs))
	}
}

// A mirror error must never fail a takedown: the block has already landed, and
// the eligibility sweep converges the ledger on the same facts regardless.
func TestBlockSucceedsOnAnInstanceWithNoMirror(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createPublishedVideo(t, srv, owner, "bobtube", `{"title":"Clip","privacy":"public"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"tos"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block with no mirror wired = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock with no mirror wired = %d", rec.Code)
	}
}
