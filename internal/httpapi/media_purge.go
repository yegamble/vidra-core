package httpapi

import (
	"context"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// This file wires the delivery resolver's Purge hook to the moments an
// edge-cached object becomes wrong.
//
// IT PURGES URLS, NOT KEYS. The CDN's origin is this API (internal/cdn), so the
// edge holds its entries under the api's own media route paths — and one object
// is reachable at more than one of them (an original is both /original and
// /download/original; a rendition's progressive MP4 is /download/hls/720p with
// and without ?audio=false). Enumerating routes rather than keys is therefore
// both what the edge can act on and STRICTLY MORE PRECISE than the key set was:
// closing downloads on a video used to purge the original's key, which evicted
// the playback URL for that same object too and cold-started every viewer to
// enforce a gate that does not apply to playback.
//
// Fan-out purges — a video's whole URL set (purgeVideoEdgeCopies):
//   - deletion, direct (handleDeleteVideo) or via the channel cascade
//     (handleDeleteChannel: the DATABASE deletes the videos, 0006 ON DELETE
//     CASCADE, so the channel handler snapshots them first);
//   - a privacy flip away from public (handleUpdateVideo);
//   - an admin block (handleBlockVideo).
//
// Download-gated purges — the strictly smaller set the SECOND fence controls
// (videoDownloadPurgeSnapshot), fired when a per-video download_enabled flip
// shuts while the video stays public and watchable:
//   - the stored original, the VP9 alternate, every rendition's progressive MP4
//     in both audio-included and video-only form, and audio.m4a. Exact keys
//     only: they live inside the ladder's directory, so a prefix purge would
//     evict the segments and cold-start playback to enforce a gate on
//     four objects.
//
// Single-key purges — assets at ONE stable identity key (purgeEdgeKey):
//   - user/channel avatar and banner replacement and deletion
//     (profile_images.go), including the channel-delete cascade;
//   - public playlist cover replacement/deletion, playlist deletion, and a
//     visibility flip away from public (playlists.go).
//
// STILL UNPURGED — the ledger that gates header promotion; nothing may become
// shared-cacheable while any of these can leave wrong bytes at the edge:
//   - video thumbnail and storyboard replacement: both overwrite their stable
//     key in place with no invalidation;
//   - (CLOSED) the same-generation admin re-transcode. It needed no purge in
//     the end: every transcode run now mints its own generation prefix
//     (media.HLSPrefixForGeneration) and the promotion moves the ?v= tag with
//     it, so the new generation's URLs are new URLs and the edge cannot answer
//     one of them from an old entry. The superseded generation's objects are
//     mediagc's, exactly as a source replacement's already were;
//   - account deletion (internal/account): cascades channels, videos and
//     images away without visiting any of the handlers above;
//   - the INSTANCE-WIDE downloads_enabled toggle: publicDownload is the AND of
//     it and the per-video flag, so closing it globally revokes the same
//     objects on EVERY public video at once. Deliberately not wired: the
//     per-video flip purges four keys, and this would fan out four keys times
//     the whole public catalogue from a single settings write — thousands of
//     third-party HTTP calls off one admin click, with no batching, no
//     progress and no way to stop it. It needs a job with a lease and a
//     resumable cursor (the videoimport/mediagc shape), not a detached
//     goroutine. Until then an operator closing downloads globally must treat
//     the edge as still serving them until TTL.
// (Instance branding images need no entry: they are served through
// serveStoredObjectNamed, never through the resolver, so they cannot be at
// the edge at all.)
//
// WHY IT EXISTS. The purge seam shipped in phase 4 with ZERO call sites, and
// docs/productionization/phase-5-enterprise.md carries that forward as work
// item 3's gate: "wire and exercise Purge call sites (delete + privacy flip);
// nothing may become shared-cacheable before it". Until this file every media
// response was `Cache-Control: private` precisely because an operator with a
// CDN could delete a video and the edge would keep serving every byte of it,
// with nothing in the system even attempting an invalidation. Promoting any
// media header to a shared directive is gated on this working.
//
// WHAT THE PROVIDER CAN ACTUALLY DO. internal/cdn's purge is ONE URL per
// request — a method, a URL template and at most one auth header, which is what
// every CDN's single-URL invalidation API reduces to. There is no prefix, no
// wildcard and no "purge everything under this directory", and inventing one
// here would mean inventing a vendor. So a video's invalidation is a fan-out of
// single-URL purges over the routes that video actually occupies, which is why
// this file enumerates rather than issues one call.
//
// TWO-PHASE, AND THAT IS NOT STYLISTIC. The paths are SNAPSHOTTED BEFORE the
// state change and purged AFTER it commits:
//
//   - before, because deleting a video deletes the rows that name its objects
//     and its ladder generation (mediagc collects the objects themselves much
//     later), and because a video
//     that has already been flipped private no longer answers the question
//     "what could an anonymous visitor have fetched?" — which is exactly the
//     question the edge's contents are the answer to.
//   - after, because a purge that raced the commit could evict a copy and then
//     have the origin repopulate it from a row that had not changed yet.
//
// BEST-EFFORT, ALWAYS. A purge failure is a logged warning and never a failed
// request: nothing is shared-cacheable yet, so a surviving edge copy of a
// deleted object is exactly the state the instance was already in before this
// file existed. Turning a successful deletion into a 5xx would be strictly
// worse than the stale copy it is reporting.

// maxVideoPurgePaths bounds one video's fan-out.
//
// A long video's ladder is thousands of segments, and each purge is a
// third-party HTTP call: unbounded, that is a background task that runs for
// hours and a purge API that starts rate-limiting the ones that matter. The cap
// is high enough to cover an ordinary video whole and low enough that the worst
// case stays a bounded background task.
const maxVideoPurgePaths = 5000

// edgeCacheableVideoFileKinds are the video_files kinds whose objects a CDN
// edge can be holding: every one of them is served through a Redirectable
// delivery.Class under the Eligible fence.
//
// Deliberately absent: "storyboard_vtt" (delivery.Redirectable is false for
// ClassStoryboardVTT — its cues reference the sprite relatively, so it is never
// served from anywhere but the origin and can never be at the edge) and
// captions (they never go through the resolver at all; they are streamed by
// serveMediaAsset's non-resolver sibling).
var edgeCacheableVideoFileKinds = []string{"original", "thumbnail", "webm", "storyboard"}

// videoFileKindPaths maps a video_files kind onto the media route paths that
// kind's object is REACHABLE at. More than one for two of them, which is the
// whole reason this seam moved from keys to URLs: an original answers both the
// playback route and the official download, and each is its own edge entry.
func videoFileKindPaths(videoID uuid.UUID, kind string) []string {
	switch kind {
	case "original":
		return []string{
			videoMediaPath(videoID, "/original"),
			videoMediaPath(videoID, "/download/original"),
		}
	case "webm":
		return []string{
			videoMediaPath(videoID, "/webm"),
			videoMediaPath(videoID, "/download/webm"),
		}
	case "thumbnail":
		return []string{videoMediaPath(videoID, "/thumbnail")}
	case "storyboard":
		return []string{videoMediaPath(videoID, "/storyboard.jpg")}
	default:
		return nil
	}
}

// edgePurgeTree is a streaming tree whose children have to be enumerated from
// storage before they can be named as URLs: an HLS ladder's segments have no
// database rows.
type edgePurgeTree struct {
	// videoID is the route's own id — the tree is addressed by video, not by key.
	videoID uuid.UUID
	// listPrefix is what the storage backend is asked to enumerate. For a
	// Vidra-laid-out video that is the whole per-video tree, EVERY generation
	// included, so a superseded one can be SEEN even though its URLs can no
	// longer be named (see servedPrefix).
	listPrefix string
	// servedPrefix is the promoted generation's own directory — path.Dir of the
	// master key. Only keys under it are reachable at a URL today, because the
	// route resolves every request through the recorded master key.
	servedPrefix string
	// version is the ?v= generation tag the promoted tree's children are
	// requested with, and therefore cached under.
	version string
	// peertube marks an imported tree served through the compatibility
	// pseudo-rendition (/hls/peertube/<basename>).
	peertube bool
}

// edgePurgeSnapshot is what the database knew about a video's edge-reachable
// URLs at the instant BEFORE the change that invalidated them: exact media
// route paths for everything a row names, and listable trees for the ladder.
type edgePurgeSnapshot struct {
	paths []string
	trees []edgePurgeTree
}

func (s edgePurgeSnapshot) empty() bool { return len(s.paths) == 0 && len(s.trees) == 0 }

// cdnConfigured reports whether a CDN was wired at boot at all. It is the cheap
// gate in front of everything below, and it is what keeps this feature free for
// the installs that have no DELIVERY_CDN_BASE_URL — which is all of them by
// default: with no CDN there is provably no shared copy, so a snapshot would
// spend database reads and an object-store listing to invalidate nothing.
func (s *Server) cdnConfigured() bool {
	return s.mediaCDNEdge != nil || s.mediaCDNPurge != nil
}

// videoEdgePurgeSnapshot records what a CDN edge could be holding for videoID.
//
// It returns an EMPTY snapshot for anything that was never edge-reachable, and
// the fence it applies is the resolver's own: delivery.Request.Eligible is
// public AND published, so a private, unlisted, scheduled or quarantined video
// has structurally never been handed to a CDN and has nothing to invalidate.
// Re-deriving it here rather than trusting the caller is the same discipline
// every media route follows (see delivery.go's header comment).
//
// The video_files rows are read through the ANONYMOUS view — FileForView with
// no viewer — on purpose: "what a public visitor could have fetched" is exactly
// "what the edge could be holding", so the two questions have one answer and
// one code path.
func (s *Server) videoEdgePurgeSnapshot(ctx context.Context, videoID uuid.UUID) edgePurgeSnapshot {
	if !s.cdnConfigured() || s.videosvc == nil {
		return edgePurgeSnapshot{}
	}
	v, err := s.videosvc.GetByID(ctx, videoID)
	if err != nil || !publicVideoForIPFS(v.Privacy, v.State) {
		return edgePurgeSnapshot{}
	}
	snap := edgePurgeSnapshot{}
	for _, kind := range edgeCacheableVideoFileKinds {
		f, ferr := s.videosvc.FileForView(ctx, videoID, uuid.Nil, false, kind)
		if ferr != nil || f.StorageKey == "" {
			continue
		}
		snap.paths = append(snap.paths, videoFileKindPaths(videoID, kind)...)
	}
	snap.paths = append(snap.paths, s.videoDerivedDownloadPaths(ctx, videoID)...)
	if tree, ok := s.videoEdgePurgeTree(ctx, videoID); ok {
		snap.trees = append(snap.trees, tree)
	}
	return snap
}

// videoEdgePurgeTree describes the streaming tree the edge could be holding
// children of. Absent when there is no ready ladder to have served.
//
// The web-video progressive MP4s (web-videos/<id>[/rN]/<rung>.mp4) have no
// entry here and need none: no media route serves them, so they can never have
// reached an edge. They were in the key-addressed snapshot only because it
// enumerated a prefix.
func (s *Server) videoEdgePurgeTree(ctx context.Context, videoID uuid.UUID) (edgePurgeTree, bool) {
	if s.transcodesvc == nil {
		return edgePurgeTree{}, false
	}
	sp, ok := s.transcodesvc.Playlist(ctx, videoID)
	if !ok || sp.MasterKey == "" || !strings.Contains(sp.MasterKey, "/") {
		return edgePurgeTree{}, false
	}
	served := path.Dir(sp.MasterKey)
	if served == "." {
		return edgePurgeTree{}, false
	}
	tree := edgePurgeTree{
		videoID:      videoID,
		listPrefix:   media.HLSKeyPrefix(videoID),
		servedPrefix: served,
		version:      hlsCacheVersion(sp),
	}
	// A PeerTube-imported ladder does NOT live under this video's id — it keeps
	// the source instance's layout (streaming-playlists/hls/<source-uuid>/) and
	// is served through the compatibility pseudo-rendition, so the tree to list
	// is the master's own directory and there is no generation beneath it.
	if isPeerTubeHLSMasterKey(sp.MasterKey) {
		tree.listPrefix = served
		tree.peertube = true
	}
	return tree, true
}

// videoDerivedDownloadPaths are the official-download routes whose objects are
// derived from the finalized HLS tree rather than recorded as whole files: the
// per-rendition progressive MP4s in both audio-included and video-only form,
// and the audio-only m4a.
//
// They are named as ROUTES, which is what makes this list short: the objects
// themselves live inside the ladder's directory, and the key-addressed
// predecessor had to enumerate them by key precisely so that a prefix purge
// would not evict the segments alongside them.
func (s *Server) videoDerivedDownloadPaths(ctx context.Context, videoID uuid.UUID) []string {
	if s.transcodesvc == nil {
		return nil
	}
	sp, ok := s.transcodesvc.Playlist(ctx, videoID)
	if !ok || sp.MasterKey == "" {
		return nil
	}
	paths := []string{videoMediaPath(videoID, "/download/audio")}
	for _, rendition := range s.transcodesvc.Renditions(ctx, videoID) {
		if rendition.Height <= 0 {
			continue
		}
		paths = append(paths,
			videoRenditionDownloadPath(videoID, int(rendition.Height), true),
			videoRenditionDownloadPath(videoID, int(rendition.Height), false),
		)
	}
	return paths
}

// videoDownloadPurgeSnapshot records what a CDN edge could be holding for
// videoID that the DOWNLOAD gates authorised.
//
// It is the strictly smaller set the SECOND fence controls: publicDownload
// (downloads.go) is a gate independent of visibility, so these URLs can become
// unauthorized while the video stays public, published and perfectly watchable.
//
// EVERY PATH HERE IS A /download ROUTE, and that is the correctness the move
// from keys to URLs bought. The download gate does not apply to /original or
// /webm — those are playback routes fenced on visibility alone — yet the
// original and the VP9 alternate are the SAME OBJECTS behind both. Purging by
// key therefore evicted the playback URLs too, cold-starting every viewer's
// progressive fallback to enforce a gate that does not touch it.
//
// An EMPTY snapshot when the gates were ALREADY closed is load-bearing, not an
// optimisation: it is what makes a re-close idempotent and what keeps a PATCH
// that merely restates download_enabled:false from firing a purge. The question
// this answers is "what could an anonymous visitor have fetched as a download
// immediately BEFORE this change?", and if downloads were off the answer is
// nothing.
func (s *Server) videoDownloadPurgeSnapshot(ctx context.Context, videoID uuid.UUID) edgePurgeSnapshot {
	if !s.cdnConfigured() || s.videosvc == nil {
		return edgePurgeSnapshot{}
	}
	v, err := s.videosvc.GetByID(ctx, videoID)
	if err != nil || !s.publicDownload(v) {
		return edgePurgeSnapshot{}
	}
	snap := edgePurgeSnapshot{}
	for _, kind := range downloadGatedVideoFileKinds {
		f, ferr := s.videosvc.FileForView(ctx, videoID, uuid.Nil, false, kind)
		if ferr != nil || f.StorageKey == "" {
			continue
		}
		snap.paths = append(snap.paths, videoMediaPath(videoID, "/download/"+kind))
	}
	snap.paths = append(snap.paths, s.videoDerivedDownloadPaths(ctx, videoID)...)
	return snap
}

// downloadGatedVideoFileKinds are the video_files kinds whose objects the
// DOWNLOAD gates control. Their /download/<kind> route names them directly.
//
// "thumbnail" and "storyboard" are edge-cacheable but carry no download gate,
// so they are deliberately absent — closing downloads does not make a poster
// unauthorized.
var downloadGatedVideoFileKinds = []string{"original", "webm"}

// purgeVideoEdgeCopies invalidates a snapshot's URLs at the edge, detached from
// the request.
//
// Detached because a purge is a fan-out of third-party HTTP calls, each bounded
// only by DELIVERY_CDN_PURGE_TIMEOUT (10s by default): holding a deletion open
// for it would make a slow edge look like a broken API. The context is
// WithoutCancel'd for the same reason the live replay hook is
// (handleStopLiveStream) — the work outlives the response by design.
func (s *Server) purgeVideoEdgeCopies(ctx context.Context, videoID uuid.UUID, snap edgePurgeSnapshot) {
	if !s.cdnConfigured() || snap.empty() {
		return
	}
	go s.runVideoEdgePurge(context.WithoutCancel(ctx), videoID, snap)
}

// runVideoEdgePurge issues one purge per URL and reports the outcome ONCE.
//
// One aggregate log line rather than one per URL, and that is the point: a CDN
// configured with no DELIVERY_CDN_PURGE_URL fails every single call (cmd/api
// already warns about that at boot), and a per-URL warning would turn one
// takedown into thousands of identical lines. Neither the path nor the purge URL
// is logged — a purge template is operator-supplied and some APIs carry the
// credential in the query string.
//
// Sequential, not concurrent: purge APIs are rate-limited and a takedown is not
// latency-critical. A rejected URL never stops the loop — one object saying no
// tells you nothing about the next one, and stopping early would leave the rest
// of the ladder cached.
func (s *Server) runVideoEdgePurge(ctx context.Context, videoID uuid.UUID, snap edgePurgeSnapshot) {
	paths, complete := s.expandEdgePurgePaths(ctx, snap)
	failed := 0
	for _, mediaPath := range paths {
		if err := s.deliverysvc.Purge(ctx, mediaPath); err != nil {
			failed++
		}
	}
	// The counters (media_purge_metrics.go) are the observable record of this
	// run — the admin page's answer to "has purge been exercised".
	recordVideoEdgePurgeRun(len(paths)-failed, failed, complete)
	if failed == 0 && complete {
		return
	}
	s.logger.WarnContext(ctx, "cdn purge incomplete; the edge may still be serving this video",
		"video_id", videoID.String(),
		"purged", len(paths)-failed,
		"failed", failed,
		"url_set_complete", complete)
}

// purgeEdgePath invalidates ONE media URL at the edge — the single-URL sibling
// of purgeVideoEdgeCopies, for the assets that occupy exactly one stable route
// (avatars, banners, playlist covers). The stable URL is what makes these
// purges matter at all: replacement overwrites the bytes behind it, so without
// an invalidation the edge serves the old image until its TTL expires, and after
// a deletion it serves it with nothing at the origin left to name it.
//
// Same contract as the fan-out: detached (the work outlives the response by
// design), best-effort (a purge failure never fails the mutation), and fired
// AFTER the mutation commits with a path snapshotted BEFORE it.
//
// asset/resourceID label the failure log; the PATH is never logged (the purge
// URL template is operator-supplied and may carry the credential, and log lines
// must not become the place media URLs leak from either).
func (s *Server) purgeEdgePath(ctx context.Context, asset string, resourceID uuid.UUID, mediaPath string) {
	if !s.cdnConfigured() || mediaPath == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		// A one-URL run is always a complete set; it shares the fan-out's
		// counters because the cdn_purge exercise record must cover every purge
		// shape — an install that only ever replaces images still purges.
		if err := s.deliverysvc.Purge(ctx, mediaPath); err != nil {
			recordVideoEdgePurgeRun(0, 1, true)
			s.logger.WarnContext(ctx, "cdn purge failed; the edge may still be serving this object",
				"asset", asset,
				"resource_id", resourceID.String())
			return
		}
		recordVideoEdgePurgeRun(1, 0, true)
	}()
}

