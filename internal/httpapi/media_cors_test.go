package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
)

// A29 remediation: cross-origin access to PUBLIC media.
//
// The matrix these tests pin is the owner's ruling of 2026-09-08: public,
// non-credentialed media answers `Access-Control-Allow-Origin: *`; private,
// unlisted, unpublished and credentialed responses answer with no CORS header
// at all. It is the same (eligible, credentialed) pair the cache policy is
// derived from, so a response can never be publicly cacheable without being
// publicly readable, or the reverse.

const testCrossOrigin = "https://follower.example.test"

// corsHeader is the Access-Control-Allow-Origin of a response.
func corsHeader(rec *httptest.ResponseRecorder) string {
	return rec.Header().Get(echo.HeaderAccessControlAllowOrigin)
}

// getCrossOrigin issues a GET carrying an Origin header from an instance that
// is NOT on the operator's CORS allow-list — a federated follower's player.
func getCrossOrigin(srv *Server, path, token, playbackToken string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	if playbackToken != "" {
		sep := "?"
		if strings.ContainsRune(path, '?') {
			sep = "&"
		}
		path += sep + playbackTokenParam + "=" + playbackToken
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(echo.HeaderOrigin, testCrossOrigin)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestPublicMediaCarriesWildcardCORS walks every media route a federated player
// touches and asserts the wildcard is present on the BYTES.
func TestPublicMediaCarriesWildcardCORS(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	for _, path := range []string{
		"/api/v1/videos/" + id + "/hls/master.m3u8",
		"/api/v1/videos/" + id + "/hls/master.m3u8?v=" + version,
		"/api/v1/videos/" + id + "/hls/240p/playlist.m3u8",
		"/api/v1/videos/" + id + "/hls/240p/seg_00000.ts",
		"/api/v1/videos/" + id + "/thumbnail",
		"/api/v1/videos/" + id + "/original",
		"/api/v1/videos/" + id + "/download/original",
	} {
		rec := getCrossOrigin(srv, path, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		if got := corsHeader(rec); got != delivery.PublicMediaOrigin {
			t.Errorf("%s Access-Control-Allow-Origin = %q, want %q", path, got, delivery.PublicMediaOrigin)
		}
		// A wildcard origin and credentials are mutually exclusive: the browser
		// rejects the pair, so this must never appear alongside the wildcard.
		if got := rec.Header().Get(echo.HeaderAccessControlAllowCredentials); got != "" {
			t.Errorf("%s Access-Control-Allow-Credentials = %q on a wildcard response", path, got)
		}
	}
}

// TestNonPublicMediaCarriesNoCORS is the negative half of the matrix, and it
// deliberately reuses the shapes A08's "credentialed → no-store, no redirect"
// test already covers: every response that is not shared-cacheable is also not
// cross-origin readable.
func TestNonPublicMediaCarriesNoCORS(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	pubID := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	privID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadThumbnail(srv, privID, "p.png", "image/png", "private-poster", tok); rec.Code != http.StatusCreated {
		t.Fatalf("private thumbnail = %d; body=%s", rec.Code, rec.Body.String())
	}
	unlistedID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Quiet","privacy":"unlisted"}`)
	if rec := uploadThumbnail(srv, unlistedID, "p.png", "image/png", "unlisted-poster", tok); rec.Code != http.StatusCreated {
		t.Fatalf("unlisted thumbnail = %d; body=%s", rec.Code, rec.Body.String())
	}
	draftID := createVideo(t, srv, tok, "ada", `{"title":"Draft","privacy":"public"}`)
	if rec := uploadThumbnail(srv, draftID, "p.png", "image/png", "draft-poster", tok); rec.Code != http.StatusCreated {
		t.Fatalf("draft thumbnail = %d; body=%s", rec.Code, rec.Body.String())
	}

	tests := []struct {
		name  string
		path  string
		token string
	}{
		{"private video thumbnail on the owner path", "/api/v1/videos/" + privID + "/thumbnail", tok},
		{"unlisted video thumbnail", "/api/v1/videos/" + unlistedID + "/thumbnail", ""},
		{"unpublished public video thumbnail", "/api/v1/videos/" + draftID + "/thumbnail", tok},
		{"public media requested with an Authorization header", "/api/v1/videos/" + pubID + "/original", tok},
		{"public HLS master with an Authorization header", "/api/v1/videos/" + pubID + "/hls/master.m3u8", tok},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := getCrossOrigin(srv, tt.path, tt.token, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if got := corsHeader(rec); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want none", got)
			}
		})
	}
}

// TestPlaybackTokenMediaCarriesNoCORS: a ?pt= credential is the password gate's
// bearer token. Its response is no-store AND unreadable cross-origin.
func TestPlaybackTokenMediaCarriesNoCORS(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "hunter2secret")
	seedReadyHLS(t, tcRepo, blobs, id)
	pt := unlockToken(t, srv, id, "hunter2secret")

	rec := getCrossOrigin(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", pt)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheNoStore {
		t.Errorf("Cache-Control = %q, want %q", cc, delivery.CacheNoStore)
	}
	if got := corsHeader(rec); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q on a ?pt= response, want none", got)
	}
}

