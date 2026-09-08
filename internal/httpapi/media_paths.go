package httpapi

import (
	"path"
	"strings"

	"github.com/vidra/vidra-core/internal/mediaroute"
)

// The media route PATH grammar lives in internal/mediaroute, because the purge
// queue's worker builds these paths in a process where httpapi.Server does not
// exist (cmd/api skips constructing it at VIDRA_ROLE=worker). What stays here
// is the half that is genuinely this package's: the aliases below, so no call
// site had to change, and hlsTreeRelForKey, which is the HLS route's OWN
// admission test and is written in this package's route regexes.
//
// TestMediaPathsMatchTheirRoutes still lives here and still asserts every
// builder against its route registration — the assertion has to run where the
// router is.

// apiBasePath is the prefix every REST route is registered under (server.go's
// api group).
const apiBasePath = mediaroute.APIBase

var (
	videoMediaPath             = mediaroute.Video
	videoHLSChildPath          = mediaroute.HLSChild
	videoRenditionDownloadPath = mediaroute.RenditionDownload
	userImagePath              = mediaroute.UserImage
	channelImagePath           = mediaroute.ChannelImage
	playlistCoverPath          = mediaroute.PlaylistCover
)

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