// userImageEdgePath and channelImageEdgePath record the one URL a CDN edge
// could be holding for a profile image, BEFORE a mutation replaces or removes
// it. Empty when there is nothing to invalidate: no CDN configured (the same
// free-by-default gate as the video snapshot), no image service wired, or no
// image set. There is no privacy fence to re-derive here — identity images are
// served with unconditional eligibility (profile_images.go), so "an image
// exists" IS "the edge could hold it".
//
// The IMAGE ROW is still read rather than the path simply being built: the
// route 404s when no image is set, so an unset image has no edge entry and must
// not spend a purge call.
func (s *Server) userImageEdgePath(ctx context.Context, userID uuid.UUID, kind string) string {
	if !s.cdnConfigured() || s.imagesvc == nil {
		return ""
	}
	if _, err := s.imagesvc.UserImage(ctx, userID, kind); err != nil {
		return ""
	}
	return userImagePath(userID, kind)
}

// channelImageEdgePath takes the HANDLE because that is what the route is
// addressed by. A handle rename therefore leaves the old URL at the edge under
// a name nothing here can reconstruct; that is recorded rather than papered
// over, and it is the same class of gap as a superseded HLS generation's ?v=.
func (s *Server) channelImageEdgePath(ctx context.Context, channelID uuid.UUID, handle, kind string) string {
	if !s.cdnConfigured() || s.imagesvc == nil || handle == "" {
		return ""
	}
	if _, err := s.imagesvc.ChannelImage(ctx, channelID, kind); err != nil {
		return ""
	}
	return channelImagePath(handle, kind)
}

