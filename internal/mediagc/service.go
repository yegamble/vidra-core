// Package mediagc implements media garbage collection: a sweep that lists stored
// object keys under a fixed set of KNOWN prefixes and deletes only those blobs
// with no database reference. It NEVER lists or touches an unknown prefix, and a
// dry run reports what it would delete without deleting anything. It is
// HTTP-agnostic and testable against any storage backend that can list objects.
//
// "No database row references it" is a statement about the database, and it is
// only evidence about the STORE when the two belong to the same install. Four
// rails stand between that inference and an irreversible delete, and all of them
// degrade to a dry run rather than to an error:
//
//   - the enable flag (config.MediaGCEnabled), which stops the daily worker from
//     being started at all;
//   - the bucket-ownership marker (storage.OwnerMarkerKey), which refuses to
//     delete from an object store this install has not been shown to own;
//   - the storage-migration interlock, which refuses to delete while a migration
//     campaign is in flight: during a move the two stores are deliberately out of
//     step, so "unreferenced" stops being evidence about either of them;
//   - the orphan-ratio circuit breaker, which refuses a sweep that found an
//     implausible share of what it scanned to be garbage — the shape every
//     wrong reference set has.
package mediagc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrListingUnsupported means the configured storage backend cannot enumerate
// objects (no storage.ObjectLister), so garbage collection cannot run.
var ErrListingUnsupported = errors.New("mediagc: storage backend does not support listing")

// ErrAdoptNotApplicable means AdoptBucket was called on a deployment that has no
// object store to adopt (the local-disk backend), where ownership is not a
// question that can be asked.
var ErrAdoptNotApplicable = errors.New("mediagc: this deployment stores media on local disk, so there is no bucket to adopt")

// ErrNoInstanceIdentity means the marker cannot be written because this process
// never read its instance identity (the row the identity migration inserts).
var ErrNoInstanceIdentity = errors.New("mediagc: this process has no instance identity to stamp on the bucket")

// BucketOwnership is the answer to "does the object store I am about to delete
// from belong to THIS install?", derived from the ownership marker
// (storage.OwnerMarkerKey) at boot and re-derived by an explicit adoption.
//
// It gates DESTRUCTIVE sweeps only. A dry run is always allowed — reporting what
// would be deleted from a store that is not ours is exactly how an operator
// finds out it is not ours.
type BucketOwnership string

const (
	// OwnershipUnknown is the zero value: nothing has resolved ownership yet.
	// It forbids deletion on purpose — the failure mode of forgetting to wire
	// the resolution has to be "GC stops deleting", never "GC deletes anyway".
	OwnershipUnknown BucketOwnership = "unknown"
	// OwnershipNotApplicable is the local-disk backend. A storage root is a
	// directory this install was pointed at and populated itself; there is no
	// shared-bucket hazard to guard against, and stamping a marker file into
	// somebody's media directory would be its own surprise. Local is exempt BY
	// DESIGN, not by omission.
	OwnershipNotApplicable BucketOwnership = "not-applicable"
	// OwnershipOwned means the marker holds this install's identity.
	OwnershipOwned BucketOwnership = "owned"
	// OwnershipUnowned means there is no marker and the store was not empty, so
	// somebody's objects are in there and it is not established whose. An
	// operator resolves it with the adopt-bucket endpoint.
	OwnershipUnowned BucketOwnership = "unowned"
	// OwnershipConflict means the marker holds a DIFFERENT install's identity:
	// another Vidra thinks this bucket is its own. This is the case worth being
	// loudest about — two installs sweeping one bucket delete each other's media.
	OwnershipConflict BucketOwnership = "conflict"
)

// AllowsDelete reports whether a destructive sweep may run in this state.
func (o BucketOwnership) AllowsDelete() bool {
	return o == OwnershipOwned || o == OwnershipNotApplicable
}

// DefaultMaxOrphanPercent mirrors config.Config.MediaGCMaxOrphanPercent's
// default, for callers that construct a Service without one.
const DefaultMaxOrphanPercent = 25

