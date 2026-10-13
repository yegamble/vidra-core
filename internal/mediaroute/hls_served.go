package mediaroute

import (
	"path"
	"regexp"
	"strings"
)

// THE SET OF FILES THE HLS ROUTES SERVE, written down once.
//
// internal/httpapi's handleGetHLSFile admits a request only if its
// "<rendition>/<file>" matches one of the shapes below, and the IPFS mirror
// (internal/ipfsmirror) pins exactly those files and nothing else. They used to be
// two lists: the handlers' regexes and a "everything under the generation except
// vp9.webm" in the mirror, which is how the mirror came to pin every rendition's
// progressive video.mp4 / video-only.mp4 and the audio.m4a — files no HLS
// playlist references and no mirror consumer reads, roughly 1.7x the bytes that
// play. A shared definition cannot drift; a hand-maintained second list did.
//
// The names live here rather than in internal/httpapi for the reason the rest of
// this package exists: the mirror runs in the worker role, where no
// httpapi.Server is constructed.

// HLSCMAFRendition is the pseudo-rendition the CMAF directory is served under.
const HLSCMAFRendition = "cmaf"

// HLSPeerTubeRendition is the pseudo-rendition an imported PeerTube tree is
// served under; its files sit flat beside the master.
const HLSPeerTubeRendition = "peertube"

// HLSMasterFilename is the name a native generation's master playlist is stored
// and routed under (/videos/{id}/hls/master.m3u8). A tree's actual master key is
// streaming_playlists.master_key; this is only the fallback for a row that has no
// playlist record to read it from.
const HLSMasterFilename = "master.m3u8"

// PeerTubeHLSKeyPrefix is the layout a PeerTube reference-mode import keeps.
const PeerTubeHLSKeyPrefix = "streaming-playlists/hls/"

// The file shapes reachable under a video's HLS prefix. Anything else —
// including any traversal attempt — is 404.
//
// HLSRenditionName/HLSFileName are the MPEG-TS tree: rendition directories named
// "<height>p" holding a variant playlist, the trick-play pair, and numbered
// segments.
//
// HLSCMAFFileName is the CMAF tree, which lives in ONE directory ("cmaf") under
// the same prefix because HLS and DASH must resolve the SAME segments relative
// to their own manifests. It is deliberately an exhaustive list of the names the
// packager emits rather than a permissive "anything that looks like a media
// file": this route is an authorization boundary, and the only reason the
// PeerTube pass-through gets a loose pattern is that its tree was written by
// another program whose naming Vidra does not control.
//
// The representation index is unbounded on purpose. A tree has one video
// representation per ladder rung PER CODEC (TRANSCODING_HEVC_ENABLED /
// TRANSCODING_AV1_ENABLED) plus the shared audio one, so "how many are there"
// is a packaging decision this must not restate — the names are what is pinned,
// not the count. media's own tests assert every stored object matches this
// pattern.
var (
	HLSRenditionName = regexp.MustCompile(`^[0-9]{2,4}p$`)
	HLSFileName      = regexp.MustCompile(`^(playlist\.m3u8|iframe\.m3u8|iframe\.ts|seg_[0-9]+\.ts)$`)
	HLSCMAFFileName  = regexp.MustCompile(
		`^(stream\.mpd|media_[0-9]+\.m3u8|init-[0-9]+\.mp4|chunk-[0-9]+-[0-9]+\.m4s|iframe-[0-9]+\.m3u8|iframe-[0-9]+\.mp4)$`)
	HLSPeerTubeFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(m3u8|mp4|m4s|ts)$`)
)

// IsPeerTubeHLSMasterKey reports whether a master key (or a tree prefix) is a
// PeerTube reference-mode import's layout.
func IsPeerTubeHLSMasterKey(key string) bool {
	return strings.HasPrefix(key, PeerTubeHLSKeyPrefix)
}

// HLSChildShape classifies the "<rendition>/<file>" of an HLS child route. At most
// one result is true; none means the route answers 404.
func HLSChildShape(rendition, file string) (canonical, cmaf, peertube bool) {
	canonical = HLSRenditionName.MatchString(rendition) && HLSFileName.MatchString(file)
	cmaf = rendition == HLSCMAFRendition && HLSCMAFFileName.MatchString(file)
	peertube = rendition == HLSPeerTubeRendition && HLSPeerTubeFileName.MatchString(file)
	return canonical, cmaf, peertube
}

// HLSServedRel reports whether a path relative to a tree's directory is a file
// the HLS routes serve: the master playlist (masterKey's own file name, falling
// back to HLSMasterFilename when the tree has no playlist record) or a child that
// HLSChildShape admits. An imported PeerTube tree (peertube=true) is flat and
// admitted through the looser PeerTube name pattern; a native tree never is.
//
// It matches the RELATIVE path, never the basename: "720p/video.mp4" is not
// served, and neither is a "video.mp4" that happens to share a name with a served
// file in another directory.
func HLSServedRel(rel, masterKey string, peertube bool) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
		return false
	}
	master := HLSMasterFilename
	if masterKey != "" {
		master = path.Base(masterKey)
	}
	if rel == master {
		return true
	}
	rendition, file, found := strings.Cut(rel, "/")
	if peertube {
		return !found && HLSPeerTubeFileName.MatchString(rel)
	}
	if !found || strings.Contains(file, "/") {
		return false
	}
	canonical, cmaf, _ := HLSChildShape(rendition, file)
	return canonical || cmaf
}
