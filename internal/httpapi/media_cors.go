package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
)

// Cross-origin access to PUBLIC media (A29 remediation, owner ruling
// 2026-09-08).
//
// THE PROBLEM THIS SOLVES. A federated Video object now carries a playable HLS
// master URL, and the follower instance renders its OWN player against it. That
// player runs on the follower's origin and fetches bytes from this one, so
// every such fetch is cross-origin: without an Access-Control-Allow-Origin
// header the browser refuses to hand hls.js the response, and federation
// degrades to the link-out A29 measured. Emitting a stream link and refusing
// the CORS header would be shipping a URL that cannot work.
//
// THE RULE, IN ONE SENTENCE: a media response gets `Access-Control-Allow-Origin: *`
// exactly when it is PUBLIC (the route's own eligibility assertion — the same
// boolean internal/delivery uses to decide whether the bytes may be redirected
// or shared-cached) and NON-CREDENTIALED (no ?pt=, no Authorization, no
// video-read cookie in use). Private, unlisted, password-gated and credentialed
// responses carry no CORS header whatsoever, which is the A08 posture unchanged.
//
// WHERE IT IS APPLIED. Every place delivery.CacheControl is applied, and
// nowhere else — serveMediaAsset's api-proxy branch, setHLSCacheControl and
// setMediaCacheControl. Sharing the seam with the cache policy is the point:
// both answers derive from the same (eligible, credentialed) pair, so they
// cannot drift into a response that is publicly cacheable but not publicly
// readable, or the reverse.
//
// WHAT IS DELIBERATELY NOT COVERED:
//
//   - THE 307 ITSELF. A presigned or CDN redirect carries no CORS header. A
//     browser following a redirect re-runs the CORS check against the FINAL
//     response, so the header that matters is the bucket's or the edge's, not
//     this one; adding it to the redirect would only advertise an access the
//     redirect target may not honour. (The edge's own origin fetch is a
//     different matter — that request reaches the api-proxy branch and DOES get
//     the header, which is exactly how the shared cache entry ends up carrying
//     it for every cross-origin player behind that edge.)
//   - Vary: Origin. The value is a constant, not a reflection of the request's
//     Origin, so the response does not vary by it. (Echo's CORS middleware adds
//     a Vary: Origin of its own to every response it sees; that is pre-existing
//     and untouched here.)

// setMediaCORS applies the public-media CORS policy to a response that is about
// to carry media bytes. eligible is the route's own "these bytes are servable to
// an anonymous public visitor" assertion — the same value handed to
// delivery.Request.Eligible.
//
// It NEVER overwrites a header the CORS middleware already set. That middleware
// answers an ALLOW-LISTED browser origin with that exact origin plus
// Access-Control-Allow-Credentials, which is what makes cookie-mode auth work
// cross-origin; replacing it with a wildcard would break every credentialed
// media read from the instance's own frontend, because a browser rejects `*`
// together with credentials.
func setMediaCORS(c echo.Context, eligible bool) {
	header := c.Response().Header()
	if header.Get(echo.HeaderAccessControlAllowOrigin) != "" {
		return
	}
	if v := delivery.AllowOrigin(eligible, credentialedMediaRequest(c)); v != "" {
		header.Set(echo.HeaderAccessControlAllowOrigin, v)
	}
}

// Preflight support for the media routes.
//
// hls.js fetches CMAF byte ranges with a `Range` header, which is not a
// CORS-safelisted request header, so a cross-origin player's segment request is
// preceded by an OPTIONS preflight in the browsers that do not have the
// response cached. A32 measured ZERO preflights for the same-origin-then-307
// case — correctly, because same-origin requests never preflight — which is
// exactly why this has to be added deliberately rather than assumed present.
const (
	mediaPreflightMethods = "GET, HEAD"
	mediaPreflightHeaders = "Range"
	// mediaPreflightMaxAge lets a browser reuse one preflight for ten minutes,
	// so a ladder of segment requests costs one OPTIONS rather than one per
	// segment. Short enough that turning a video private is not shadowed by a
	// stale preflight for long — and the preflight grants no access anyway: the
	// GET behind it is authorised on its own.
	mediaPreflightMaxAge = "600"
)