// playlistCoverEdgePath records the one URL a CDN edge could be holding for a
// playlist's cover, BEFORE a mutation replaces, removes or de-lists it. Unlike
// the identity images this one HAS an eligibility fence to re-derive: a cover
// may only leave the origin for a PUBLIC playlist (playlists.go), so a private
// or unlisted playlist's cover was structurally never handed to the CDN and
// returns empty here — the same self-fencing discipline as the video snapshot.
func (s *Server) playlistCoverEdgePath(ctx context.Context, playlistID uuid.UUID) string {
	if !s.cdnConfigured() || s.playlistsvc == nil {
		return ""
	}
	p, err := s.playlistsvc.GetByID(ctx, playlistID)
	if err != nil || p.Visibility != "public" || p.ThumbnailExt == nil || *p.ThumbnailExt == "" {
		return ""
	}
	return playlistCoverPath(playlistID)
}

// supersededGenerationKey reports that a key under the promoted generation's own
// prefix in fact belongs to a LATER generation directory
// (streaming-playlists/<id>/rN/…). It is only ever true when the promoted
// generation is the legacy in-place layout, because that layout is the parent of
// every rN directory the video has ever had.
func supersededGenerationKey(servedPrefix, key string) bool {
	rest, ok := strings.CutPrefix(key, servedPrefix)
	if !ok {
		return false
	}
	seg, _, found := strings.Cut(rest, "/")
	return found && media.IsHLSGenerationName(seg)
}

