package httpapi

import (
	"context"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// This file wires the delivery resolver's Purge hook to the moments a video's
// edge-cached bytes become wrong: deletion — direct (handleDeleteVideo) or via
// the channel cascade (handleDeleteChannel: the DATABASE deletes the videos,
// 0006 ON DELETE CASCADE, so the channel handler snapshots them first) — a
// privacy flip away from public, and an admin block.
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
// WHAT THE PROVIDER CAN ACTUALLY DO. internal/cdn's purge is ONE OBJECT KEY per
// request — a method, a URL template and at most one auth header, which is what
// every CDN's single-URL invalidation API reduces to. There is no prefix, no
// wildcard and no "purge everything under this directory", and inventing one
// here would mean inventing a vendor. So a video's invalidation is a fan-out of
// single-key purges over the keys that video actually occupies, which is why
// this file enumerates rather than issues one call.
//
// TWO-PHASE, AND THAT IS NOT STYLISTIC. The keys are SNAPSHOTTED BEFORE the
// state change and purged AFTER it commits:
//
//   - before, because deleting a video deletes the rows that NAME its objects
//     (mediagc collects the objects themselves much later), and because a video
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

// maxVideoPurgeKeys bounds one video's fan-out.
//
// A long video's ladder is thousands of segments, and each purge is a
// third-party HTTP call: unbounded, that is a background task that runs for
// hours and a purge API that starts rate-limiting the ones that matter. The cap
// is high enough to cover an ordinary video whole and low enough that the worst
// case stays a bounded background task. It is a mitigation, not a fix — the
// real fix is generation-addressed keys (phase-5 item 1a), after which content
// replacement stops needing purge at all.
const maxVideoPurgeKeys = 5000

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

// edgePurgeSnapshot is what the database knew about a video's edge-reachable
// objects at the instant BEFORE the change that invalidated them: exact keys
// for the whole-file objects video_files records, and listable directory
// prefixes for the trees it does not (an HLS ladder's segments have no rows).
type edgePurgeSnapshot struct {
	keys     []string
	prefixes []string
}

func (s edgePurgeSnapshot) empty() bool { return len(s.keys) == 0 && len(s.prefixes) == 0 }

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
		if ferr == nil && f.StorageKey != "" {
			snap.keys = append(snap.keys, f.StorageKey)
		}
	}
	// The trees, by prefix. Both are recursive, so superseded HLS generations
	// (streaming-playlists/<id>/rN) and the progressive web-video generations
	// come along — an edge that cached a segment before a source replacement is
	// still holding it afterwards.
	snap.prefixes = append(snap.prefixes,
		media.HLSKeyPrefix(videoID),
		media.WebVideoPrefixForSource(videoID, ""),
	)
	// A PeerTube-imported ladder does NOT live under this video's id — it keeps
	// the source instance's layout (streaming-playlists/hls/<source-uuid>/), and
	// the only handle on it is the recorded master key. Its row cascades away
	// with the video, so it has to be read here with the rest of the snapshot.
	if s.transcodesvc != nil {
		if sp, ok := s.transcodesvc.Playlist(ctx, videoID); ok {
			if dir := path.Dir(sp.MasterKey); strings.Contains(sp.MasterKey, "/") && dir != "." {
				snap.prefixes = append(snap.prefixes, dir)
			}
		}
	}
	return snap
}

// purgeVideoEdgeCopies invalidates a snapshot's objects at the edge, detached
// from the request.
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

// runVideoEdgePurge issues one purge per key and reports the outcome ONCE.
//
// One aggregate log line rather than one per key, and that is the point: a CDN
// configured with no DELIVERY_CDN_PURGE_URL fails every single call (cmd/api
// already warns about that at boot), and a per-key warning would turn one
// takedown into thousands of identical lines. Neither the key nor the purge URL
// is logged — a purge template is operator-supplied and some APIs carry the
// credential in the query string.
//
// Sequential, not concurrent: purge APIs are rate-limited and a takedown is not
// latency-critical. A rejected key never stops the loop — one object saying no
// tells you nothing about the next one, and stopping early would leave the rest
// of the ladder cached.
func (s *Server) runVideoEdgePurge(ctx context.Context, videoID uuid.UUID, snap edgePurgeSnapshot) {
	keys, complete := s.expandEdgePurgeKeys(ctx, snap)
	failed := 0
	for _, key := range keys {
		if err := s.deliverysvc.Purge(ctx, key); err != nil {
			failed++
		}
	}
	if failed == 0 && complete {
		return
	}
	s.logger.WarnContext(ctx, "cdn purge incomplete; the edge may still be serving this video",
		"video_id", videoID.String(),
		"purged", len(keys)-failed,
		"failed", failed,
		"key_set_complete", complete)
}