// breakerFloor is the orphan count below which the ratio breaker never fires,
// whatever the percentage says. A brand-new instance with four objects in it and
// one orphan is at 25%; so is one with 40,000 objects and 10,000 orphans. Only
// the second is a sweep worth stopping, and a ratio alone cannot tell them
// apart. Below the floor the sweep is small enough that a wrong answer is
// recoverable from a backup, which is not true of the case this breaker exists
// for.
const breakerFloor = 100

// Mode names for Result.Mode.
const (
	ModeDryRun = "dry-run"
	ModeDelete = "delete"
)

// Reasons a requested delete was downgraded to a dry run by a SAFETY RAIL rather
// than by the caller. Result.ForcedDryRunReason carries one of these; the empty
// string means no rail forced anything (the sweep ran as asked, or the caller
// asked for a dry run).
const (
	// ReasonBucketOwnership: the object store is not established as this
	// install's (see BucketOwnership).
	ReasonBucketOwnership = "bucket_ownership"
	// ReasonMigrationActive: a storage migration campaign is in flight, so the
	// two stores are mid-move and the reference set describes neither of them
	// completely. It also covers "the check itself failed" — an unanswerable
	// question about whether a migration is running has to mean "assume one is".
	ReasonMigrationActive = "storage_migration_active"
)

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
	repo             Repository
	blobs            storage.Backend
	maxOrphanPercent int
	// identity is this install's instance-identity UUID, the value AdoptBucket
	// stamps into the ownership marker. Empty when the process could not read it.
	identity string

	// migrationActive answers "is a storage migration in flight right now?". Nil
	// means the question was never wired, which is the pre-migration world and
	// is treated as "no migration" — unlike a wired check that FAILS, which is
	// treated as "yes" (see Sweep).
	migrationActive func(context.Context) (bool, error)

	// mu guards ownership alone: it is read by every sweep and rewritten by the
	// adopt endpoint, from different goroutines.
	mu        sync.RWMutex
	ownership BucketOwnership
}

// Option configures a Service at construction.
type Option func(*Service)

// WithMaxOrphanPercent sets the circuit-breaker ratio (0–100); out-of-range
// values are ignored in favour of the default, because config validation is
// where a bad number gets reported, not here.
func WithMaxOrphanPercent(pct int) Option {
	return func(s *Service) {
		if pct >= 0 && pct <= 100 {
			s.maxOrphanPercent = pct
		}
	}
}

// WithBucketOwnership sets the ownership state resolved at boot.
func WithBucketOwnership(o BucketOwnership) Option {
	return func(s *Service) { s.ownership = o }
}

// WithActiveMigrationCheck supplies the storage-migration interlock: a
// DESTRUCTIVE sweep is downgraded to a dry run while the check says a migration
// campaign is live.
//
// It exists because a migration deliberately puts the two stores out of step.
// Objects are being written into a destination this instance is not serving
// from; the source still holds copies the campaign has not deleted yet; and
// between cutover and the end of the grace period the OLD store is full of
// objects that no database row will ever reference again by design. A sweep that
// ran in any of those windows would look at a perfectly healthy store and see a
// library's worth of garbage.
//
// A check that ERRORS is treated as "a migration is running". The rail exists
// for the case where the answer matters most, and that is exactly the case where
// a database it cannot reach is least reassuring.
func WithActiveMigrationCheck(fn func(context.Context) (bool, error)) Option {
	return func(s *Service) { s.migrationActive = fn }
}

// WithInstanceIdentity supplies the UUID AdoptBucket writes into the marker.
func WithInstanceIdentity(id string) Option {
	return func(s *Service) { s.identity = strings.TrimSpace(id) }
}

