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
// the whole tree is orphan only when its video no longer exists.
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

	referenced, liveVideoIDs, err := s.referenceSet(ctx)
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
			if s.isReferenced(key, prefix, referenced, liveVideoIDs) {
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

// isReferenced reports whether a listed object key is live. For the HLS tree the
// unit is the video id (second path segment); every other prefix is an exact-key
// match against the referenced set.
func (s *Service) isReferenced(key, prefix string, referenced, liveVideoIDs map[string]bool) bool {
	if prefix == hlsPrefix {
		// key = streaming-playlists/<video_id>/...
		parts := strings.Split(key, "/")
		if len(parts) < 2 {
			return false
		}
		return liveVideoIDs[parts[1]]
	}
	return referenced[key]
}

// referenceSet gathers the exact object keys the database references plus the
// set of live video ids (for the HLS tree).
func (s *Service) referenceSet(ctx context.Context) (referenced, liveVideoIDs map[string]bool, err error) {
	referenced = map[string]bool{}
	liveVideoIDs = map[string]bool{}

	fileKeys, err := s.repo.ListAllVideoFileKeys(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, k := range fileKeys {
		referenced[k] = true
	}
	capKeys, err := s.repo.ListAllCaptionKeys(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, k := range capKeys {
		referenced[k] = true
	}
	plRefs, err := s.repo.ListPlaylistThumbnailRefs(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range plRefs {
		if r.ThumbnailExt != nil && *r.ThumbnailExt != "" {
			referenced[media.PlaylistThumbnailKey(r.ID, *r.ThumbnailExt)] = true
		}
	}
	ids, err := s.repo.ListAllVideoIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		liveVideoIDs[id.String()] = true
	}
	return referenced, liveVideoIDs, nil
}