// purgeEdgeKey invalidates ONE object at the edge — the single-key sibling of
// purgeVideoEdgeCopies, for the assets that occupy exactly one stable identity
// key (avatars, banners, playlist covers). The stable key is what makes these
// purges matter at all: replacement overwrites IN PLACE, so without an
// invalidation the edge serves the old bytes until its TTL expires, and after
// a deletion it serves them with nothing at the origin left to name them.
//
// Same contract as the fan-out: detached (the work outlives the response by
// design), best-effort (a purge failure never fails the mutation), and fired
// AFTER the mutation commits with a key snapshotted BEFORE it — the
// pre-mutation key is the one the edge cached; on an extension-changing
// replacement the new key holds nothing yet.
//
// asset/resourceID label the failure log; the KEY is never logged (the purge
// URL template is operator-supplied and may carry the credential, and log
// lines must not become the place object keys leak from either).
func (s *Server) purgeEdgeKey(ctx context.Context, asset string, resourceID uuid.UUID, key string) {
	if !s.cdnConfigured() || key == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		if err := s.deliverysvc.Purge(ctx, key); err != nil {
			s.logger.WarnContext(ctx, "cdn purge failed; the edge may still be serving this object",
				"asset", asset,
				"resource_id", resourceID.String())
		}
	}()
}

// userImageEdgeKey and channelImageEdgeKey record the one key a CDN edge could
// be holding for a profile image, BEFORE a mutation replaces or removes it.
// Empty when there is nothing to invalidate: no CDN configured (the same free-
// by-default gate as the video snapshot), no image service wired, or no image
// set. There is no privacy fence to re-derive here — identity images are
// served with unconditional eligibility (profile_images.go), so "an image
// exists" IS "the edge could hold it".
func (s *Server) userImageEdgeKey(ctx context.Context, userID uuid.UUID, kind string) string {
	if !s.cdnConfigured() || s.imagesvc == nil {
		return ""
	}
	img, err := s.imagesvc.UserImage(ctx, userID, kind)
	if err != nil {
		return ""
	}
	return img.StorageKey
}

func (s *Server) channelImageEdgeKey(ctx context.Context, channelID uuid.UUID, kind string) string {
	if !s.cdnConfigured() || s.imagesvc == nil {
		return ""
	}
	img, err := s.imagesvc.ChannelImage(ctx, channelID, kind)
	if err != nil {
		return ""
	}
	return img.StorageKey
}

// playlistCoverEdgeKey records the one key a CDN edge could be holding for a
// playlist's cover, BEFORE a mutation replaces, removes or de-lists it. Unlike
// the identity images this one HAS an eligibility fence to re-derive: a cover
// may only leave the origin for a PUBLIC playlist (playlists.go), so a private
// or unlisted playlist's cover was structurally never handed to the CDN and
// returns empty here — the same self-fencing discipline as the video snapshot.
func (s *Server) playlistCoverEdgeKey(ctx context.Context, playlistID uuid.UUID) string {
	if !s.cdnConfigured() || s.playlistsvc == nil {
		return ""
	}
	p, err := s.playlistsvc.GetByID(ctx, playlistID)
	if err != nil || p.Visibility != "public" || p.ThumbnailExt == nil || *p.ThumbnailExt == "" {
		return ""
	}
	return media.PlaylistThumbnailKey(playlistID, *p.ThumbnailExt)
}

// expandEdgePurgeKeys turns a snapshot into the deduplicated key list to purge,
// enumerating each prefix through the storage backend.
//
// complete=false means the list is known to be short of what the edge could
// hold — the backend cannot list, a listing failed, or the cap was hit — which
// is a materially different outcome from "purged everything and some calls
// failed" and is reported as such.
//
// The recorded whole-file keys go FIRST so that a capped fan-out still spends
// its budget on the thumbnail and the original rather than exhausting it on
// segments. Playlist objects come along in the tree listing even though
// delivery.Redirectable excludes them from the edge entirely: they cost one
// call each against a provider for which "there was never a copy" is a success
// (404 is success in internal/cdn), and filtering by filename here would be a
// second, drifting copy of the key grammar that lives in internal/media.
func (s *Server) expandEdgePurgeKeys(ctx context.Context, snap edgePurgeSnapshot) ([]string, bool) {
	seen := make(map[string]struct{}, len(snap.keys))
	out := make([]string, 0, len(snap.keys))
	add := func(key string) bool {
		if key == "" {
			return true
		}
		if _, dup := seen[key]; dup {
			return true
		}
		seen[key] = struct{}{}
		out = append(out, key)
		return len(out) < maxVideoPurgeKeys
	}
	for _, key := range snap.keys {
		if !add(key) {
			return out, false
		}
	}
	lister, ok := s.media.(storage.ObjectLister)
	if !ok {
		return out, len(snap.prefixes) == 0
	}
	complete := true
	for _, prefix := range snap.prefixes {
		listed, err := lister.ListKeys(ctx, prefix)
		if err != nil {
			complete = false
			continue
		}
		for _, key := range listed {
			if !add(key) {
				return out, false
			}
		}
	}
	return out, complete
}
