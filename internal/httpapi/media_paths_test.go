package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/delivery"
)

// TestMediaPathsMatchTheirRoutes is the drift guard media_paths.go promises.
//
// Those builders exist because a purge runs outside any request and has to name
// a URL from ids alone. A builder that drifted from its route registration
// would purge a URL that has never existed — and internal/cdn treats an edge's
// 404 as success, so the operator would read "purged" while the stale copy sat
// exactly where it was. Matching them against the router is the only way that
// mistake fails loudly.
func TestMediaPathsMatchTheirRoutes(t *testing.T) {
	// Two harnesses, because the identity-image routes are only registered when
	// the profile-image service is wired and the video routes only when the
	// video service is. A builder must match a route SOMETHING mounts.
	srv, _, _, _, _ := videoServerFull(t, testConfig())
	registered := map[string]bool{}
	for _, s := range []*Server{srv, profileImageServer(t)} {
		for _, r := range s.echo.Routes() {
			if r.Method == http.MethodGet {
				registered[r.Path] = true
			}
		}
	}
	id := uuid.New()
	cases := []struct{ built, route string }{
		{videoMediaPath(id, "/original"), "/api/v1/videos/:id/original"},
		{videoMediaPath(id, "/webm"), "/api/v1/videos/:id/webm"},
		{videoMediaPath(id, "/thumbnail"), "/api/v1/videos/:id/thumbnail"},
		{videoMediaPath(id, "/storyboard.jpg"), "/api/v1/videos/:id/storyboard.jpg"},
		{videoMediaPath(id, "/download/original"), "/api/v1/videos/:id/download/original"},
		{videoMediaPath(id, "/download/webm"), "/api/v1/videos/:id/download/webm"},
		{videoMediaPath(id, "/download/audio"), "/api/v1/videos/:id/download/audio"},
		{videoRenditionDownloadPath(id, 720, true), "/api/v1/videos/:id/download/hls/:height"},
		{videoRenditionDownloadPath(id, 720, false), "/api/v1/videos/:id/download/hls/:height"},
		{videoHLSChildPath(id, "cmaf/chunk-0-00001.m4s", "abc"), "/api/v1/videos/:id/hls/:rendition/:file"},
		{videoHLSChildPath(id, "240p/seg_00000.ts", ""), "/api/v1/videos/:id/hls/:rendition/:file"},
		{userImagePath(id, "avatar"), "/api/v1/users/:id/avatar"},
		{userImagePath(id, "banner"), "/api/v1/users/:id/banner"},
		{channelImagePath("ada", "avatar"), "/api/v1/channels/:handle/avatar"},
		{channelImagePath("ada", "banner"), "/api/v1/channels/:handle/banner"},
		{playlistCoverPath(id), "/api/v1/playlists/:id/thumbnail"},
	}
	for _, tc := range cases {
		if !registered[tc.route] {
			t.Errorf("route %q is not registered; the builder for it is addressing nothing", tc.route)
			continue
		}
		if segs, want := len(strings.Split(pathOnly(tc.built), "/")), len(strings.Split(tc.route, "/")); segs != want {
			t.Errorf("built %q has %d path segments, route %q has %d", tc.built, segs, tc.route, want)
		}
		if !strings.HasPrefix(tc.built, apiBasePath+"/") {
			t.Errorf("built %q does not start with the api base path", tc.built)
		}
	}
}

func pathOnly(u string) string {
	p, _, _ := strings.Cut(u, "?")
	return p
}

