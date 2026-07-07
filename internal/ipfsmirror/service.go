package ipfsmirror

import (
	"context"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// webmAlternateName is the base name of the VP9/WebM progressive alternate, which
// lives under the same streaming-playlists/<id>/ prefix as the HLS tree but is a
// SEPARATE media class pinned on its own — so the HLS directory add excludes it
// (keeping the car_root a clean playlists+segments tree). Kept in lock-step with
// media.VP9WebMKey.
const webmAlternateName = "vp9.webm"

const (
	// defaultMaxAttempts dead-letters a pin/unpin after this many tries (matches
	// transcode_jobs' bounded-retry shape).
	defaultMaxAttempts = 6
	// defaultBaseBackoff is the first retry delay; it doubles each attempt up to
	// maxBackoff. Configurable (0 in tests → immediate re-claim).
	defaultBaseBackoff = time.Minute
	maxBackoff         = time.Hour
	// leaseSeconds is the claim visibility timeout: a crashed worker's leased row
	// is retried after this, while concurrent claimers skip it. Pins/unpins are
	// idempotent so an over-long lease is safe.
	leaseSeconds = 120
	// defaultReconcileBatch bounds how many failed rows one reconcile scan re-arms.
	defaultReconcileBatch = 200
)

// Repository is the ledger/queue data access the mirror needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	UpsertIPFSPinIntent(ctx context.Context, arg sqlcgen.UpsertIPFSPinIntentParams) (sqlcgen.MediaIpfsPin, error)
	RepinIPFSObject(ctx context.Context, arg sqlcgen.RepinIPFSObjectParams) error
	EnqueueIPFSUnpin(ctx context.Context, objectKey string) error
	ClaimDueIPFSPins(ctx context.Context, arg sqlcgen.ClaimDueIPFSPinsParams) ([]sqlcgen.ClaimDueIPFSPinsRow, error)
	MarkIPFSPinned(ctx context.Context, arg sqlcgen.MarkIPFSPinnedParams) error
	MarkIPFSPinFailed(ctx context.Context, arg sqlcgen.MarkIPFSPinFailedParams) error
	MarkIPFSPinUnpinned(ctx context.Context, objectKey string) error
	RescheduleIPFSPin(ctx context.Context, arg sqlcgen.RescheduleIPFSPinParams) error
	RearmFailedIPFSPins(ctx context.Context, batchSize int32) (int64, error)
	CountIPFSPinsByStateClass(ctx context.Context) ([]sqlcgen.CountIPFSPinsByStateClassRow, error)
	CountIPFSPinsSharingCID(ctx context.Context, arg sqlcgen.CountIPFSPinsSharingCIDParams) (int64, error)
	ListIPFSPinsByVideo(ctx context.Context, videoID pgtype.UUID) ([]sqlcgen.MediaIpfsPin, error)
	ListPinnedVideoIDs(ctx context.Context, videoIds []uuid.UUID) ([]pgtype.UUID, error)
}

// VideoFileRef is one stored video-file object (video_files row) the mirror may
// consider (its kind is mapped to a MediaClass in-package).
type VideoFileRef struct {
	Kind       string
	StorageKey string
}

// ImageRef is one identity image (avatar/banner) belonging to an owner — used by
// the account-level re-evaluation (unlisted toggle / deactivate).
type ImageRef struct {
	Class       MediaClass
	ObjectKey   string
	OwnerUserID uuid.UUID
}

// Lookups resolves the committed visibility facts + object keys the enqueue
// helpers gate on, so the eligibility fence stays authoritative in ONE place. A
// SQL-backed adapter (lookups.go) satisfies it in production; tests fake it. The
// bool return distinguishes "missing row" (skip, not an error) from a real error.
type Lookups interface {
	VideoVisibility(ctx context.Context, videoID uuid.UUID) (privacy, state string, ownerID uuid.UUID, ok bool, err error)
	VideoFiles(ctx context.Context, videoID uuid.UUID) ([]VideoFileRef, error)
	VideoCaptionKeys(ctx context.Context, videoID uuid.UUID) ([]string, error)
	UserFlags(ctx context.Context, userID uuid.UUID) (active, unlisted bool, ok bool, err error)
	ChannelOwner(ctx context.Context, channelID uuid.UUID) (ownerID uuid.UUID, ok bool, err error)
	PlaylistCover(ctx context.Context, playlistID uuid.UUID) (visibility, objectKey string, hasCover bool, err error)
	OwnerImageRefs(ctx context.Context, userID uuid.UUID) ([]ImageRef, error)
}

