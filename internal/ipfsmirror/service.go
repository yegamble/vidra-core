package ipfsmirror

import (
	"context"
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
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

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

// SyncVideo re-evaluates a video's IMAGE-DERIVATIVE ledger rows (thumbnail,
// storyboard, storyboard vtt, captions) against the video's current
// privacy+state, enqueuing pins for now-eligible derivatives and unpins for
// now-ineligible ones. It is the single entrypoint wired to the publish, privacy
// -update and unpublish transitions — public+published enqueues, anything else
// unpins — so eligibility is always derived from committed state. Video originals
// and the HLS tree are added on the publish transition in P19.3/P19.4; this slice
// handles the derivatives. No-op when disabled.
func (s *Service) SyncVideo(ctx context.Context, videoID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	privacy, state, _, ok, err := s.lookups.VideoVisibility(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	refs, err := s.videoDerivativeRefs(ctx, videoID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if Eligible(Subject{Class: ref.Class, VideoPrivacy: privacy, VideoState: state}) {
			if perr := s.upsertPin(ctx, ref.ObjectKey, ref.Class, videoID, uuid.Nil); perr != nil {
				return perr
			}
		} else if uerr := s.repo.EnqueueIPFSUnpin(ctx, ref.ObjectKey); uerr != nil {
			return uerr
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

// videoDerivativeRefs is the video's mirror-eligible IMAGE derivatives: the
// thumbnail/storyboard/storyboard-vtt video_files plus caption tracks. Originals
// (video_original) and the VP9 alternate (webm) are enqueued on the publish
// transition in P19.3; the HLS tree in P19.4 — this slice covers the derivatives.
func (s *Service) videoDerivativeRefs(ctx context.Context, videoID uuid.UUID) ([]ImageRef, error) {
	var refs []ImageRef
	files, err := s.lookups.VideoFiles(ctx, videoID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if cls, ok := imageDerivativeClass(f.Kind); ok {
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

// imageDerivativeClass maps a video_files.kind to the mirror MediaClass for the
// classes THIS slice enqueues. 'original' and 'webm' are intentionally absent
// (P19.3); 'hls' is a directory add (P19.4).
func imageDerivativeClass(kind string) (MediaClass, bool) {
	switch kind {
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
func (s *Service) pin(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	// Directory (HLS tree) adds — a trailing-slash key — land in P19.4.
	if strings.HasSuffix(row.ObjectKey, "/") {
		s.recordFailure(ctx, row, "directory add not supported in this slice")
		return false
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
	// Info level carries NO cid (CIDs are public capability handles — no spam);
	// the cid is debug-only.
	s.logger.Info("ipfs_pin_ok", "media_class", row.MediaClass, "byte_size", res.Size, "attempts", row.Attempts+1)
	s.logger.Debug("ipfs_pin_ok cid", "object_key", row.ObjectKey, "cid", res.CID)
	return true
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
