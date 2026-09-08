// Package mediaroute is the ONE place a media route's PATH is written down as a
// value rather than as a route registration.
//
// It exists because the CDN's origin is this API (internal/cdn): the edge holds
// its entries under the api's own media URLs, so an invalidation has to name a
// URL, and the code that fires an invalidation runs outside any request — after
// a deletion, a privacy flip, an image replacement — with no echo.Context to
// read a path from. Every builder here is a pure function of ids the caller
// already holds, and each is asserted against its route registration by
// internal/httpapi's TestMediaPathsMatchTheirRoutes so the two cannot drift.
//
// IT IS A PACKAGE RATHER THAN A FILE IN internal/httpapi because the purge
// queue's worker (internal/cdnpurge) has to build these paths too, and it runs
// in a process where no HTTP server exists: cmd/api does not construct
// httpapi.Server at VIDRA_ROLE=worker. A second copy of the grammar in the
// worker is exactly the drift this package prevents — the URL a takedown names
// and the URL a viewer requests must be one construction or a purge silently
// invalidates nothing.
//
// WHAT A PATH IS HERE: rooted, already URL-safe, and carrying whatever query
// makes it the URL a viewer would actually request (the ?v= generation tag on
// an HLS child, ?audio=false on the video-only rendition download). It is NOT
// escaped again downstream — see cdn.edgeSuffix — so anything that could need
// escaping is escaped here.
package mediaroute

import (
	"net/url"
	"strconv"

	"github.com/google/uuid"
)

// APIBase is the prefix every REST route is registered under (httpapi's api
// group). It lives here so the path builders and the router share one
// definition.
const APIBase = "/api/v1"

// HLSVersionParam is the query parameter carrying an HLS child's generation
// tag. It is the cache key's mutable half: the bytes behind a given ?v= are
// immutable, which is what lets them be cached for a year.
const HLSVersionParam = "v"

// Video builds /api/v1/videos/<id><suffix>. suffix starts with "/".
func Video(videoID uuid.UUID, suffix string) string {
	return APIBase + "/videos/" + videoID.String() + suffix
}

// HLSChild is the route path for one file under a video's streaming tree:
// /api/v1/videos/<id>/hls/<rendition>/<file>, carrying the generation version
// that makes it immutable.
//
// version is the ?v= tag (httpapi's hlsCacheVersion); empty omits the query,
// which is the unversioned compatibility URL. rel is the tree-relative
// "<rendition>/<file>", which is exactly how handleGetHLSFile composes the
// storage key back from the route — the ladder's on-disk layout and its URL
// layout are the same shape, and that is what makes this mapping a rename
// rather than a translation.
func HLSChild(videoID uuid.UUID, rel, version string) string {
	p := Video(videoID, "/hls/"+rel)
	if version == "" {
		return p
	}
	return p + "?" + HLSVersionParam + "=" + url.QueryEscape(version)
}

// RenditionDownload is /api/v1/videos/<id>/download/hls/<height>, with
// includeAudio=false adding the ?audio=false the handler selects on. Both forms
// are separately reachable and therefore separately cached.
func RenditionDownload(videoID uuid.UUID, height int, includeAudio bool) string {
	p := Video(videoID, "/download/hls/"+strconv.Itoa(height))
	if includeAudio {
		return p
	}
	return p + "?audio=false"
}

// UserImage and ChannelImage are the identity-image routes. A channel is
// addressed by HANDLE, not id, so the handle is escaped: it is the one path
// component here that is not a uuid or a fixed word.
func UserImage(userID uuid.UUID, kind string) string {
	return APIBase + "/users/" + userID.String() + "/" + kind
}

// ChannelImage returns "" for an empty handle rather than a path with a hole in
// it: an unaddressable image is one there is nothing to invalidate for, and the
// callers treat "" as exactly that.
func ChannelImage(handle, kind string) string {
	if handle == "" {
		return ""
	}
	return APIBase + "/channels/" + url.PathEscape(handle) + "/" + kind
}

// PlaylistCover is the playlist cover route.
func PlaylistCover(playlistID uuid.UUID) string {
	return APIBase + "/playlists/" + playlistID.String() + "/thumbnail"
}