// NewService builds the media GC service. Ownership defaults to
// OwnershipNotApplicable for every backend that is not an object store, and to
// OwnershipUnknown — which forbids deletion — for one that is: a caller that
// forgets to resolve ownership must lose the ability to delete, not the guard.
func NewService(repo Repository, blobs storage.Backend, opts ...Option) *Service {
	s := &Service{
		repo:             repo,
		blobs:            blobs,
		maxOrphanPercent: DefaultMaxOrphanPercent,
		ownership:        OwnershipNotApplicable,
	}
	if _, isObjectStore := blobs.(*storage.S3); isObjectStore {
		s.ownership = OwnershipUnknown
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Ownership reports the current bucket-ownership state.
func (s *Service) Ownership() BucketOwnership {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ownership
}

// SetOwnership replaces the bucket-ownership state. Boot resolution and the
// adopt endpoint are its only callers.
func (s *Service) SetOwnership(o BucketOwnership) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownership = o
}

// refreshOwnership re-derives the ownership state from the marker itself,
// because the marker is the shared truth and the field is one process's cache
// of it: AdoptBucket in another replica moves the marker without moving this
// process's state. Called ONLY from the blocked-delete path in Sweep — see the
// comment there for why not per-sweep — and it returns the state to gate on.
//
//	read failed → state kept as-is (an unanswerable question must not delete)
//	marker == this identity → owned   (some replica adopted; honour it)
//	marker == another       → conflict (the reported state stays honest)
//	no marker               → state kept as-is
//
// The no-marker case is deliberate: claiming a bucket is boot's decision (an
// empty store) or an operator's (AdoptBucket), NEVER a sweep's. This is a read,
// not the write half of an adoption. Without an identity there is nothing to
// match a marker against, so there is nothing to refresh either.
func (s *Service) refreshOwnership(ctx context.Context) BucketOwnership {
	if s.identity == "" {
		return s.Ownership()
	}
	marker, found, err := storage.ReadOwnerMarker(ctx, s.blobs)
	switch {
	case err != nil:
		// Fail safe: the caller stays dry-run on the state it already had.
	case found && marker == s.identity:
		s.SetOwnership(OwnershipOwned)
	case found:
		s.SetOwnership(OwnershipConflict)
	}
	return s.Ownership()
}

// AdoptBucket claims the object store for this install: it stamps the instance
// identity into the ownership marker and flips this process's state to owned, so
// destructive sweeps are allowed from the next one onward. It is the deliberate,
// audited answer to an unowned or conflicting bucket — deliberately an operator
// action rather than something boot decides, because the store holds objects
// whose owner boot cannot establish.
func (s *Service) AdoptBucket(ctx context.Context) error {
	if s.Ownership() == OwnershipNotApplicable {
		return ErrAdoptNotApplicable
	}
	if s.identity == "" {
		return ErrNoInstanceIdentity
	}
	if err := storage.WriteOwnerMarker(ctx, s.blobs, s.identity); err != nil {
		return err
	}
	s.SetOwnership(OwnershipOwned)
	return nil
}

// Result summarises a sweep. Orphans is the sorted list of unreferenced object
// keys — the would-delete set in a dry run, the actually-deleted set otherwise
// (Deleted counts the successful deletes).
type Result struct {
	// DryRun and Mode are the same fact twice: the mode the sweep ACTUALLY ran
	// in, which is not necessarily the one that was asked for. A requested delete
	// that the ownership gate or the circuit breaker refused reports dry-run, and
	// ForcedDryRun/BreakerTripped say which of the two refused it.
	DryRun  bool     `json:"dry_run"`
	Mode    string   `json:"mode"`
	Scanned int      `json:"scanned"`
	Orphans []string `json:"orphans"`
	Deleted int      `json:"deleted"`
	// OrphanPercent is len(Orphans) as a whole-number percentage of Scanned
	// (0 when nothing was scanned) — the figure the breaker compares.
	OrphanPercent int `json:"orphan_percent"`
	// BreakerTripped means the orphan ratio was over the configured limit, so a
	// requested delete deleted nothing. The orphan list is still the full report.
	BreakerTripped bool `json:"breaker_tripped"`
	// BucketOwnership is the marker state the sweep ran under, and ForcedDryRun
	// means a SAFETY RAIL (not the caller) is why nothing was deleted.
	BucketOwnership string `json:"bucket_ownership"`
	ForcedDryRun    bool   `json:"forced_dry_run"`
	// ForcedDryRunReason says WHICH rail forced it — there is more than one now,
	// and "forced_dry_run: true" alone sends an operator to adopt a bucket when
	// the real answer is "wait for the migration to finish". Empty when nothing
	// was forced.
	ForcedDryRunReason string `json:"forced_dry_run_reason,omitempty"`
}

// Summary is the one-line audit reason for a sweep. It exists once so the daily
// worker and the admin endpoint cannot drift into describing the same outcome
// two different ways. It carries counts and states only — never an object key.
func (r Result) Summary() string {
	reason := r.ForcedDryRunReason
	if reason == "" {
		reason = "none"
	}
	return fmt.Sprintf("mode=%s scanned=%d orphans=%d orphan_pct=%d deleted=%d breaker=%t ownership=%s forced_dry_run=%t forced_reason=%s",
		r.Mode, r.Scanned, len(r.Orphans), r.OrphanPercent, r.Deleted, r.BreakerTripped, r.BucketOwnership, r.ForcedDryRun, reason)
}

// sweptPrefixes are the ONLY prefixes the sweep lists. Anything stored outside
// them (avatars, banners, resumable-upload chunks, remote thumbnails, the
// ownership marker at storage.OwnerMarkerKey, …) is never enumerated or deleted
// here — those are owned by other lifecycles.
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
//
// Three things can turn a requested delete into a dry run, and all of them
// report a full orphan list rather than an error, because the operator's next
// question is always "what would it have deleted?":
//
//   - the bucket-ownership gate, which refuses to delete from a store this
//     install has not been established to own;
//   - the storage-migration interlock, which refuses to delete while a campaign
//     has the two stores deliberately out of step;
//   - the orphan-ratio circuit breaker, which refuses a sweep that found an
//     implausible share of the store to be garbage.
func (s *Service) Sweep(ctx context.Context, dryRun bool) (Result, error) {
	lister, ok := s.blobs.(storage.ObjectLister)
	if !ok {
		return Result{}, ErrListingUnsupported
	}

	// Both gates come first: they are decisions about the store and about the
	// world, made before a single key is listed. Ownership is checked first
	// because it is free, and because a store that is not ours is the more
	// alarming of the two answers.
	ownership := s.Ownership()
	forced, forcedReason := false, ""
	if !dryRun && !ownership.AllowsDelete() {
		// The in-memory state is a boot-time cache of the marker, and adoption
		// only flips it in the ONE process that served the admin's request. In a
		// role-split or multi-replica deployment every other process — the leader
		// worker running the daily sweep, a second api replica serving a manual
		// sweep — would otherwise stay forced-dry-run until restart, while the
		// admin holds an "owned" response in their hand. So re-read the marker —
		// but ONLY here, on the already-blocked path. Boot deliberately refused a
		// per-sweep round trip on the ordinary path (an owned store re-answering
		// the same question daily); a sweep that is about to refuse to delete is
		// not the ordinary path, and one small object read is what it costs to
		// make "re-enables deletion from the next sweep" true across processes.
		ownership = s.refreshOwnership(ctx)
		if !ownership.AllowsDelete() {
			dryRun, forced, forcedReason = true, true, ReasonBucketOwnership
		}
	}
	if !dryRun && s.migrationActive != nil {
		active, merr := s.migrationActive(ctx)
		if merr != nil {
			// Fail safe. An unanswerable "is a migration running?" is the case the
			// interlock exists for, not an excuse to skip it.
			active = true
		}
		if active {
			dryRun, forced, forcedReason = true, true, ReasonMigrationActive
		}
	}

	refs, err := referenceSet(ctx, s.repo)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		DryRun:             dryRun,
		Mode:               modeOf(dryRun),
		BucketOwnership:    string(ownership),
		ForcedDryRun:       forced,
		ForcedDryRunReason: forcedReason,
	}
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
	res.OrphanPercent = percentOf(len(res.Orphans), res.Scanned)

	// The orphan set is final here, which is the only point at which the ratio
	// is knowable and still the point before anything has been deleted.
	if !dryRun && s.breakerTrips(len(res.Orphans), res.Scanned) {
		res.BreakerTripped = true
		res.DryRun = true
		res.Mode = ModeDryRun
		return res, nil
	}

	if !res.DryRun {
		for _, key := range res.Orphans {
			if derr := s.blobs.Delete(ctx, key); derr == nil {
				res.Deleted++
			}
		}
	}
	return res, nil
}