// TestEdgeOriginFetchIsServedNotRedirected is the api-as-origin contract at the
// route level, and it is the one failure this topology introduces: the edge
// fetches the SAME url a viewer does, so an api that could not tell them apart
// would answer the edge with a redirect to the edge.
func TestEdgeOriginFetchIsServedNotRedirected(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := cdnServer(t, true, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	mediaPath := "/api/v1/videos/" + id + "/original"
	// A viewer is sent to the edge...
	assertCDNRedirect(t, getWith(srv, mediaPath, "", ""), mediaPath)

	// ...and the edge's own fetch of that very URL gets the bytes.
	edgePath := mediaPath + "?" + delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue
	p.calls = nil
	rec := getWith(srv, edgePath, "", "")
	if rec.Code == http.StatusTemporaryRedirect {
		t.Fatalf("the edge's origin fetch was redirected to %q — that is the loop", rec.Header().Get("Location"))
	}
	assertBytes(t, rec, "video-bytes")
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheSharedLongLived {
		t.Errorf("edge origin fetch Cache-Control = %q, want %q", cc, delivery.CacheSharedLongLived)
	}
	if len(p.calls) != 0 {
		t.Errorf("a presigned URL was minted for the edge (%d calls); that hands the private bucket to the edge", len(p.calls))
	}
}

// TestEdgeOriginFetchStillRunsEveryAuthorizationFence. The api being the origin
// is worth nothing if the origin stops asking. Each miss and each revalidation
// re-runs the route's own checks, which is exactly what a public-read bucket
// origin could not do — measured in the A32/A33 acceptance run, where a correct
// 18-key purge was undone by the next request re-pulling the object from an
// origin that still served it.
func TestEdgeOriginFetchStillRunsEveryAuthorizationFence(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	edge := "?" + delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue

	// Public: bytes, shared policy.
	if rec := getWith(srv, "/api/v1/videos/"+id+"/original"+edge, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("edge fetch of a public video = %d", rec.Code)
	}
	// Flip it private and the edge's very next fetch is refused, exactly as an
	// anonymous viewer's would be.
	if r := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"privacy":"private"}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	rec := getWith(srv, "/api/v1/videos/"+id+"/original"+edge, "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("edge fetch of a now-private video = %d, want 404", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); strings.HasPrefix(cc, "public") {
		t.Errorf("a refused edge fetch answered %q; nothing unauthorized may be shared-cacheable", cc)
	}
}

// TestEdgeMarkerIsInertWithoutACDN. The marker is not a credential and grants
// nothing; on an install with no DELIVERY_CDN_BASE_URL — which is all of them
// by default — it must not change a single byte of a response either.
func TestEdgeMarkerIsInertWithoutACDN(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	plain := getWith(srv, "/api/v1/videos/"+id+"/original", "", "")
	marked := getWith(srv, "/api/v1/videos/"+id+"/original?"+delivery.EdgeOriginParam+"="+delivery.EdgeOriginValue, "", "")
	assertBytes(t, plain, "video-bytes")
	assertBytes(t, marked, "video-bytes")
	if got, want := marked.Header().Get("Cache-Control"), plain.Header().Get("Cache-Control"); got != want {
		t.Errorf("marked Cache-Control = %q, want the unmarked answer %q", got, want)
	}
	if !strings.HasPrefix(marked.Header().Get("Cache-Control"), "private") {
		t.Errorf("Cache-Control = %q; with no edge to purge, nothing may be shared-cacheable", marked.Header().Get("Cache-Control"))
	}
}

// TestVersionedHLSChildIsImmutableAtTheEdge pins the header that makes a
// generation-addressed ladder safe to cache for a year: the ?v= tag is part of
// the URL, so a new transcode generation is a new URL and an old entry can
// never be answered for it.
func TestVersionedHLSChildIsImmutableAtTheEdge(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	version := hlsVersionOf(t, srv, id)
	edge := "&" + delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue

	rec := getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts?v="+version+edge, "", "")
	assertBytes(t, rec, "fake-ts-bytes")
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheSharedVersionedImmutable {
		t.Errorf("versioned segment Cache-Control = %q, want %q", cc, delivery.CacheSharedVersionedImmutable)
	}
	// Unversioned, the same object revalidates every time — which is what keeps
	// it safe at a shared cache with no purge behind it.
	rec = getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts?"+delivery.EdgeOriginParam+"="+delivery.EdgeOriginValue, "", "")
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheSharedStableRevalidate {
		t.Errorf("unversioned segment Cache-Control = %q, want %q", cc, delivery.CacheSharedStableRevalidate)
	}
	// And an OLD generation's tag is refused outright rather than answered with
	// the new generation's bytes.
	rec = getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts?v=stale-generation"+edge, "", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("a stale ?v= answered %d, want 404", rec.Code)
	}
}
