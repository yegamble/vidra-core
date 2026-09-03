package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/cdn"
	"github.com/vidra/vidra-core/internal/storage"
)

// These tests are the phase-4 carry-forward gate in executable form
// (docs/productionization/phase-5-enterprise.md, work item 3): "wire and
// exercise Purge call sites (delete + privacy flip); nothing may become
// shared-cacheable before it". Before this file the seam was fully built and
// had ZERO call sites, so an operator with a CDN could delete a video, flip it
// private or block it and the edge would keep serving every byte.
//
// What each test asserts is the KEY SET, not merely "purge was called". A purge
// that fires with the wrong keys is worse than none: it looks like a working
// takedown in the logs and leaves the media at the edge.

// purgeRecorder is a delivery.CDNPurge that records what it was asked to
// invalidate. It is the CDN half of the seam, so the tests can assert on keys
// without an HTTP server or a vendor.
type purgeRecorder struct {
	mu       sync.Mutex
	keys     []string
	failWith error
}

func (p *purgeRecorder) purge(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, key)
	return p.failWith
}

func (p *purgeRecorder) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]string(nil), p.keys...)
	sort.Strings(out)
	return out
}

// testCDNPurge wires a real internal/cdn provider for the edge lookup and the
// recorder for purge, exactly as cmd/api wires the provider's two methods —
// as an Option, so every harness (video, profile-image, …) can mount the same
// pair.
func testCDNPurge(t *testing.T) (Option, *purgeRecorder) {
	t.Helper()
	provider, err := cdn.New(cdn.Config{BaseURL: testCDNBase}, nil)
	if err != nil {
		t.Fatalf("cdn.New: %v", err)
	}
	rec := &purgeRecorder{}
	return WithDeliveryCDN(provider.EdgeURL, rec.purge), rec
}

// purgeServer is the full video harness with the CDN purge recorder mounted.
//
// The delivery_cdn_enabled toggle is left OFF deliberately: purge is NOT gated
// by it (see delivery.WithCDN), because switching delivery off evicts nothing
// the edge is already holding. A test that only passed with the toggle on would
// be asserting the wrong contract.
func purgeServer(t *testing.T) (*Server, storage.Backend, *transcodeFakeRepo, *purgeRecorder) {
	t.Helper()
	opt, rec := testCDNPurge(t)
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{opt})
	return srv, blobs, tcRepo, rec
}

// wantVideoPurgeKeys is the exact set publishedPublicVideo leaves behind that a
// CDN edge could be holding: the two video_files-recorded whole-file objects
// plus every object in the HLS tree.
func wantVideoPurgeKeys(id string) []string {
	hls := "streaming-playlists/" + id
	want := []string{
		"thumbnails/" + id + ".jpg",
		"web-videos/" + id + ".mp4",
		hls + "/240p/iframe.m3u8",
		hls + "/240p/iframe.ts",
		hls + "/240p/playlist.m3u8",
		hls + "/240p/seg_00000.ts",
		hls + "/240p/video-only.mp4",
		hls + "/240p/video.mp4",
		hls + "/audio.m4a",
		hls + "/master.m3u8",
	}
	sort.Strings(want)
	return want
}

// waitForPurge waits for the detached purge to finish. The purge is fired after
// the state change commits and never blocks the handler, so the response
// arriving is not the signal that it ran.
func waitForPurge(t *testing.T, rec *purgeRecorder, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = rec.snapshot()
		if len(got) >= len(want) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != len(want) {
		t.Fatalf("purged %d keys, want %d:\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("purged keys differ at %d: got %q, want %q\n got=%v\nwant=%v", i, got[i], want[i], got, want)
		}
	}
}

// assertNoPurge asserts nothing was invalidated. It waits first, because a
// purge that fires late is still a purge — and a regression here would be a
// silent one.
func assertNoPurge(t *testing.T, rec *purgeRecorder) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("purged %v, want nothing", got)
	}
}

// TestDeleteVideoPurgesEdgeCopies. A deleted video whose bytes are still cached
// at the edge is still a published video to anyone holding a URL — the row is
// gone and the media is not.
func TestDeleteVideoPurgesEdgeCopies(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id, tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}

// TestPrivacyFlipAwayFromPublicPurgesEdgeCopies. delivery.Request.Eligible is
// public AND published, so a video leaving that state is exactly the moment
// every shared copy of it became unauthorized.
func TestPrivacyFlipAwayFromPublicPurgesEdgeCopies(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"privacy":"private"}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}

// TestUnlistedIsAlsoAFlipAwayFromPublic. Unlisted is not public, so it is not
// Eligible, so the edge must stop serving it too.
func TestUnlistedIsAlsoAFlipAwayFromPublic(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"privacy":"unlisted"}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}

// TestOrdinaryEditDoesNotPurge. The trigger is the loss of eligibility, not the
// PATCH: a title edit purges nothing, or every metadata save would cold-start
// the edge for the whole ladder.
func TestOrdinaryEditDoesNotPurge(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"title":"Renamed"}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
}

