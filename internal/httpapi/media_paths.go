package httpapi

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// This file is the ONE place a media route's PATH is written down as a value
// rather than as a route registration.
//
// It exists because the CDN's origin is this API (internal/cdn): the edge holds
// its entries under the api's own media URLs, so an invalidation has to name a
// URL, and the code that fires an invalidation runs outside any request — after
// a deletion, a privacy flip, an image replacement — with no echo.Context to
// read a path from. Every builder below is a pure function of ids the caller
// already holds, and each is asserted against its route registration by
// TestMediaPathsMatchTheirRoutes so the two cannot drift.
//
// WHAT A PATH IS HERE: rooted, already URL-safe, and carrying whatever query
// makes it the URL a viewer would actually request (the ?v= generation tag on
// an HLS child, ?audio=false on the video-only rendition download). It is NOT
// escaped again downstream — see cdn.edgeSuffix — so anything that could need
// escaping is escaped here.

// apiBasePath is the prefix every REST route is registered under (server.go's
// api group). It lives here so the path builders and the router share one
// definition.
const apiBasePath = "/api/v1"

// videoMediaPath builds /api/v1/videos/<id><suffix>. suffix starts with "/".
func videoMediaPath(videoID uuid.UUID, suffix string) string {
	return apiBasePath + "/videos/" + videoID.String() + suffix
}

// videoHLSChildPath is the route path for one file under a video's streaming
// tree: /api/v1/videos/<id>/hls/<rendition>/<file>, carrying the generation
// version that makes it immutable.
//
// version is the ?v= tag (hlsCacheVersion); empty omits the query, which is the
// unversioned compatibility URL. rel is the tree-relative "<rendition>/<file>",
// which is exactly how handleGetHLSFile composes the storage key back from the
// route — the ladder's on-disk layout and its URL layout are the same shape,
// and that is what makes this mapping a rename rather than a translation.
func videoHLSChildPath(videoID uuid.UUID, rel, version string) string {
	p := videoMediaPath(videoID, "/hls/"+rel)
	if version == "" {
		return p
	}
	return p + "?" + hlsVersionParam + "=" + url.QueryEscape(version)
}

// videoRenditionDownloadPath is /api/v1/videos/<id>/download/hls/<height>, with
// includeAudio=false adding the ?audio=false the handler selects on. Both forms
// are separately reachable and therefore separately cached.
func videoRenditionDownloadPath(videoID uuid.UUID, height int, includeAudio bool) string {
	p := videoMediaPath(videoID, "/download/hls/"+strconv.Itoa(height))
	if includeAudio {
		return p
	}
	return p + "?audio=false"
}

// userImagePath and channelImagePath are the identity-image routes. A channel
// is addressed by HANDLE, not id, so the handle is escaped: it is the one path
// component here that is not a uuid or a fixed word.
func userImagePath(userID uuid.UUID, kind string) string {
	return apiBasePath + "/users/" + userID.String() + "/" + kind
}

func channelImagePath(handle, kind string) string {
	if handle == "" {
		return ""
	}
	return apiBasePath + "/channels/" + url.PathEscape(handle) + "/" + kind
}

// playlistCoverPath is the playlist cover route.
func playlistCoverPath(playlistID uuid.UUID) string {
	return apiBasePath + "/playlists/" + playlistID.String() + "/thumbnail"
}

// hlsTreeRelForKey maps a storage key under a video's streaming prefix back to
// the "<rendition>/<file>" the HLS route serves it at. ok=false means the edge
// cannot be holding this object at all.
//
// It is deliberately the ROUTE's own admission test rather than a looser
// "anything under the prefix": handleGetHLSFile answers 404 for every name
// outside these patterns, so a purge for one would name a URL that has never
// existed. Two further exclusions, both correctness rather than tidiness:
//
//   - PLAYLISTS AND MANIFESTS ARE NOT AT THE EDGE. delivery.Redirectable is
//     false for an m3u8 and serveCMAFManifest keeps the .mpd on the origin, so
//     no edge URL for one is ever minted.
//   - THE DOWNLOAD DERIVATIVES ARE NOT HERE. A rendition's progressive MP4 and
//     the audio.m4a live INSIDE the ladder's directory but are served by the
//     /download routes, so they are named by videoRenditionDownloadPath and
//     videoMediaPath instead — at the URL the edge actually holds them under.
func hlsTreeRelForKey(prefix, key string, peertube bool) (string, bool) {
	rel, ok := strings.CutPrefix(key, strings.TrimSuffix(prefix, "/")+"/")
	if !ok || rel == "" {
		return "", false
	}
	if peertube {
		if strings.Contains(rel, "/") || !hlsPeerTubeFileName.MatchString(rel) || !redirectableHLSChild(rel) {
			return "", false
		}
		return "peertube/" + rel, true
	}
	rendition, file, found := strings.Cut(rel, "/")
	if !found || strings.Contains(file, "/") {
		return "", false
	}
	canonical := hlsRenditionName.MatchString(rendition) && hlsFileName.MatchString(file)
	cmaf := rendition == hlsCMAFRendition && hlsCMAFFileName.MatchString(file)
	if (!canonical && !cmaf) || !redirectableHLSChild(file) {
		return "", false
	}
	return rel, true
}

// redirectableHLSChild reports whether a file under the streaming tree is one
// the delivery resolver may redirect — opaque media bytes, not a manifest.
func redirectableHLSChild(file string) bool {
	switch path.Ext(file) {
	case ".m3u8", ".mpd":
		return false
	default:
		return true
	}
}
