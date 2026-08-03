package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// This file classifies routes by SHAPE rather than by feature, because two
// pieces of middleware need the same distinction and must never drift apart:
//
//   - the rate limiter (rateLimit): a media GET is a cheap, cacheable blob read
//     that a single page view fans out ~20 of, so it cannot share the API's
//     per-minute budget;
//   - the request deadline (requestDeadline): a media GET or an upload PUT is
//     bounded by the CLIENT'S LINK SPEED, not by server work, so it cannot share
//     the API's 30s deadline.
//
// The sets hold Echo route TEMPLATES (c.Path()), never raw URLs — matching on
// the template is exact and allocation-free, and it cannot be fooled by a
// crafted path. Every entry must correspond to a live registration in routes();
// TestMediaRouteTemplatesAreRegistered proves that, so a renamed route fails the
// gate instead of silently losing its exemption.

// mediaRouteTemplates are the GET routes that stream stored blobs: thumbnails
// and other page-fan-out images, HLS playlists/segments, storyboards, captions,
// originals and downloads. They get the media rate-limit budget and the long
// request deadline.
//
// Deliberately EXCLUDED: GET /api/v1/videos/:id/download (a JSON listing of the
// available downloads, not a blob) and every write route.
var mediaRouteTemplates = map[string]struct{}{
	// Page-fan-out images. A feed render requests one per card, plus the
	// channel/instance branding on every page.
	"/api/v1/videos/:id/thumbnail":        {},
	"/api/v1/playlists/:id/thumbnail":     {},
	"/api/v1/remote-videos/:id/thumbnail": {},
	"/api/v1/users/:id/avatar":            {},
	"/api/v1/users/:id/banner":            {},
	"/api/v1/channels/:handle/avatar":     {},
	"/api/v1/channels/:handle/banner":     {},
	"/api/v1/instance/avatar":             {},
	"/api/v1/instance/banner":             {},
	"/api/v1/instance/logo/:type":         {},

	// Playback. HLS adds roughly ten segment GETs a minute per viewer on top of
	// the playlist reads, and the player prefetches in bursts.
	"/api/v1/videos/:id/hls/master.m3u8":      {},
	"/api/v1/videos/:id/hls/:rendition/:file": {},
	"/api/v1/videos/:id/storyboard.jpg":       {},
	"/api/v1/videos/:id/storyboard.vtt":       {},
	"/api/v1/videos/:id/captions/:lang":       {},
	"/api/v1/videos/:id/original":             {},
	"/api/v1/live/:id/hls/master.m3u8":        {},
	"/api/v1/live/:id/hls/:file":              {},

	// Downloads: a single response can be the whole source file, so these are the
	// ones the 30s deadline was truncating outright.
	"/api/v1/videos/:id/download/original":        {},
	"/api/v1/videos/:id/download/webm":            {},
	"/api/v1/videos/:id/download/audio":           {},
	"/api/v1/videos/:id/download/hls/:height":     {},
	"/api/v1/videos/:id/download/subtitles/:lang": {},
	"/api/v1/me/export/download":                  {},
	"/api/v1/attachments/:id":                     {},
}

// uploadRouteTemplates are the routes that receive raw media bytes. They get the
// long request deadline but NOT the media rate-limit budget: an upload is
// expensive, already bounded by UPLOAD_MAX_SIZE and the per-user active-session
// cap, and has no reason to be exempted from the API budget.
//
// Resumable chunks are 8 MiB, which needs a sustained ~2.2 Mbit/s to land inside
// the general 30s deadline — below that, a client retried the same chunk forever.
var uploadRouteTemplates = map[string]struct{}{
	uploadChunkRoutePath:      {}, // PUT  /api/v1/uploads/:upload_id/chunks/:n
	uploadRoutePath:           {}, // POST /api/v1/videos/:id/file
	replaceRoutePath:          {}, // POST /api/v1/videos/:id/replace
	attachmentUploadRoutePath: {}, // POST /api/v1/conversations/:id/attachments
}

// isMediaRoute reports whether the matched route is a media GET. The method
// check matters: POST /videos/:id/thumbnail shares its template with the GET but
// is an owner-only write that belongs on the API budget.
func isMediaRoute(c echo.Context) bool {
	if c.Request().Method != http.MethodGet {
		return false
	}
	_, ok := mediaRouteTemplates[c.Path()]
	return ok
}

// isStreamingRoute reports whether the matched route streams bytes in either
// direction, and therefore takes HTTPStreamRequestTimeout instead of
// HTTPRequestTimeout.
func isStreamingRoute(c echo.Context) bool {
	if isMediaRoute(c) {
		return true
	}
	_, ok := uploadRouteTemplates[c.Path()]
	return ok
}
