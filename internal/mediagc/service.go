// Package mediagc implements media garbage collection: a sweep that lists stored
// object keys under a fixed set of KNOWN prefixes and deletes only those blobs
// with no database reference. It NEVER lists or touches an unknown prefix, and a
// dry run reports what it would delete without deleting anything. It is
// HTTP-agnostic and testable against any storage backend that can list objects.
package mediagc

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrListingUnsupported means the configured storage backend cannot enumerate
// objects (no storage.ObjectLister), so garbage collection cannot run.
var ErrListingUnsupported = errors.New("mediagc: storage backend does not support listing")

// Repository is the reference-set data access mediagc needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	ListAllVideoFileKeys(ctx context.Context) ([]string, error)
	ListAllCaptionKeys(ctx context.Context) ([]string, error)
	ListAllVideoIDs(ctx context.Context) ([]uuid.UUID, error)
	ListStreamingPlaylistRefs(ctx context.Context) ([]sqlcgen.ListStreamingPlaylistRefsRow, error)
	ListPlaylistThumbnailRefs(ctx context.Context) ([]sqlcgen.ListPlaylistThumbnailRefsRow, error)
}

// Service runs the media GC sweep over a storage backend.
type Service struct {
	repo  Repository
	blobs storage.Backend
}

// NewService builds the media GC service.
func NewService(repo Repository, blobs storage.Backend) *Service {
	return &Service{repo: repo, blobs: blobs}
}

// Result summarises a sweep. Orphans is the sorted list of unreferenced object
// keys — the would-delete set in a dry run, the actually-deleted set otherwise
// (Deleted counts the successful deletes).
type Result struct {
	DryRun  bool     `json:"dry_run"`
	Scanned int      `json:"scanned"`
	Orphans []string `json:"orphans"`
	Deleted int      `json:"deleted"`
}

// sweptPrefixes are the ONLY prefixes the sweep lists. Anything stored outside
// them (avatars, banners, resumable-upload chunks, remote thumbnails, …) is
// never enumerated or deleted here — those are owned by other lifecycles.
var sweptPrefixes = []string{
	"web-videos",          // originals (video_files)
	"thumbnails",          // posters (video_files)
	"storyboards",         // sprite sheet + vtt (video_files)
	"captions",            // per-language WebVTT (captions)
	"streaming-playlists", // HLS tree + vp9 alternate — kept per live video id
	"playlist-thumbnails", // playlist covers (playlists.thumbnail_ext)
}

// hlsPrefix is the id-partitioned tree collected at the video-id level: its
// segments/variant playlists are not individually recorded in the database, so
// the whole tree is orphan only when its video no longer exists — except for
// replacement GENERATION directories (streaming-playlists/<id>/rN/, config-
// parity W14), which are collected at the generation level: only the video's
// LIVE generations (the promoted one per the playlist master key, plus the
// current source's target generation while a re-transcode is in flight) are
// kept. See hlsGenerations.
const hlsPrefix = "streaming-playlists"

// Sweep lists every object under the known prefixes and computes the orphan set
// (objects with no DB reference). When dryRun is false it deletes each orphan
// (best-effort; a per-object delete error is skipped, not fatal). Returns
// ErrListingUnsupported when the backend cannot list.
func (s *Service) Sweep(ctx context.Context, dryRun bool) (Result, error) {
	lister, ok := s.blobs.(storage.ObjectLister)
	if !ok {
		return Result{}, ErrListingUnsupported
	}

	refs, err := s.referenceSet(ctx)
	if err != nil {
		return Result{}, err
	}

	res := Result{DryRun: dryRun}
	for _, prefix := range sweptPrefixes {
		keys, lerr := lister.ListKeys(ctx, prefix)
		if lerr != nil {
			return Result{}, lerr
		}
		for _, key := range keys {
			res.Scanned++
			if s.isReferenced(key, prefix, refs) {
				continue
			}
			res.Orphans = append(res.Orphans, key)
		}
	}
	sort.Strings(res.Orphans)

	if !dryRun {
		for _, key := range res.Orphans {
			if derr := s.blobs.Delete(ctx, key); derr == nil {
				res.Deleted++
			}
		}
	}
	return res, nil
}