// Config is the mirror service's tunables (from internal/config).
type Config struct {
	Enabled        bool
	GatewayURL     string
	ClusterEnabled bool
	AddTimeout     time.Duration
	Concurrency    int
	MaxAttempts    int
	BaseBackoff    time.Duration
	ReconcileBatch int
	Logger         *slog.Logger
}

// Service is the mirror sidecar: enqueue helpers (called from the authoritative
// write paths), the pin/unpin worker, the reconciliation scan, and admin status.
// When Enabled is false every method is an inert no-op, so producing services can
// call the enqueue helpers unconditionally.
type Service struct {
	repo    Repository
	lookups Lookups
	blobs   storage.Backend
	client  ipfs.Client

	enabled        bool
	gatewayURL     string
	clusterEnabled bool
	addTimeout     time.Duration
	concurrency    int
	maxAttempts    int
	baseBackoff    time.Duration
	reconcileBatch int
	logger         *slog.Logger
}

// New builds the mirror service. repo is required; lookups/blobs/client may be
// nil when Enabled is false (the no-op path). All defaults are filled in.
func New(repo Repository, lookups Lookups, blobs storage.Backend, client ipfs.Client, cfg Config) *Service {
	s := &Service{
		repo:           repo,
		lookups:        lookups,
		blobs:          blobs,
		client:         client,
		enabled:        cfg.Enabled,
		gatewayURL:     cfg.GatewayURL,
		clusterEnabled: cfg.ClusterEnabled,
		addTimeout:     cfg.AddTimeout,
		concurrency:    cfg.Concurrency,
		maxAttempts:    cfg.MaxAttempts,
		baseBackoff:    cfg.BaseBackoff,
		reconcileBatch: cfg.ReconcileBatch,
		logger:         cfg.Logger,
	}
	if s.addTimeout <= 0 {
		s.addTimeout = 60 * time.Second
	}
	if s.concurrency < 1 {
		s.concurrency = 1
	}
	if s.maxAttempts < 1 {
		s.maxAttempts = defaultMaxAttempts
	}
	if s.baseBackoff < 0 {
		s.baseBackoff = 0
	} else if s.baseBackoff == 0 {
		s.baseBackoff = defaultBaseBackoff
	}
	if s.reconcileBatch < 1 {
		s.reconcileBatch = defaultReconcileBatch
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

// Enabled reports whether the mirror is active (used to decide whether to start
// the background worker).
func (s *Service) Enabled() bool { return s.enabled }

// ---- enqueue helpers (authoritative-write hooks) --------------------------

// EnqueueUserImage records a pin intent for a user's avatar/banner if the owner
// is active and listed. No-op when disabled. Best-effort: an enqueue error is
// returned for logging but must never fail the upload (graceful degradation — the
// reconciliation scan is the backstop).
func (s *Service) EnqueueUserImage(ctx context.Context, userID uuid.UUID, class MediaClass, objectKey string) error {
	if !s.enabled {
		return nil
	}
	active, unlisted, ok, err := s.lookups.UserFlags(ctx, userID)
	if err != nil || !ok {
		return err
	}
	if !Eligible(Subject{Class: class, OwnerActive: active, OwnerUnlisted: unlisted}) {
		return nil
	}
	return s.upsertPin(ctx, objectKey, class, uuid.Nil, userID)
}

// EnqueueChannelImage records a pin intent for a channel's avatar/banner if the
// owning account is active and listed. No-op when disabled.
func (s *Service) EnqueueChannelImage(ctx context.Context, channelID uuid.UUID, class MediaClass, objectKey string) error {
	if !s.enabled {
		return nil
	}
	owner, ok, err := s.lookups.ChannelOwner(ctx, channelID)
	if err != nil || !ok {
		return err
	}
	active, unlisted, ok, err := s.lookups.UserFlags(ctx, owner)
	if err != nil || !ok {
		return err
	}
	if !Eligible(Subject{Class: class, OwnerActive: active, OwnerUnlisted: unlisted}) {
		return nil
	}
	return s.upsertPin(ctx, objectKey, class, uuid.Nil, owner)
}

// SyncVideo re-evaluates a video's ledger against its current privacy+state and
// is the single entrypoint wired to the publish, privacy-update and unpublish
// transitions, so eligibility is always derived from committed state. Every
// video-derived class shares ONE gate (public+published), so the whole video
// flips together:
//   - eligible ⇒ (re)pin the original (web-videos/<id><ext>), the VP9/WebM
//     alternate, and the image derivatives (thumbnail, storyboard, storyboard
//     vtt, captions) that currently exist. private→public re-pins them all.
//   - ineligible (public→private, unpublish, quarantine) ⇒ unpin ALL of the
//     video's ledger rows — including the HLS row added in P19.4 and any orphan
//     rows — so nothing non-public lingers pinned. This is the privacy fence at
//     the video level.
//
// The HLS directory add itself is enqueued on transcode completion in P19.4; the
// unpin-all path here already covers its removal. No-op when disabled.
func (s *Service) SyncVideo(ctx context.Context, videoID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	privacy, state, _, ok, err := s.lookups.VideoVisibility(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	// A representative video-derived class decides the whole video (they share the
	// public+published gate).
	if !Eligible(Subject{Class: ClassVideoOriginal, VideoPrivacy: privacy, VideoState: state}) {
		return s.unpinAllForVideo(ctx, videoID)
	}
	refs, err := s.videoMirrorRefs(ctx, videoID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if perr := s.upsertPin(ctx, ref.ObjectKey, ref.Class, videoID, uuid.Nil); perr != nil {
			return perr
		}
	}
	return nil
}

// UnpinVideo enqueues an unpin for EVERY ledger row of a video — the delete path.
// The worker's reference-count guard ensures a shared CID is only removed from the
// node once no live row references it. No-op when disabled.
func (s *Service) UnpinVideo(ctx context.Context, videoID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	return s.unpinAllForVideo(ctx, videoID)
}

// unpinAllForVideo flips every ledger row of a video toward removal. Shared by
// the unpublish/private transition (SyncVideo) and the delete path (UnpinVideo):
// listing by video_id catches every class — original, VP9, image derivatives and
// the HLS tree — plus any orphan row whose object key is no longer a current file.
func (s *Service) unpinAllForVideo(ctx context.Context, videoID uuid.UUID) error {
	rows, err := s.repo.ListIPFSPinsByVideo(ctx, pgUUID(videoID))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.repo.EnqueueIPFSUnpin(ctx, r.ObjectKey); err != nil {
			return err
		}
	}
	return nil
}

// OnTranscodeComplete is the transcode-completion hook (P19.4): the video's HLS
// tree — and, when VP9 is enabled, the progressive VP9/WebM alternate — have just
// been produced, or replaced wholesale on a re-transcode ('0039'). For an ELIGIBLE
// (public+published) VOD it force-(re)pins:
//   - the finalized HLS tree as ONE directory row (media_class='hls', object_key
//     streaming-playlists/<id>/ with a trailing slash marking a directory intent
//     the worker resolves via the storage ObjectLister + AddDirectory into one
//     car_root CID); and
//   - the VP9/WebM alternate, which does NOT exist at publish time (it is produced
//     here), so the publish-hook SyncVideo could not pin it.
//
// Both keep a STABLE object_key across re-transcodes while their content — and
// therefore their CID — changes, so RepinIPFSObject forces a re-claim (the no-op-
// on-pinned UpsertIPFSPinIntent would not) and the worker swaps the superseded CID.
//
// For an INELIGIBLE video this is a no-op: the privacy transition (SyncVideo /
// UnpinVideo) owns unpinning. LIVE HLS never flows here — only a finalized VOD
// transcode job completes; the mutable live edge is never mirrored. No-op when
// disabled.
func (s *Service) OnTranscodeComplete(ctx context.Context, videoID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	privacy, state, _, ok, err := s.lookups.VideoVisibility(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	// The whole video shares the public+published gate; ClassHLS is representative.
	// A private/unlisted/quarantined video re-checked here is refused, so nothing
	// non-public is ever armed at transcode completion (the privacy fence).
	if !Eligible(Subject{Class: ClassHLS, VideoPrivacy: privacy, VideoState: state}) {
		return nil
	}
	hlsKey := media.HLSKeyPrefix(videoID) + "/"
	if err := s.repo.RepinIPFSObject(ctx, sqlcgen.RepinIPFSObjectParams{
		ObjectKey:  hlsKey,
		MediaClass: string(ClassHLS),
		VideoID:    pgUUID(videoID),
	}); err != nil {
		return err
	}
	files, err := s.lookups.VideoFiles(ctx, videoID)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Kind == "webm" {
			if err := s.repo.RepinIPFSObject(ctx, sqlcgen.RepinIPFSObjectParams{
				ObjectKey:  f.StorageKey,
				MediaClass: string(ClassWebM),
				VideoID:    pgUUID(videoID),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReevaluateUser re-evaluates an owner's identity images (their own avatar/banner
// plus their channels') after an unlisted toggle or a deactivate/reactivate:
// eligible ⇒ (re)pin, ineligible ⇒ unpin. This is the privacy re-evaluation
// trigger — flipping a user to unlisted or deactivating them pulls their images
// off the public network. No-op when disabled.
func (s *Service) ReevaluateUser(ctx context.Context, userID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	active, unlisted, ok, err := s.lookups.UserFlags(ctx, userID)
	if err != nil || !ok {
		return err
	}
	refs, err := s.lookups.OwnerImageRefs(ctx, userID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if Eligible(Subject{Class: ref.Class, OwnerActive: active, OwnerUnlisted: unlisted}) {
			if perr := s.upsertPin(ctx, ref.ObjectKey, ref.Class, uuid.Nil, userID); perr != nil {
				return perr
			}
		} else if uerr := s.repo.EnqueueIPFSUnpin(ctx, ref.ObjectKey); uerr != nil {
			return uerr
		}
	}
	return nil
}

// EnqueuePlaylistCover records a pin intent for a public playlist's cover, or an
// unpin when the playlist is not public. No-op when disabled or when the playlist
// has no cover.
func (s *Service) EnqueuePlaylistCover(ctx context.Context, playlistID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	vis, key, has, err := s.lookups.PlaylistCover(ctx, playlistID)
	if err != nil || !has {
		return err
	}
	if Eligible(Subject{Class: ClassPlaylistCover, PlaylistVisibility: vis}) {
		return s.upsertPin(ctx, key, ClassPlaylistCover, uuid.Nil, uuid.Nil)
	}
	return s.repo.EnqueueIPFSUnpin(ctx, key)
}

// EnqueueUserImagePin is the kind-keyed convenience the profileimage service
// calls (kind is "avatar" or "banner") — it maps kind→MediaClass and delegates
// to EnqueueUserImage. Keeping the mapping here lets profileimage depend on a
// small structural interface rather than importing this package's class enum.
func (s *Service) EnqueueUserImagePin(ctx context.Context, userID uuid.UUID, kind, objectKey string) error {
	return s.EnqueueUserImage(ctx, userID, userImageClass(kind), objectKey)
}

// EnqueueChannelImagePin is the kind-keyed convenience for channel avatars/banners.
func (s *Service) EnqueueChannelImagePin(ctx context.Context, channelID uuid.UUID, kind, objectKey string) error {
	return s.EnqueueChannelImage(ctx, channelID, channelImageClass(kind), objectKey)
}

// userImageClass / channelImageClass map the "avatar"/"banner" kind to the class
// (anything that is not the avatar kind is treated as a banner).
func userImageClass(kind string) MediaClass {
	if kind == kindAvatar {
		return ClassUserAvatar
	}
	return ClassUserBanner
}

func channelImageClass(kind string) MediaClass {
	if kind == kindAvatar {
		return ClassChannelAvatar
	}
	return ClassChannelBanner
}

// EnqueueUnpin flips a single object's ledger row toward removal (delete of one
// derivative). No-op when disabled.
func (s *Service) EnqueueUnpin(ctx context.Context, objectKey string) error {
	if !s.enabled {
		return nil
	}
	return s.repo.EnqueueIPFSUnpin(ctx, objectKey)
}

// upsertPin writes a pending pin intent for one object key.
func (s *Service) upsertPin(ctx context.Context, objectKey string, class MediaClass, videoID, ownerUserID uuid.UUID) error {
	_, err := s.repo.UpsertIPFSPinIntent(ctx, sqlcgen.UpsertIPFSPinIntentParams{
		ObjectKey:   objectKey,
		MediaClass:  string(class),
		VideoID:     pgUUID(videoID),
		OwnerUserID: pgUUID(ownerUserID),
	})
	return err
}

// videoMirrorRefs is the video's currently-stored, mirror-eligible objects: the
// single-file video_files rows (original, VP9/WebM alternate, thumbnail,
// storyboard, storyboard vtt) plus caption tracks. The HLS tree is a directory
// add enqueued on transcode completion in P19.4, not from here.
func (s *Service) videoMirrorRefs(ctx context.Context, videoID uuid.UUID) ([]ImageRef, error) {
	var refs []ImageRef
	files, err := s.lookups.VideoFiles(ctx, videoID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if cls, ok := videoFileMirrorClass(f.Kind); ok {
			refs = append(refs, ImageRef{Class: cls, ObjectKey: f.StorageKey})
		}
	}
	caps, err := s.lookups.VideoCaptionKeys(ctx, videoID)
	if err != nil {
		return nil, err
	}
	for _, k := range caps {
		refs = append(refs, ImageRef{Class: ClassCaption, ObjectKey: k})
	}
	return refs, nil
}

// videoFileMirrorClass maps a video_files.kind to the mirror MediaClass for the
// single-file classes SyncVideo enqueues: the original and VP9/WebM alternate
// (P19.3) plus the image derivatives (P19.2). 'hls' is intentionally absent — the
// HLS tree is a directory/wrap add (P19.4), enqueued as one media_class='hls' row
// on transcode completion, not a per-file row here.
func videoFileMirrorClass(kind string) (MediaClass, bool) {
	switch kind {
	case "original":
		return ClassVideoOriginal, true
	case "webm":
		return ClassWebM, true
	case "thumbnail":
		return ClassThumbnail, true
	case "storyboard":
		return ClassStoryboard, true
	case "storyboard_vtt":
		return ClassStoryboardVTT, true
	}
	return "", false
}

// pgUUID converts a uuid.UUID to pgtype.UUID, leaving the zero UUID as SQL NULL.
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

// ---- read models (additive API serving surfaces) --------------------------

// VideoIPFS is the pinned-CID summary the video-detail handler exposes as the
// additive `ipfs` object. Every CID is a validated CIDv1; GatewayURL is the
// configured public gateway base so a client resolves an object as
// {GatewayURL}/ipfs/{cid}. Empty fields are omitted from the response.
type VideoIPFS struct {
	OriginalCID string
	HLSCID      string
	GatewayURL  string
}

// VideoPins returns the pinned original/HLS CIDs for a video, for the detail
// `ipfs` object. ok is false (and VideoIPFS zero) when the mirror is disabled or
// nothing is pinned. ONLY 'pinned'-state rows are considered and every CID is
// re-validated before it is returned — an invalid or unvalidated CID is never
// emitted, and the gateway URL comes solely from IPFS_GATEWAY_URL. The caller
// (handler) is still responsible for the public+published gate: this method
// reports what is pinned, the handler decides whether the video may expose it.
func (s *Service) VideoPins(ctx context.Context, videoID uuid.UUID) (VideoIPFS, bool, error) {
	if !s.enabled {
		return VideoIPFS{}, false, nil
	}
	rows, err := s.repo.ListIPFSPinsByVideo(ctx, pgUUID(videoID))
	if err != nil {
		return VideoIPFS{}, false, err
	}
	var out VideoIPFS
	for _, r := range rows {
		if r.State != "pinned" || r.Cid == "" || ipfs.ValidateCID(r.Cid) != nil {
			continue
		}
		switch MediaClass(r.MediaClass) {
		case ClassVideoOriginal:
			out.OriginalCID = r.Cid
		case ClassHLS:
			out.HLSCID = r.Cid
		}
	}
	if out.OriginalCID == "" && out.HLSCID == "" {
		return VideoIPFS{}, false, nil
	}
	out.GatewayURL = s.gatewayURL
	return out, true, nil
}

// PinnedVideoIDs returns the subset of videoIDs that have at least one pinned
// ledger row — the feed/card `ipfs_pinned` badge, resolved in ONE indexed query
// for a whole page. Returns an empty set when the mirror is disabled or the input
// is empty; nil/zero UUIDs are dropped. Only 'pinned' rows count, so a video that
// went private (its media unpinned) is correctly reported as not pinned.
func (s *Service) PinnedVideoIDs(ctx context.Context, videoIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if !s.enabled || len(videoIDs) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(videoIDs))
	seen := make(map[uuid.UUID]struct{}, len(videoIDs))
	for _, id := range videoIDs {
		if id == uuid.Nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.repo.ListPinnedVideoIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(rows))
	for _, r := range rows {
		if r.Valid {
			out[uuid.UUID(r.Bytes)] = true
		}
	}
	return out, nil
}

// ---- worker ---------------------------------------------------------------

// DrainDue claims up to batch due rows and processes each (pin for 'pending',
// reference-checked unpin for 'unpinning') with up to Concurrency in flight.
// Returns the number that reached a terminal success. Only the claim-query error
// is returned — per-row failures are persisted (rescheduled/dead-lettered) in the
// ledger, never surfaced, so a mirror outage never blocks anything. No-op when
// disabled.
func (s *Service) DrainDue(ctx context.Context, batch int) (int, error) {
	if !s.enabled {
		return 0, nil
	}
	if batch < 1 {
		batch = 1
	}
	rows, err := s.repo.ClaimDueIPFSPins(ctx, sqlcgen.ClaimDueIPFSPinsParams{
		LeaseSeconds: leaseSeconds,
		BatchSize:    int32(batch),
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	var done int64
	for _, row := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func(row sqlcgen.ClaimDueIPFSPinsRow) {
			defer wg.Done()
			defer func() { <-sem }()
			if s.process(ctx, row) {
				atomic.AddInt64(&done, 1)
			}
		}(row)
	}
	wg.Wait()
	return int(done), nil
}

// process handles one claimed row within a per-RPC timeout. Returns true on a
// terminal success (pinned/unpinned).
func (s *Service) process(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	rpcCtx, cancel := context.WithTimeout(ctx, s.addTimeout)
	defer cancel()
	switch row.State {
	case "pending":
		return s.pin(rpcCtx, row)
	case "unpinning":
		return s.unpin(rpcCtx, row)
	default:
		return false
	}
}

// pin streams the object's bytes from the authoritative store and add+pins them.
// A trailing-slash object key is a directory intent (the HLS tree) → pinDirectory.
func (s *Service) pin(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	if strings.HasSuffix(row.ObjectKey, "/") {
		return s.pinDirectory(ctx, row)
	}
	rc, err := s.blobs.Open(ctx, row.ObjectKey)
	if err != nil {
		s.recordFailure(ctx, row, "open source object failed")
		s.logger.Warn("ipfs_pin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "open_source", "error", err)
		return false
	}
	res, err := s.client.Add(ctx, path.Base(row.ObjectKey), rc)
	_ = rc.Close()
	if err != nil {
		s.recordFailure(ctx, row, "add+pin rpc failed")
		s.logger.Warn("ipfs_pin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "add_pin", "error", err)
		return false
	}
	if err := s.repo.MarkIPFSPinned(ctx, sqlcgen.MarkIPFSPinnedParams{
		ObjectKey: row.ObjectKey, Cid: res.CID, CarRoot: "", ByteSize: res.Size,
	}); err != nil {
		s.logger.Warn("ipfs mark pinned failed", "object_key", row.ObjectKey, "error", err)
		return false
	}
	// A re-pin under a STABLE key (e.g. a re-transcoded VP9/WebM alternate) whose
	// content changed leaves the prior CID orphaned — swap it out (reference-checked).
	s.swapUnpin(ctx, row.Cid, res.CID, row.ObjectKey)
	// Info level carries NO cid (CIDs are public capability handles — no spam);
	// the cid is debug-only.
	s.logger.Info("ipfs_pin_ok", "media_class", row.MediaClass, "byte_size", res.Size, "attempts", row.Attempts+1)
	s.logger.Debug("ipfs_pin_ok cid", "object_key", row.ObjectKey, "cid", res.CID)
	return true
}

// pinDirectory add+pins a finalized VOD HLS tree as ONE UnixFS directory (P19.4).
// The trailing-slash object key (streaming-playlists/<id>/) is listed via the
// storage ObjectLister; every playlist + segment is wrapped with-directory into a
// single car_root CID, so the relative playlist URIs resolve unchanged under
// {gateway}/ipfs/{car_root}/…. The VP9/WebM alternate that shares the prefix is
// EXCLUDED (it is a separate class pinned on its own). On a re-transcode the tree
// is replaced wholesale under the same key, yielding a new car_root; the superseded
// root is swap-unpinned (reference-checked).
func (s *Service) pinDirectory(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	lister, ok := s.blobs.(storage.ObjectLister)
	if !ok {
		s.recordFailure(ctx, row, "backend cannot list a directory tree")
		s.logger.Warn("ipfs_pin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "no_object_lister")
		return false
	}
	prefix := row.ObjectKey
	keys, err := lister.ListKeys(ctx, prefix)
	if err != nil {
		s.recordFailure(ctx, row, "list directory tree failed")
		s.logger.Warn("ipfs_pin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "list_tree", "error", err)
		return false
	}
	var entries []ipfs.DirEntry
	var openers []*lazyBlob
	for _, key := range keys {
		if path.Base(key) == webmAlternateName {
			continue // separate media class, pinned on its own
		}
		lb := &lazyBlob{ctx: ctx, blobs: s.blobs, key: key}
		openers = append(openers, lb)
		entries = append(entries, ipfs.DirEntry{Path: strings.TrimPrefix(key, prefix), Data: lb})
	}
	// The client reads entries sequentially, so at most one file handle is open at
	// a time; this defer releases any handle left open on an error/abort path.
	defer func() {
		for _, o := range openers {
			_ = o.Close()
		}
	}()
	if len(entries) == 0 {
		s.recordFailure(ctx, row, "hls tree is empty")
		s.logger.Warn("ipfs_pin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "empty_tree")
		return false
	}
	res, err := s.client.AddDirectory(ctx, entries)
	if err != nil {
		s.recordFailure(ctx, row, "directory add+pin rpc failed")
		s.logger.Warn("ipfs_pin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "add_dir", "error", err)
		return false
	}
	if err := s.repo.MarkIPFSPinned(ctx, sqlcgen.MarkIPFSPinnedParams{
		ObjectKey: row.ObjectKey, Cid: res.CID, CarRoot: res.CID, ByteSize: res.Size,
	}); err != nil {
		s.logger.Warn("ipfs mark pinned failed", "object_key", row.ObjectKey, "error", err)
		return false
	}
	// Re-transcode swap: a prior car_root that differs from the new tree is orphaned.
	s.swapUnpin(ctx, row.CarRoot, res.CID, row.ObjectKey)
	s.logger.Info("ipfs_pin_ok", "media_class", row.MediaClass, "byte_size", res.Size,
		"attempts", row.Attempts+1, "files", len(entries))
	s.logger.Debug("ipfs_pin_ok cid", "object_key", row.ObjectKey, "cid", res.CID)
	return true
}

// swapUnpin removes a superseded CID from the node after a re-pin replaced a
// stable-key object's content (an HLS re-transcode's old car_root, a refreshed
// VP9/WebM alternate's old CID). It is reference-checked — a CID still shared by
// another live ledger row (content-address dedupe) is left pinned. A first pin
// (oldCID=="") or unchanged content (oldCID==newCID) is a no-op. Best-effort: an
// unpin failure is logged, not surfaced (the row is already pinned to the new CID).
func (s *Service) swapUnpin(ctx context.Context, oldCID, newCID, objectKey string) {
	if oldCID == "" || oldCID == newCID || ipfs.ValidateCID(oldCID) != nil {
		return
	}
	shared, err := s.repo.CountIPFSPinsSharingCID(ctx, sqlcgen.CountIPFSPinsSharingCIDParams{
		Cid: oldCID, ObjectKey: objectKey,
	})
	if err != nil || shared > 0 {
		return
	}
	if err := s.client.Unpin(ctx, oldCID); err != nil {
		s.logger.Warn("ipfs_unpin_failed", "object_key", objectKey, "reason", "swap_superseded", "error", err)
		return
	}
	s.logger.Info("ipfs_unpin_ok", "object_key", objectKey, "reason", "superseded")
}

// lazyBlob is an io.Reader that opens its backing storage object on the first Read
// and closes it at EOF, so a directory add streaming hundreds of HLS segments keeps
// only ONE file handle open at a time (the client reads directory entries
// sequentially). Close releases the handle on an error/abort path and is safe to
// call repeatedly.
type lazyBlob struct {
	ctx   context.Context //nolint:containedctx // bounded to one AddDirectory streaming call
	blobs storage.Backend
	key   string
	rc    io.ReadCloser
	done  bool
}

func (l *lazyBlob) Read(p []byte) (int, error) {
	if l.done {
		return 0, io.EOF
	}
	if l.rc == nil {
		rc, err := l.blobs.Open(l.ctx, l.key)
		if err != nil {
			l.done = true
			return 0, err
		}
		l.rc = rc
	}
	n, err := l.rc.Read(p)
	if err == io.EOF {
		_ = l.rc.Close()
		l.rc = nil
		l.done = true
	}
	return n, err
}

func (l *lazyBlob) Close() error {
	if l.rc != nil {
		err := l.rc.Close()
		l.rc = nil
		return err
	}
	return nil
}

// unpin removes a node pin, but only after a reference check: a CID shared by
// another live ledger row (identical bytes ⇒ identical CID) is NOT removed until
// the last reference is gone. Unpinning does not guarantee erasure on a public
// network — that is the node's own GC.
func (s *Service) unpin(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	if row.Cid == "" {
		// Enqueued for pin then removed before it was ever pinned: nothing on the
		// node to remove.
		if err := s.repo.MarkIPFSPinUnpinned(ctx, row.ObjectKey); err != nil {
			return false
		}
		return true
	}
	shared, err := s.repo.CountIPFSPinsSharingCID(ctx, sqlcgen.CountIPFSPinsSharingCIDParams{
		Cid: row.Cid, ObjectKey: row.ObjectKey,
	})
	if err != nil {
		s.recordFailure(ctx, row, "reference check failed")
		return false
	}
	if shared == 0 {
		if err := s.client.Unpin(ctx, row.Cid); err != nil {
			s.recordFailure(ctx, row, "unpin rpc failed")
			s.logger.Warn("ipfs_unpin_failed", "media_class", row.MediaClass, "object_key", row.ObjectKey,
				"attempts", row.Attempts+1, "error", err)
			return false
		}
	} else {
		s.logger.Debug("ipfs unpin skipped: cid still referenced", "object_key", row.ObjectKey, "shared", shared)
	}
	if err := s.repo.MarkIPFSPinUnpinned(ctx, row.ObjectKey); err != nil {
		return false
	}
	s.logger.Info("ipfs_unpin_ok", "media_class", row.MediaClass, "attempts", row.Attempts+1)
	return true
}

// recordFailure reschedules with exponential backoff, or dead-letters to 'failed'
// after maxAttempts. reason is a SAFE, client-invisible category — never the raw
// node error verbatim (which is logged separately).
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow, reason string) {
	attempts := int(row.Attempts) + 1
	if attempts >= s.maxAttempts {
		_ = s.repo.MarkIPFSPinFailed(ctx, sqlcgen.MarkIPFSPinFailedParams{ObjectKey: row.ObjectKey, LastError: reason})
		return
	}
	_ = s.repo.RescheduleIPFSPin(ctx, sqlcgen.RescheduleIPFSPinParams{
		ObjectKey:     row.ObjectKey,
		NextAttemptAt: time.Now().UTC().Add(s.backoff(attempts)),
		LastError:     reason,
	})
}

// backoff is baseBackoff * 2^(attempts-1), capped at maxBackoff.
func (s *Service) backoff(attempts int) time.Duration {
	d := s.baseBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// ---- reconciliation -------------------------------------------------------

// Reconcile re-arms dead-lettered ('failed') rows so the worker retries them (the
// outage-recovery path): a failed pin re-arms to 'pending', a failed unpin to
// 'unpinning' (the query discriminates on the CID). Returns how many were
// re-armed. The full catalog backfill (upserting intents for pre-existing
// eligible objects that never got a ledger row) is the admin one-shot in P19.6.
// No-op when disabled.
func (s *Service) Reconcile(ctx context.Context) (int64, error) {
	if !s.enabled {
		return 0, nil
	}
	n, err := s.repo.RearmFailedIPFSPins(ctx, int32(s.reconcileBatch))
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.logger.Info("ipfs reconcile re-armed failed pins", "count", n)
	}
	return n, nil
}

// ---- admin status ---------------------------------------------------------

// PinCounts is the pin tally by ledger state.
type PinCounts struct {
	Pinned   int64
	Pending  int64
	Failed   int64
	Unpinned int64
}

// ClassCounts is a per-media-class pin tally.
type ClassCounts struct {
	MediaClass string
	PinCounts
}

// Status is the admin mirror status (backs GET /api/v1/ipfs/status).
type Status struct {
	Enabled        bool
	NodeReachable  bool
	GatewayURL     string
	ClusterEnabled bool
	Pins           PinCounts
	ByClass        []ClassCounts
}

// Status aggregates ledger counts (overall + per class) and probes node health.
// The health probe never fails the call — IPFS is non-authoritative, so an
// unreachable node yields node_reachable=false, not an error.
func (s *Service) Status(ctx context.Context) (Status, error) {
	st := Status{Enabled: s.enabled, GatewayURL: s.gatewayURL, ClusterEnabled: s.clusterEnabled}
	if s.client != nil {
		if _, err := s.client.Version(ctx); err == nil {
			st.NodeReachable = true
		} else {
			s.logger.Debug("ipfs_node_unhealthy", "error", err)
		}
	}
	rows, err := s.repo.CountIPFSPinsByStateClass(ctx)
	if err != nil {
		return st, err
	}
	byClass := map[string]*ClassCounts{}
	for _, r := range rows {
		addStateCount(&st.Pins, r.State, r.Count)
		cc := byClass[r.MediaClass]
		if cc == nil {
			cc = &ClassCounts{MediaClass: r.MediaClass}
			byClass[r.MediaClass] = cc
		}
		addStateCount(&cc.PinCounts, r.State, r.Count)
	}
	names := make([]string, 0, len(byClass))
	for name := range byClass {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st.ByClass = append(st.ByClass, *byClass[name])
	}
	return st, nil
}

// addStateCount folds one (state,count) row into a PinCounts tally. 'unpinning'
// is counted with 'pending' (both are in-flight, actionable states) so the
// four-bucket contract stays exhaustive.
func addStateCount(c *PinCounts, state string, n int64) {
	switch state {
	case "pinned":
		c.Pinned += n
	case "pending", "unpinning":
		c.Pending += n
	case "failed":
		c.Failed += n
	case "unpinned":
		c.Unpinned += n
	}
}