// breakerTrips is the ratio guard: an absolute floor first (a handful of orphans
// is never suspicious, whatever share of a nearly-empty store they are), then
// the percentage, compared by cross-multiplication so no float rounding decides
// a deletion.
func (s *Service) breakerTrips(orphans, scanned int) bool {
	if orphans <= breakerFloor {
		return false
	}
	return orphans*100 > scanned*s.maxOrphanPercent
}

// percentOf is the whole-number share, rounded down; 0 objects scanned is 0%
// rather than a division by zero.
func percentOf(part, total int) int {
	if total <= 0 {
		return 0
	}
	return part * 100 / total
}

func modeOf(dryRun bool) string {
	if dryRun {
		return ModeDryRun
	}
	return ModeDelete
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
		vid := ""
		if len(parts) > 1 {
			vid = parts[1]
		}
		if !isVideoID(vid) {
			// The id position holds something that is not a video id, so this
			// key belongs to a tree THIS INSTALL DID NOT LAY OUT — a
			// reference-mode PeerTube import records
			// streaming-playlists/hls/<source-uuid>/… and points STORAGE_* at
			// the source instance's own bucket (docs/peertube-migration.md §4).
			// Unattributable means KEPT: the sweep deletes only what it can
			// positively account for, and the object here may be a live third
			// party's. It is also why this check comes BEFORE the live-video
			// test — "hls" is never a live video id, so asking that question
			// first would answer "orphan" for every segment of every imported
			// video.
			return true
		}
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
//
// It is a function of the repository rather than a method on Service because it
// is the ONE place the key grammar is derived from the database, and the
// blob-reference consistency check (internal/blobverify) has to ask the exact
// same question without owning a sweep, an ownership state or a backend. Two
// enumerations of "every key the database references" would drift, and the one
// that drifted would either delete live media or declare a healthy store broken.
func referenceSet(ctx context.Context, repo Repository) (refSet, error) {
	refs := refSet{
		referenced:   map[string]bool{},
		liveVideoIDs: map[string]bool{},
		hasPlaylist:  map[string]bool{},
		promotedGen:  map[string]string{},
		targetGen:    map[string]string{},
	}

	fileKeys, err := repo.ListAllVideoFileKeys(ctx)
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
	capKeys, err := repo.ListAllCaptionKeys(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, k := range capKeys {
		refs.referenced[k] = true
	}
	plRefs, err := repo.ListPlaylistThumbnailRefs(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, r := range plRefs {
		if r.ThumbnailExt != nil && *r.ThumbnailExt != "" {
			refs.referenced[media.PlaylistThumbnailKey(r.ID, *r.ThumbnailExt)] = true
		}
	}
	spRefs, err := repo.ListStreamingPlaylistRefs(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, r := range spRefs {
		vid := r.VideoID.String()
		own := media.HLSKeyPrefix(r.VideoID) + "/"
		rest, isOwn := strings.CutPrefix(r.MasterKey, own)
		if !isOwn || rest == "" {
			// No master ('' after a dead-lettered transcode), or a master that
			// is not under this video's own prefix (a reference-mode import
			// records streaming-playlists/hls/<source-uuid>/…). Either way
			// there is no attributable generation, so hasPlaylist is left
			// unset and isReferenced keeps the whole tree.
			//
			// For the FOREIGN layout that fallback is not what saves it, and
			// this comment used to claim otherwise: those keys never reach the
			// generation logic at all, because their id position holds no video
			// id and isReferenced keeps them as unattributable first. The two
			// rules have to agree, since either one alone would be a way to
			// delete another instance's live media.
			continue
		}
		refs.hasPlaylist[vid] = true
		gen := ""
		if seg, _, found := strings.Cut(rest, "/"); found && media.IsHLSGenerationName(seg) {
			gen = seg
		}
		refs.promotedGen[vid] = gen
	}
	ids, err := repo.ListAllVideoIDs(ctx)
	if err != nil {
		return refSet{}, err
	}
	for _, id := range ids {
		refs.liveVideoIDs[id.String()] = true
	}
	return refs, nil
}

// References is what the database says the object store must contain, in the
// two shapes the store can be asked about.
//
// The split is not cosmetic. Most media is referenced by an EXACT key that a
// row holds, so "is it there?" is one Exists call. An HLS tree is not: its
// segments and rendition playlists have no rows at all (a ladder rung is one
// video_renditions row covering a whole directory), so the only object a
// reference check can name exactly is the master manifest, and everything below
// it can only be reached through the prefix.
type References struct {
	// Keys are the exact object keys the database references: originals,
	// thumbnails, storyboards and webm alternates (video_files), caption tracks,
	// and the playlist covers reconstructed from playlists.thumbnail_ext.
	// Sorted, deduplicated, and never empty-string.
	Keys []string
	// Playlists is one entry per recorded streaming playlist, including the
	// dead-lettered ones whose MasterKey is '' — a caller that reports on trees
	// needs to be able to say "this video's transcode never produced one"
	// rather than silently not mentioning it.
	Playlists []PlaylistRef
}

// PlaylistRef is one video's HLS tree as the database records it.
type PlaylistRef struct {
	// VideoID owns the tree.
	VideoID uuid.UUID
	// MasterKey is the promoted master manifest's exact key, or '' when the
	// transcode never produced one (a failed or dead-lettered job).
	MasterKey string
	// Prefix is the tree's own key prefix (media.HLSKeyPrefix), the only handle
	// on the segments and rendition playlists that have no rows of their own.
	Prefix string
}

// ListReferences enumerates every object the database expects the store to
// hold, using the SAME derivation the GC sweep matches listed keys against
// (referenceSet). It is exported for the blob-reference consistency check,
// which asks the identical question with the opposite intent: the sweep looks
// for objects with no row, this looks for rows with no object.
//
// It deliberately reports only what the database can name. Objects the sweep
// keeps at the video-id level (a live video's whole HLS tree) are reported as
// Playlists, not as keys.
func ListReferences(ctx context.Context, repo Repository) (References, error) {
	refs, err := referenceSet(ctx, repo)
	if err != nil {
		return References{}, err
	}
	out := References{Keys: make([]string, 0, len(refs.referenced))}
	for k := range refs.referenced {
		if strings.TrimSpace(k) == "" {
			// A row whose blob was never written. Asking a bucket about "" is a
			// question with no useful answer, and reporting it as a dangling
			// reference would be reporting the absence of something nobody ever
			// stored.
			continue
		}
		out.Keys = append(out.Keys, k)
	}
	sort.Strings(out.Keys)

	spRefs, err := repo.ListStreamingPlaylistRefs(ctx)
	if err != nil {
		return References{}, err
	}
	out.Playlists = make([]PlaylistRef, 0, len(spRefs))
	for _, r := range spRefs {
		out.Playlists = append(out.Playlists, PlaylistRef{
			VideoID:   r.VideoID,
			MasterKey: strings.TrimSpace(r.MasterKey),
			Prefix:    media.HLSKeyPrefix(r.VideoID),
		})
	}
	sort.Slice(out.Playlists, func(i, j int) bool {
		if out.Playlists[i].VideoID != out.Playlists[j].VideoID {
			return out.Playlists[i].VideoID.String() < out.Playlists[j].VideoID.String()
		}
		return out.Playlists[i].MasterKey < out.Playlists[j].MasterKey
	})
	return out, nil
}

// isVideoID reports whether a path segment can be a Vidra video id. A "no" is
// the interesting answer: the id position of an HLS key holding anything but a
// UUID is the signature of a tree laid out by another system, for which the
// sweep's unit of collection — the video id — does not exist at all.
func isVideoID(seg string) bool {
	_, err := uuid.Parse(seg)
	return err == nil
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