// TestPresignedRedirectCarriesNoCORS: the 307 itself is not a CORS-bearing
// response. A browser re-runs the check against the redirect's TARGET, so the
// bucket's policy decides; advertising an access the bucket may refuse would be
// a lie the api cannot honour.
func TestPresignedRedirectCarriesNoCORS(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	rec := getCrossOrigin(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", "")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body=%s", rec.Code, rec.Body.String())
	}
	if got := corsHeader(rec); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q on a presigned 307, want none", got)
	}
}

// TestEdgeOriginFetchCarriesCORS: the edge's own origin fetch takes the
// api-proxy path, so the shared cache entry it stores carries the wildcard —
// which is what makes a cross-origin player work THROUGH the edge as well as
// against the origin. The pairing with the shared cache policy is the point.
func TestEdgeOriginFetchCarriesCORS(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	path := "/api/v1/videos/" + id + "/hls/240p/seg_00000.ts?" +
		delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue
	rec := getWith(srv, path, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheSharedStableRevalidate {
		t.Errorf("edge Cache-Control = %q, want %q", cc, delivery.CacheSharedStableRevalidate)
	}
	if got := corsHeader(rec); got != delivery.PublicMediaOrigin {
		t.Errorf("edge Access-Control-Allow-Origin = %q, want %q", got, delivery.PublicMediaOrigin)
	}
}

// TestEdgeOriginFetchOfNonPublicMediaCarriesNoCORS: the marker is not a
// credential and buys nothing. A non-public object answered on the edge path is
// still private and still unreadable cross-origin.
func TestEdgeOriginFetchOfNonPublicMediaCarriesNoCORS(t *testing.T) {
	srv, _, _ := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadThumbnail(srv, id, "p.png", "image/png", "private-poster", tok); rec.Code != http.StatusCreated {
		t.Fatalf("thumbnail = %d; body=%s", rec.Code, rec.Body.String())
	}
	path := "/api/v1/videos/" + id + "/thumbnail?" +
		delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue
	rec := getWith(srv, path, tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := corsHeader(rec); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q on private media, want none", got)
	}
}

// --- preflight ---------------------------------------------------------------

func optionsRequest(srv *Server, path, origin, requestHeaders string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, path, nil)
	req.Header.Set(echo.HeaderOrigin, origin)
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	if requestHeaders != "" {
		req.Header.Set(echo.HeaderAccessControlRequestHeaders, requestHeaders)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestMediaPreflightAnswersRangeFromAnyOrigin: hls.js sends Range on CMAF byte
// ranges, which is not a safelisted request header, so a cross-origin player
// preflights. A32 measured zero preflights for the same-origin case, which is
// exactly why this had to be added rather than assumed.
func TestMediaPreflightAnswersRangeFromAnyOrigin(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	rec := optionsRequest(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", testCrossOrigin, "range")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := corsHeader(rec); got != delivery.PublicMediaOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, delivery.PublicMediaOrigin)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowMethods); got != mediaPreflightMethods {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, mediaPreflightMethods)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowHeaders); got != mediaPreflightHeaders {
		t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, mediaPreflightHeaders)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowCredentials); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q on a wildcard preflight", got)
	}
}

// TestMediaPreflightKeepsTheAllowListedCredentialedAnswer: the operator's own
// frontend runs cookie-mode auth cross-origin, and its preflight must still get
// an explicit origin plus Allow-Credentials. Pre-seeding the wildcard must not
// have stolen that case.
func TestMediaPreflightKeepsTheAllowListedCredentialedAnswer(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	rec := optionsRequest(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "http://localhost:3000", "range")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := corsHeader(rec); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the allow-listed origin", got)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowCredentials); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

// TestNonMediaRoutePreflightIsUnchanged: the wildcard is scoped to media. A JSON
// route's preflight from an unknown origin still gets nothing, as before.
func TestNonMediaRoutePreflightIsUnchanged(t *testing.T) {
	srv, _, _ := deliveryServer(t, nil, false)
	rec := optionsRequest(srv, "/api/v1/videos", testCrossOrigin, "content-type")
	if got := corsHeader(rec); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q on a JSON route, want none", got)
	}
}

// TestPublicMediaRoutesAreRegistered guards the one thing an explicit route set
// can get wrong: a template that matches no route at all, which would silently
// disable the preflight for that route forever.
func TestPublicMediaRoutesAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, srv := range mediaCORSHarnesses(t) {
		for _, r := range srv.Handler().Routes() {
			registered[r.Path] = true
		}
	}
	for path := range publicMediaRoutes {
		if !registered[path] {
			t.Errorf("publicMediaRoutes names %q, which no server registers", path)
		}
	}
}

// mediaCORSHarnesses builds the servers whose route tables together cover every
// media route (the video harness does not wire profile images, and vice versa).
func mediaCORSHarnesses(t *testing.T) []*Server {
	t.Helper()
	var srvs []*Server
	vsrv, _, _ := deliveryServer(t, nil, false)
	srvs = append(srvs, vsrv)
	srvs = append(srvs, profileImageServerWith(t, testConfig()))
	lsrv, _ := liveHLSServer(t)
	srvs = append(srvs, lsrv)
	rsrv, _ := remoteVideoServer(t)
	srvs = append(srvs, rsrv)
	return srvs
}
