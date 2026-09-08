package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// promoteGeneration swaps the recorded ladder to generation n and writes that
// generation's objects, leaving every earlier generation's objects in place —
// which is exactly the state a re-transcode leaves behind until mediagc sweeps.
func promoteGeneration(t *testing.T, srv *Server, repo *transcodeFakeRepo, videoID string, n int) (masterKey string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := media.HLSPrefixForGeneration(id, n)
	repo.playlists[id] = sqlcgen.StreamingPlaylist{
		VideoID:   id,
		MasterKey: prefix + "/master.m3u8",
		State:     "ready",
		UpdatedAt: time.Now().Add(time.Duration(n) * time.Second),
	}
	repo.renditions[id] = []sqlcgen.VideoRendition{
		{ID: uuid.New(), VideoID: id, Height: 240, Width: 320, KeyPrefix: prefix + "/240p"},
	}
	put := func(key, content string) {
		if _, err := srv.media.Put(context.Background(), key, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	put(prefix+"/master.m3u8", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=985600,RESOLUTION=320x240\n240p/playlist.m3u8\n")
	put(prefix+"/240p/playlist.m3u8", "#EXTM3U\n#EXTINF:2.0,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
	put(prefix+"/240p/seg_00000.ts", "generation-"+string(rune('0'+n))+"-bytes")
	return prefix + "/master.m3u8"
}

// firstChildURI returns the first relative child URI the api wrote into the
// playlist at path — the URL a player will actually request, ?v= tag and all.
func firstChildURI(t *testing.T, srv *Server, path string) string {
	t.Helper()
	rec := getWith(srv, path, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d; body=%s", path, rec.Code, rec.Body.String())
	}
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no child URI in %s:\n%s", path, rec.Body.String())
	return ""
}

// segmentURIOfLadder walks master -> variant and returns the SEGMENT URI the
// player ends up requesting, which is the only one an edge ever caches (the two
// playlists above it are never redirected).
func segmentURIOfLadder(t *testing.T, srv *Server, id string) (variant, segment string) {
	t.Helper()
	variant = firstChildURI(t, srv, "/api/v1/videos/"+id+"/hls/master.m3u8")
	segment = firstChildURI(t, srv, "/api/v1/videos/"+id+"/hls/240p/"+strings.TrimPrefix(variant, "240p/"))
	return variant, segment
}

// TestNewGenerationURLsNeverCollideWithTheOldOnes is the stale-segment
// invariant, asserted at the delivery layer where the URLs are minted.
//
// An edge caches by URL. So the only thing that makes a generation-addressed
// re-transcode safe behind a cache is that the URLs the api emits for the new
// generation are DISJOINT from the ones it emitted for the old — in the object
// key, in the ?v= tag, and therefore in the edge URL built from them. If any
// one of the three repeated, the edge could answer a new-generation request
// from an old-generation entry, which is precisely what A32/A33 measured
// happening (the stale 25 fps chunk decoding against the new 24 fps init
// segment, with no error anywhere).
//
// The live proof is the edge simulator, and that is slice 3's re-run; this is
// the property the simulator would be checking, made a unit test so it cannot
// regress between lab runs.
func TestNewGenerationURLsNeverCollideWithTheOldOnes(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	_ = blobs
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	oldMasterKey := promoteGeneration(t, srv, tcRepo, id, 1)
	oldVariant, oldChild := segmentURIOfLadder(t, srv, id)
	oldSegment := getWith(srv, "/api/v1/videos/"+id+"/hls/240p/"+oldChild, "", "")
	if oldSegment.Code != http.StatusTemporaryRedirect {
		t.Fatalf("generation 1 segment = %d, want a 307 to the edge", oldSegment.Code)
	}
	oldEdgeURL := oldSegment.Header().Get("Location")

	// The re-transcode: a new generation is promoted, the previous one's objects
	// are still in the store (mediagc collects them later).
	newMasterKey := promoteGeneration(t, srv, tcRepo, id, 2)
	newVariant, newChild := segmentURIOfLadder(t, srv, id)
	newSegment := getWith(srv, "/api/v1/videos/"+id+"/hls/240p/"+newChild, "", "")
	if newSegment.Code != http.StatusTemporaryRedirect {
		t.Fatalf("generation 2 segment = %d, want a 307 to the edge", newSegment.Code)
	}
	newEdgeURL := newSegment.Header().Get("Location")

	if oldMasterKey == newMasterKey {
		t.Errorf("both generations promoted the same master key %q", oldMasterKey)
	}
	if oldVariant == newVariant {
		t.Errorf("the master playlist points at the same variant URL across generations: %q", oldVariant)
	}
	if oldChild == newChild {
		t.Errorf("the variant playlist points at the same segment URL across generations: %q", oldChild)
	}
	if oldEdgeURL == newEdgeURL {
		t.Fatalf("both generations map to the same edge URL %q; an edge cannot tell them apart", oldEdgeURL)
	}

	// The other direction, which is the one that actually bites: an OLD URL must
	// never be answered with the NEW generation's bytes. The api refuses it
	// outright rather than serving whatever now sits at that route.
	stale := getWith(srv, "/api/v1/videos/"+id+"/hls/240p/"+oldChild, "", "")
	if stale.Code != http.StatusNotFound {
		t.Errorf("an old generation's URL answered %d, want 404", stale.Code)
	}
	if cc := stale.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("the refusal was cached as %q; a 404 under an immutable directive is permanent", cc)
	}

	// And the ?v= tag really is what separates them, rather than the path.
	oldPath, oldQuery, _ := strings.Cut(oldChild, "?")
	newPath, newQuery, _ := strings.Cut(newChild, "?")
	if oldPath != newPath {
		t.Errorf("child paths differ (%q vs %q); this test would pass for the wrong reason", oldPath, newPath)
	}
	if oldQuery == "" || oldQuery == newQuery {
		t.Errorf("generation tags did not move: %q -> %q", oldQuery, newQuery)
	}
}