// refSet is everything the sweep matches listed keys against: exact DB-recorded
// keys, the live video ids, and the per-video HLS generation picture (W14).
type refSet struct {
	referenced   map[string]bool
	liveVideoIDs map[string]bool
	// hasPlaylist marks video ids with a streaming_playlists row whose
	// master_key is non-empty AND parseable under the video's own HLS prefix;
	// promotedGen is that master's generation dir ("" = the legacy in-place
	// layout).
	hasPlaylist map[string]bool
	promotedGen map[string]string
	// targetGen is the generation the video's CURRENT source would transcode
	// into (from the original file key's version), kept so an in-flight
	// replacement's half-written tree is never swept. Only set for videos with
	// a stored original.
	targetGen map[string]string
}

// isReferenced reports whether a listed object key is live. For the HLS tree
// the unit is the video id (second path segment) with generation-level
// collection for replacement trees (W14); every other prefix is an exact-key
// match against the referenced set.
func (s *Service) isReferenced(key, prefix string, refs refSet) bool {
	if prefix == hlsPrefix {
		// key = streaming-playlists/<video_id>/[rN/]...
		parts := strings.Split(key, "/")
		if len(parts) < 2 {
			return false
		}
		vid := parts[1]
		if !refs.liveVideoIDs[vid] {
			return false
		}
		// Without an attributable promoted generation (no playlist yet — the
		// first transcode may be mid-write — or a failed/unparseable master),
		// keep the whole tree: never risk sweeping files we cannot attribute.
		if !refs.hasPlaylist[vid] {
			return true
		}
		gen := ""
		if len(parts) > 2 && media.IsHLSGenerationName(parts[2]) {
			gen = parts[2]
		}
		if gen == refs.promotedGen[vid] {
			return true
		}
		// The current source's target generation: an in-flight (or not yet
		// promoted) re-transcode writes here.
		target, ok := refs.targetGen[vid]
		return ok && gen == target
	}
	return refs.referenced[key]
}

// referenceSet gathers the exact object keys the database references, the set
// of live video ids, and the per-video HLS generation picture (for the HLS
// tree).
func (s *Service) referenceSet(ctx context.Context) (refSet, error) {
	refs := refSet{
		referenced:   map[string]bool{},
		liveVideoIDs: map[string]bool{},
		hasPlaylist:  map[string]bool{},
		promotedGen:  map[string]string{},
		targetGen:    map[string]string{},
	}

	fileKeys, err := s.repo.ListAllVideoFileKeys(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, k := range fileKeys {
		refs.referenced[k] = true
		// An original's key names the video's CURRENT source version — the
		// target generation of any in-flight re-transcode (W14).
		if vid, ok := videoIDOfOriginalKey(k); ok {
			refs.targetGen[vid] = media.HLSGenerationName(media.OriginalKeyVersion(k))
		}
	}
	capKeys, err := s.repo.ListAllCaptionKeys(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, k := range capKeys {
		refs.referenced[k] = true
	}
	plRefs, err := s.repo.ListPlaylistThumbnailRefs(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, r := range plRefs {
		if r.ThumbnailExt != nil && *r.ThumbnailExt != "" {
			refs.referenced[media.PlaylistThumbnailKey(r.ID, *r.ThumbnailExt)] = true
		}
	}
	spRefs, err := s.repo.ListStreamingPlaylistRefs(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, r := range spRefs {
		vid := r.VideoID.String()
		own := media.HLSKeyPrefix(r.VideoID) + "/"
		rest, isOwn := strings.CutPrefix(r.MasterKey, own)
		if !isOwn || rest == "" {
			// No master ('' after a dead-lettered transcode) or a foreign
			// layout (e.g. a PeerTube-import tree): no attributable
			// generation — the whole tree is kept via !hasPlaylist above.
			continue
		}
		refs.hasPlaylist[vid] = true
		gen := ""
		if seg, _, found := strings.Cut(rest, "/"); found && media.IsHLSGenerationName(seg) {
			gen = seg
		}
		refs.promotedGen[vid] = gen
	}
	ids, err := s.repo.ListAllVideoIDs(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, id := range ids {
		refs.liveVideoIDs[id.String()] = true
	}
	return refs, nil
}

// videoIDOfOriginalKey extracts the video id from an original-file storage key
// (web-videos/<id>[.rN]<ext>), reporting false for any other key shape.
func videoIDOfOriginalKey(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "web-videos/")
	if !ok || strings.Contains(rest, "/") {
		return "", false
	}
	base, _, found := strings.Cut(rest, ".")
	if !found {
		return "", false
	}
	if _, err := uuid.Parse(base); err != nil {
		return "", false
	}
	return base, true
}