// expandEdgePurgePaths turns a snapshot into the deduplicated URL list to purge,
// enumerating each tree through the storage backend.
//
// complete=false means the list is known to be short of what the edge could
// hold, which is a materially different outcome from "purged everything and some
// calls failed" and is reported as such. Three causes, all named:
//
//   - the backend cannot list, or a listing failed;
//   - the cap was hit;
//   - A SUPERSEDED GENERATION IS STILL IN THE STORE. Its children were served
//     at the same paths as today's under a DIFFERENT ?v= tag, and that tag is
//     the promoted playlist's own updated-at — it is not recorded anywhere once
//     the row moves on, so those URLs cannot be named. They stop existing when
//     mediagc collects the generation, which is why generation-addressed output
//     makes this bound shrink rather than grow.
//
// The recorded paths go FIRST so that a capped fan-out still spends its budget
// on the thumbnail and the original rather than exhausting it on segments.
func (s *Server) expandEdgePurgePaths(ctx context.Context, snap edgePurgeSnapshot) ([]string, bool) {
	seen := make(map[string]struct{}, len(snap.paths))
	out := make([]string, 0, len(snap.paths))
	add := func(mediaPath string) bool {
		if mediaPath == "" {
			return true
		}
		if _, dup := seen[mediaPath]; dup {
			return true
		}
		seen[mediaPath] = struct{}{}
		out = append(out, mediaPath)
		return len(out) < maxVideoPurgePaths
	}
	for _, mediaPath := range snap.paths {
		if !add(mediaPath) {
			return out, false
		}
	}
	lister, ok := s.media.(storage.ObjectLister)
	if !ok {
		return out, len(snap.trees) == 0
	}
	complete := true
	for _, tree := range snap.trees {
		listed, err := lister.ListKeys(ctx, tree.listPrefix)
		if err != nil {
			complete = false
			continue
		}
		servedPrefix := strings.TrimSuffix(tree.servedPrefix, "/") + "/"
		for _, key := range listed {
			if !strings.HasPrefix(key, servedPrefix) {
				// A key outside the promoted generation's own directory —
				// which is what a SUPERSEDED generation looks like when the
				// promoted one is streaming-playlists/<id>/rN.
				complete = false
				continue
			}
			rel, mapped := hlsTreeRelForKey(tree.servedPrefix, key, tree.peertube)
			if !mapped {
				// The OTHER shape a superseded generation takes, and it is the
				// one a prefix test cannot see: when the promoted generation is
				// the legacy in-place layout (generation 0), every later
				// generation sits UNDER it at streaming-playlists/<id>/rN/…, so
				// it passes the prefix check above and only the route grammar
				// rejects it. Without this branch a video that had been
				// re-transcoded once would report a complete purge while its
				// previous generation stayed at the edge.
				if !tree.peertube && supersededGenerationKey(servedPrefix, key) {
					complete = false
				}
				// Otherwise: a playlist, a manifest or a download derivative —
				// named by another route or never at the edge at all.
				continue
			}
			if !add(videoHLSChildPath(tree.videoID, rel, tree.version)) {
				return out, false
			}
		}
	}
	return out, complete
}