// TestBlockVideoPurgesEdgeCopies. A block is the takedown case the purge
// endpoint exists for; unlike the privacy flip it changes neither privacy nor
// state, so nothing about the row says the edge must be evicted.
func TestBlockVideoPurgesEdgeCopies(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	// The first registered account is the instance admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, admin)

	if r := doJSON(srv, http.MethodPost, "/api/v1/admin/videos/"+id+"/block", admin, `{"reason":"tos"}`); r.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}

// TestDeleteChannelPurgesItsVideosEdgeCopies. A channel delete never visits the
// per-video delete handler — the videos vanish at the database (0006 ON DELETE
// CASCADE) — so without a fan-out at the channel handler, "delete the channel"
// would be the one deletion path that leaves every byte of every video at the
// edge, with the rows that name those bytes gone.
func TestDeleteChannelPurgesItsVideosEdgeCopies(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	// A private sibling in the same channel was never edge-eligible; the exact
	// key-set assertion below proves the cascade purge skips it rather than
	// spending purge-API calls on objects the edge cannot hold.
	priv := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if r := uploadVideoFile(srv, priv, "clip.mp4", "video/mp4", "video-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", r.Code, r.Body.String())
	}

	if r := doJSON(srv, http.MethodDelete, "/api/v1/channels/ada", tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete channel = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}

// TestPurgeFailureDoesNotFailTheRequest. Every media response is still
// `private`, so a failed purge invalidates nothing that was ever shared — it
// must not turn a successful deletion into a 5xx the caller retries.
func TestPurgeFailureDoesNotFailTheRequest(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	rec.failWith = errors.New("edge is down")
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id, tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", r.Code, r.Body.String())
	}
	// Every key is still attempted: one rejecting object says nothing about the
	// next one, and stopping early would leave the rest of the ladder cached.
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}

// TestNonPublicVideoIsNeverPurged. Only public+published media ever reached the
// edge (the Eligible fence), so a private video's deletion has nothing to
// invalidate — and asking would spend a purge-API call per object for nothing.
func TestNonPublicVideoIsNeverPurged(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if r := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "video-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", r.Code, r.Body.String())
	}
	seedReadyHLS(t, tcRepo, blobs, id)

	if r := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id, tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
}

// TestPurgeSkippedWithoutACDN. With no DELIVERY_CDN_BASE_URL — every install by
// default — there is provably no shared copy, so the snapshot must short-circuit
// before it costs a database read or an object-store listing on the delete path.
func TestPurgeSkippedWithoutACDN(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if snap := srv.videoEdgePurgeSnapshot(context.Background(), uuid.MustParse(id)); !snap.empty() {
		t.Fatalf("snapshot with no CDN = %+v, want empty", snap)
	}
	if r := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id, tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", r.Code, r.Body.String())
	}
}

// wantDownloadPurgeKeys is the subset of a public video's edge-reachable
// objects that the DOWNLOAD gates control: the stored original, and the
// derivatives remuxed out of the finalized HLS tree. Deliberately absent are
// the thumbnail (no download gate applies) and every segment, variant playlist
// and trick-play object in the ladder — closing downloads does not make a
// video unwatchable, so purging the ladder would cold-start playback at the
// edge for no reason.
func wantDownloadPurgeKeys(id string) []string {
	hls := "streaming-playlists/" + id
	want := []string{
		"web-videos/" + id + ".mp4",
		hls + "/240p/video-only.mp4",
		hls + "/240p/video.mp4",
		hls + "/audio.m4a",
	}
	sort.Strings(want)
	return want
}

// TestClosingTheDownloadGatePurgesDerivatives. publicDownload is a SECOND
// fence, independent of Eligible: a public, published video with
// download_enabled false still serves its ladder and still 403s its master
// file. So revoking downloads leaves the video Eligible — which is why the
// privacy-flip trigger never fired for it — while making objects an anonymous
// visitor could previously fetch unauthorized. Those are exactly the bytes a
// shared cache is still holding, at a stable and publicly derivable key.
func TestClosingTheDownloadGatePurgesDerivatives(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"download_enabled":false}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantDownloadPurgeKeys(id))
}

// TestOpeningTheDownloadGateDoesNotPurge. The trigger is the LOSS of download
// eligibility. Turning downloads ON authorises bytes rather than revoking
// them, so there is nothing at the edge that has become wrong.
func TestOpeningTheDownloadGateDoesNotPurge(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"download_enabled":false}`); r.Code != http.StatusOK {
		t.Fatalf("close = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantDownloadPurgeKeys(id))
	rec.mu.Lock()
	rec.keys = nil
	rec.mu.Unlock()

	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"download_enabled":true}`); r.Code != http.StatusOK {
		t.Fatalf("open = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
}

// TestDownloadGateFlipAwayFromPublicStillPurgesEverything. When a PATCH closes
// the download gate AND leaves public in the same request, the privacy trigger
// wins: the whole ladder became unauthorized, not just the derivatives.
func TestDownloadGateFlipAwayFromPublicStillPurgesEverything(t *testing.T) {
	srv, blobs, tcRepo, rec := purgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	body := `{"download_enabled":false,"privacy":"private"}`
	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, body); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, wantVideoPurgeKeys(id))
}