// mediaPreflight PRE-SEEDS the public-media preflight answer for the media
// routes, and must be registered BEFORE the echo CORS middleware.
//
// WHY PRE-SEED RATHER THAN ANSWER. Echo's CORS middleware terminates every
// preflight itself (it answers 204 without calling the route, deliberately, so
// a preflight never traverses auth middleware). It sets its headers with Set,
// so when the browser's Origin IS on the operator's allow-list it OVERWRITES
// everything here with the credentialed answer — an explicit origin plus
// Access-Control-Allow-Credentials — which is the answer cookie-mode media
// reads need. When the Origin is NOT allow-listed it returns the same 204
// having set nothing, leaving these headers in place: the wildcard answer a
// federated player needs. One middleware, both cases, and no duplicate of the
// allow-list matcher.
//
// It is scoped to the media routes by the MATCHED ROUTE TEMPLATE, not by string
// surgery on the URL. Echo's router fills c.Path() even when the method does not
// match (it is what produces the Allow header), so an OPTIONS on a GET-only
// media route still identifies itself.
func mediaPreflight() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Method != http.MethodOptions || !isPublicMediaRoute(c.Path()) {
				return next(c)
			}
			header := c.Response().Header()
			header.Set(echo.HeaderAccessControlAllowOrigin, delivery.PublicMediaOrigin)
			header.Set(echo.HeaderAccessControlAllowMethods, mediaPreflightMethods)
			header.Set(echo.HeaderAccessControlAllowHeaders, mediaPreflightHeaders)
			header.Set(echo.HeaderAccessControlMaxAge, mediaPreflightMaxAge)
			return next(c)
		}
	}
}

// publicMediaRoutes is every route template that serves stored media bytes
// under the ordinary public visibility gate — the routes whose responses can
// legitimately carry `Access-Control-Allow-Origin: *`, and therefore the routes
// whose preflight must be answerable.
//
// It is an explicit set rather than a prefix rule because the answer is an
// authorization-adjacent one: /videos/:id/download is the download MANIFEST
// (JSON, not bytes) and /videos/:id/captions is the caption LIST, and neither
// belongs here. A route absent from this set simply keeps today's behaviour —
// no CORS header, no preflight answer — which is the safe direction to fail.
var publicMediaRoutes = func() map[string]bool {
	set := make(map[string]bool)
	for _, suffix := range []string{
		// HLS ladder: master, variant playlists, segments, the CMAF tree.
		"/videos/:id/hls/master.m3u8",
		"/videos/:id/hls/:rendition/:file",
		// Whole-file playback and official downloads.
		"/videos/:id/original",
		"/videos/:id/webm",
		"/videos/:id/download/original",
		"/videos/:id/download/webm",
		"/videos/:id/download/audio",
		"/videos/:id/download/hls/:height",
		"/videos/:id/download/subtitles/:lang",
		// Per-video small assets.
		"/videos/:id/thumbnail",
		"/videos/:id/storyboard.jpg",
		"/videos/:id/storyboard.vtt",
		"/videos/:id/captions/:lang",
		// Live HLS: the same shape, served from the ingest spool.
		"/live/:id/hls/master.m3u8",
		"/live/:id/hls/:file",
		// Identity images. A federated card renders the origin channel's avatar
		// the same way it renders the poster.
		"/users/:id/avatar",
		"/users/:id/banner",
		"/channels/:handle/avatar",
		"/channels/:handle/banner",
		"/playlists/:id/thumbnail",
		"/remote-videos/:id/thumbnail",
		"/instance/avatar",
		"/instance/banner",
		"/instance/logo/:type",
	} {
		set[apiBasePath+suffix] = true
	}
	return set
}()

// isPublicMediaRoute reports whether a matched route template serves public
// media bytes. The empty path (no route matched at all) is never one.
func isPublicMediaRoute(routePath string) bool {
	if routePath == "" {
		return false
	}
	return publicMediaRoutes[strings.TrimSuffix(routePath, "/")]
}
