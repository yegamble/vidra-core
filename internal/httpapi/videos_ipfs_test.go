package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// ipfsVideoServer builds the full video test server with IPFS_ENABLED and a fake
// mirror wired, so the additive `ipfs` / `ipfs_pinned` serving fields (fix_plan
// P19.3) are exercised end to end through the real handlers.
func ipfsVideoServer(t *testing.T, mirror *fakeIPFSMirror) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSGatewayURL = "https://ipfs.example.org"
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{WithIPFSMirrorService(mirror)})
	return srv
}

// TestVideoDetailIPFSObject: a public+published video whose original is pinned
// exposes the additive `ipfs` object (original_cid + gateway_url) on the detail
// endpoint.
func TestVideoDetailIPFSObject(t *testing.T) {
	mirror := &fakeIPFSMirror{
		videoPins: map[uuid.UUID]ipfsmirror.VideoIPFS{},
	}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Pinned","privacy":"public"}`)

	// The mirror reports the (now-published) video's original as pinned.
	mirror.videoPins[uuid.MustParse(id)] = ipfsmirror.VideoIPFS{
		OriginalCID: "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		GatewayURL:  "https://ipfs.example.org",
	}

	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.IPFS == nil {
		t.Fatalf("detail ipfs object missing; body=%s", rec.Body.String())
	}
	if v.IPFS.OriginalCID == "" {
		t.Error("ipfs.original_cid empty, want the pinned CID")
	}
	if v.IPFS.GatewayURL != "https://ipfs.example.org" {
		t.Errorf("ipfs.gateway_url = %q, want the configured gateway", v.IPFS.GatewayURL)
	}
}

// TestVideoDetailNoCIDForNonPublic: CIDs are NEVER emitted for a non-public video
// regardless of caller — even the owner fetching their own UNLISTED (link-only)
// video gets no `ipfs` object, even though the mirror (hypothetically) reports it
// pinned. This is the handler's independent privacy fence.
func TestVideoDetailNoCIDForNonPublic(t *testing.T) {
	mirror := &fakeIPFSMirror{videoPins: map[uuid.UUID]ipfsmirror.VideoIPFS{}}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Unlisted","privacy":"unlisted"}`)

	// Force the mirror to (wrongly) claim the unlisted video is pinned.
	mirror.videoPins[uuid.MustParse(id)] = ipfsmirror.VideoIPFS{
		OriginalCID: "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		GatewayURL:  "https://ipfs.example.org",
	}

	// Fetched by the owner (authorized), which can see the unlisted video.
	rec := getVideo(srv, id, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner get unlisted = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.IPFS != nil {
		t.Errorf("unlisted video leaked an ipfs object %+v, want none (privacy invariant)", v.IPFS)
	}
	if v.IPFSPinned {
		t.Error("unlisted video reported ipfs_pinned=true, want false")
	}
}

// TestFeedIPFSPinnedBadge: on the public feed, only the video the mirror reports
// as pinned carries ipfs_pinned=true; the other omits it (false).
func TestFeedIPFSPinnedBadge(t *testing.T) {
	mirror := &fakeIPFSMirror{pinnedIDs: map[uuid.UUID]bool{}}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pinnedID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Pinned","privacy":"public"}`)
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"Unpinned","privacy":"public"}`)
	mirror.pinnedIDs[uuid.MustParse(pinnedID)] = true

	rec := getPath(srv, "/api/v1/videos")
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var feed videoFeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(feed.Videos) != 2 {
		t.Fatalf("feed has %d videos, want 2", len(feed.Videos))
	}
	var sawPinned bool
	for _, v := range feed.Videos {
		if v.ID == pinnedID {
			sawPinned = true
			if !v.IPFSPinned {
				t.Error("pinned video card missing ipfs_pinned=true")
			}
		} else if v.IPFSPinned {
			t.Errorf("unpinned video %s reported ipfs_pinned=true", v.ID)
		}
	}
	if !sawPinned {
		t.Error("pinned video not found in feed")
	}
}

// TestVideoFlowGreenWhenMirrorReportsNothing is the failure-semantics test: with
// the mirror ENABLED but nothing pinned (the node-unreachable analog — no row ever
// reaches 'pinned'), the whole upload → publish → watch → feed flow is fully green
// and ipfs_pinned is false / the ipfs object absent everywhere. IPFS is a mirror
// sidecar; its outage never touches the authoritative serving path.
func TestVideoFlowGreenWhenMirrorReportsNothing(t *testing.T) {
	// Empty maps ⇒ the mirror answers "nothing pinned" for every lookup.
	mirror := &fakeIPFSMirror{
		videoPins: map[uuid.UUID]ipfsmirror.VideoIPFS{},
		pinnedIDs: map[uuid.UUID]bool{},
	}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// upload → publish
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Live","privacy":"public"}`)

	// watch (detail)
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.IPFS != nil {
		t.Errorf("detail carried an ipfs object %+v while nothing is pinned, want none", v.IPFS)
	}
	if v.IPFSPinned {
		t.Error("detail reported ipfs_pinned=true while nothing is pinned")
	}

	// feed
	rec = getPath(srv, "/api/v1/videos")
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var feed videoFeedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &feed)
	for _, card := range feed.Videos {
		if card.IPFSPinned {
			t.Errorf("feed card %s reported ipfs_pinned=true while nothing is pinned", card.ID)
		}
	}
	// The raw JSON must not carry the fields at all (omitempty) when not pinned.
	if body := rec.Body.String(); jsonHasKey(t, body, "ipfs_pinned") {
		t.Errorf("feed JSON leaked ipfs_pinned while nothing is pinned: %s", body)
	}
}

// jsonHasKey reports whether the literal key appears in the JSON body (a cheap
// omitempty assertion).
func jsonHasKey(t *testing.T, body, key string) bool {
	t.Helper()
	var anyv interface{}
	if err := json.Unmarshal([]byte(body), &anyv); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	return containsKey(anyv, key)
}

func containsKey(v interface{}, key string) bool {
	switch tv := v.(type) {
	case map[string]interface{}:
		if _, ok := tv[key]; ok {
			return true
		}
		for _, val := range tv {
			if containsKey(val, key) {
				return true
			}
		}
	case []interface{}:
		for _, val := range tv {
			if containsKey(val, key) {
				return true
			}
		}
	}
	return false
}
