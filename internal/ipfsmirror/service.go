package ipfsmirror

import (
	"context"
	"fmt"
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
	// reevalLeaseSeconds is the per-user re-eval job claim visibility timeout — the
	// fan-out is longer than a single pin, so it is generous. Re-evaluation is
	// idempotent, so an over-long lease is safe.
	reevalLeaseSeconds = 300
	// eligibilitySweepBatch bounds how many ineligible live rows one backstop sweep
	// re-arms toward removal.
	eligibilitySweepBatch = 500
)

// Network identifiers for the pin ledger's routing column (P19.P1). Each row lives
// on exactly one network; the worker pins it through that network's node client and
// NO other (fail-closed routing, spec §1/§8). networkPublic is the DB default, so a
// zero-value/legacy row normalizes to public.
const (
	networkPublic  = "public"
	networkPrivate = "private"
)

// normalizeNetwork maps the empty string to the default public network so pre-P19.P
// rows and zero-value test literals route to the public swarm. Any other value is
// returned verbatim (a 'private' row must stay private — never coerced to public).
func normalizeNetwork(n string) string {
	if n == "" {
		return networkPublic
	}
	return n
}

// netClient bundles the node client + optional cluster client + tunables for ONE
// network, resolved once per drain from the ledger row's network. Threading this
// (never s.client directly) through the whole pin/unpin call chain is what makes the
// cardinal invariant STRUCTURAL: on the private path only the private client is ever
// in scope, so a private row is provably incapable of being pinned via the public
// client — the failure mode is "private content unreachable", never "public".
type netClient struct {
	network     string
	client      ipfs.Client
	cluster     ipfs.ClusterClient
	addTimeout  time.Duration
	concurrency int
}

// Repository is the ledger/queue data access the mirror needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	UpsertIPFSPinIntent(ctx context.Context, arg sqlcgen.UpsertIPFSPinIntentParams) (sqlcgen.MediaIpfsPin, error)
	// RouteIPFSPinIntent enqueues/re-arms a pin intent ROUTED to a network (P19.P2):
	// same-network is an idempotent upsert, a network flip transitions the same row
	// across swarms (unpin current → re-arm target). Supersedes UpsertIPFSPinIntent on
	// every eligibility-driven enqueue path.
	RouteIPFSPinIntent(ctx context.Context, arg sqlcgen.RouteIPFSPinIntentParams) (sqlcgen.MediaIpfsPin, error)
	BackfillIPFSPinIntent(ctx context.Context, arg sqlcgen.BackfillIPFSPinIntentParams) (int64, error)
	RepinIPFSObject(ctx context.Context, arg sqlcgen.RepinIPFSObjectParams) error
	EnqueueIPFSUnpin(ctx context.Context, objectKey string) error
	ClaimDueIPFSPins(ctx context.Context, arg sqlcgen.ClaimDueIPFSPinsParams) ([]sqlcgen.ClaimDueIPFSPinsRow, error)
	// MarkIPFSPinned records the pinned CID and returns the row's RESULTING state:
	// 'pinned' on the normal path, or 'unpinning' when a concurrent privacy flip won
	// the lost-update race (the CID is still recorded so the worker can unpin it).
	MarkIPFSPinned(ctx context.Context, arg sqlcgen.MarkIPFSPinnedParams) (string, error)
	MarkIPFSPinFailed(ctx context.Context, arg sqlcgen.MarkIPFSPinFailedParams) error
	MarkIPFSPinUnpinned(ctx context.Context, objectKey string) error
	RescheduleIPFSPin(ctx context.Context, arg sqlcgen.RescheduleIPFSPinParams) error
	RearmFailedIPFSPins(ctx context.Context, batchSize int32) (int64, error)
	// SweepIneligibleIPFSPins is the periodic eligibility BACKSTOP: it flips every
	// live (pinned/pending) row whose current committed visibility facts make it
	// ineligible to 'unpinning', in one set-based join, so any missed unlisted/privacy
	// toggle (a crashed re-eval, a raced worker, a future rule change) converges on
	// the next reconcile tick. Returns how many rows it re-armed.
	SweepIneligibleIPFSPins(ctx context.Context, batchSize int32) (int64, error)
	CountIPFSPinsByNetworkStateClass(ctx context.Context) ([]sqlcgen.CountIPFSPinsByNetworkStateClassRow, error)
	CountIPFSPinsSharingCID(ctx context.Context, arg sqlcgen.CountIPFSPinsSharingCIDParams) (int64, error)
	ListIPFSPinsByVideo(ctx context.Context, videoID pgtype.UUID) ([]sqlcgen.MediaIpfsPin, error)
	ListPinnedVideoIDs(ctx context.Context, videoIds []uuid.UUID) ([]pgtype.UUID, error)
	// ---- durable per-user re-evaluation queue (P19 round-2 audit) --------------
	// EnqueueIPFSUserReeval records ONE compact per-user re-eval job (idempotent
	// upsert); ClaimDue leases due jobs; Delete completes one; Reschedule retries a
	// partial failure with backoff. The worker EXPANDS each job off the request path.
	EnqueueIPFSUserReeval(ctx context.Context, userID uuid.UUID) error
	ClaimDueIPFSUserReevals(ctx context.Context, arg sqlcgen.ClaimDueIPFSUserReevalsParams) ([]sqlcgen.ClaimDueIPFSUserReevalsRow, error)
	DeleteIPFSUserReeval(ctx context.Context, userID uuid.UUID) error
	RescheduleIPFSUserReeval(ctx context.Context, arg sqlcgen.RescheduleIPFSUserReevalParams) error
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
	// OwnerVideoIDs lists EVERY video (any privacy/state) owned by the user, so the
	// unlisted-toggle re-evaluation (ReevaluateUser) can re-run the per-video fence
	// on each; SyncVideo decides pin vs unpin from committed state.
	OwnerVideoIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
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
	// Cluster is the optional IPFS Cluster replication client (STOR-05). Nil unless
	// IPFS_CLUSTER_API_URL is configured. Replication is BEST-EFFORT: a cluster
	// error never fails a ledger row — the local node pin is the authoritative
	// mirror action. In v1 it only ever carries already-node-pinned (public) media
	// (spec §7); private-media mirroring is out of scope.
	Cluster ipfs.ClusterClient
	// Catalog is the read-only source the one-shot admin backfill (P19.6) scans to
	// seed pin intents for pre-existing eligible objects. Nil except in the wiring
	// that serves POST /admin/ipfs/reconcile; Backfill errors cleanly when it is nil.
	Catalog Catalog

	// ---- private mirroring tier (P19.P1) --------------------------------------
	// PrivateEnabled turns on draining the network='private' ledger rows through
	// PrivateClient (the DEDICATED private-swarm kubo — NEVER the public client).
	// When false, no private drain runs and behavior is identical to pre-P19.P.
	PrivateEnabled bool
	// PrivateClient is the private-swarm node client; PrivateCluster its optional
	// replication cluster (P19.P3). A private row is pinned ONLY through these — the
	// worker never falls back to the public client for a private row (spec §8).
	PrivateClient  ipfs.Client
	PrivateCluster ipfs.ClusterClient
	// PrivateAddTimeout / PrivateConcurrency tune the private-network worker; they
	// fall back to the public AddTimeout / Concurrency when unset.
	PrivateAddTimeout  time.Duration
	PrivateConcurrency int
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
	cluster ipfs.ClusterClient
	catalog Catalog

	// Private-tier (P19.P1): the second, swarm.key'd node client + its tunables. Nil
	// / privateEnabled=false ⇒ no private drain (default, no behavior change).
	privateEnabled     bool
	privateClient      ipfs.Client
	privateCluster     ipfs.ClusterClient
	privateAddTimeout  time.Duration
	privateConcurrency int

	// publicEnabled is the PUBLIC tier switch (cfg.Enabled). Kept distinct from the
	// aggregate `enabled` (public OR private) so the eligibility router (P19.P2) can
	// tier-gate: public-routed media is not mirrored when the public tier is off (a
	// private-only deployment), and — the default — private-routed media is not
	// mirrored when the private tier is off (no behavior change vs. pre-P19.P).
	publicEnabled bool

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
		repo:               repo,
		lookups:            lookups,
		blobs:              blobs,
		client:             client,
		cluster:            cfg.Cluster,
		catalog:            cfg.Catalog,
		privateEnabled:     cfg.PrivateEnabled,
		privateClient:      cfg.PrivateClient,
		privateCluster:     cfg.PrivateCluster,
		privateAddTimeout:  cfg.PrivateAddTimeout,
		privateConcurrency: cfg.PrivateConcurrency,
		publicEnabled:      cfg.Enabled,
		// The worker runs whenever EITHER tier is active; the enqueue helpers stay
		// no-ops only when both are off.
		enabled:        cfg.Enabled || cfg.PrivateEnabled,
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
	// Private tunables inherit the public ones when unset.
	if s.privateAddTimeout <= 0 {
		s.privateAddTimeout = s.addTimeout
	}
	if s.privateConcurrency < 1 {
		s.privateConcurrency = s.concurrency
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

// ---- eligibility routing (tier-gated) -------------------------------------

// effectiveNetwork resolves the swarm a subject actually mirrors to on THIS
// instance: Route decides the eligible swarm from visibility facts, then this drops
// it to NetworkNone when that swarm's tier is not active. Two consequences:
//   - DEFAULT (private tier off): private-routed media (private/unlisted videos,
//     unlisted/deactivated owners' images, non-public covers) resolves to NONE — not
//     mirrored — so behavior is identical to pre-P19.P.
//   - private-only deployment (public tier off): public-routed media resolves to NONE.
//
// This is the ONE tier-gate; every enqueue path funnels through it, so a private row
// is never created without the private tier on and vice versa.
func (s *Service) effectiveNetwork(sub Subject) string {
	switch Route(sub) {
	case NetworkPublic:
		if s.publicEnabled {
			return NetworkPublic
		}
	case NetworkPrivate:
		if s.privateEnabled {
			return NetworkPrivate
		}
	}
	return NetworkNone
}

// routePin enqueues/re-arms a pin intent for one object ROUTED to net (a two-phase
// transition when the row currently lives on the other swarm). net must be a real
// network (callers gate on effectiveNetwork != NetworkNone first).
func (s *Service) routePin(ctx context.Context, objectKey string, class MediaClass, videoID, ownerUserID uuid.UUID, net string) error {
	_, err := s.repo.RouteIPFSPinIntent(ctx, sqlcgen.RouteIPFSPinIntentParams{
		ObjectKey:     objectKey,
		MediaClass:    string(class),
		VideoID:       pgUUID(videoID),
		OwnerUserID:   pgUUID(ownerUserID),
		TargetNetwork: net,
	})
	return err
}

// ---- enqueue helpers (authoritative-write hooks) --------------------------

// EnqueueUserImage routes a user's avatar/banner to its eligible swarm (public for
// an active+listed owner, private for an unlisted/deactivated owner when the private
// tier is on) — or removes any existing pin when the image is not mirrored on any
// active tier. No-op when disabled. Best-effort: an enqueue error is returned for
// logging but must never fail the upload (graceful degradation — the reconciliation
// scan is the backstop).
func (s *Service) EnqueueUserImage(ctx context.Context, userID uuid.UUID, class MediaClass, objectKey string) error {
	if !s.enabled {
		return nil
	}
	active, unlisted, ok, err := s.lookups.UserFlags(ctx, userID)
	if err != nil || !ok {
		return err
	}
	net := s.effectiveNetwork(Subject{Class: class, OwnerActive: active, OwnerUnlisted: unlisted})
	if net == NetworkNone {
		// Not mirrored on any active tier: remove any existing pin (a fresh upload with
		// no row is a harmless no-op; a re-upload that became ineligible is cleaned up).
		return s.repo.EnqueueIPFSUnpin(ctx, objectKey)
	}
	return s.routePin(ctx, objectKey, class, uuid.Nil, userID, net)
}

// EnqueueChannelImage routes a channel's avatar/banner to its eligible swarm from the
// owning account's flags, or removes it when not mirrored on any active tier. No-op
// when disabled.
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
	net := s.effectiveNetwork(Subject{Class: class, OwnerActive: active, OwnerUnlisted: unlisted})
	if net == NetworkNone {
		return s.repo.EnqueueIPFSUnpin(ctx, objectKey)
	}
	return s.routePin(ctx, objectKey, class, uuid.Nil, owner, net)
}

// SyncVideo re-evaluates a video's ledger against its current privacy+state AND
// its owner's unlisted flag, and is the single entrypoint wired to the publish,
// privacy-update and unpublish transitions (and the unlisted-toggle re-evaluation),
// so eligibility is always derived from committed state. Every video-derived class
// shares ONE gate (public+published AND owner-listed), so the whole video flips
// together:
//   - eligible ⇒ (re)pin the original (web-videos/<id><ext>), the VP9/WebM
//     alternate, and the image derivatives (thumbnail, storyboard, storyboard
//     vtt, captions) that currently exist. private→public re-pins them all.
//   - ineligible (public→private, unpublish, quarantine, OR the owner going
//     unlisted) ⇒ unpin ALL of the video's ledger rows — including the HLS row
//     added in P19.4 and any orphan rows — so nothing non-public lingers pinned.
//     This is the privacy fence at the video level.
//
// The HLS directory add itself is enqueued on transcode completion in P19.4; the
// unpin-all path here already covers its removal. No-op when disabled.
func (s *Service) SyncVideo(ctx context.Context, videoID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	privacy, state, ownerID, ok, err := s.lookups.VideoVisibility(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	ownerUnlisted, err := s.ownerUnlistedForVideo(ctx, ownerID)
	if err != nil {
		return err
	}
	// A representative video-derived class decides the WHOLE video's swarm (every
	// derivative shares one routing gate). effectiveNetwork tier-gates the result, so
	// with the private tier off a private/unlisted video routes to NONE (unpin all) —
	// identical to pre-P19.P.
	net := s.effectiveNetwork(Subject{Class: ClassVideoOriginal, VideoPrivacy: privacy, VideoState: state, OwnerUnlisted: ownerUnlisted})
	if net == NetworkNone {
		return s.unpinAllForVideo(ctx, videoID)
	}
	// Route every currently-stored single-file ref to net (RouteIPFSPinIntent
	// transitions an existing row across swarms on a privacy flip).
	refs, err := s.videoMirrorRefs(ctx, videoID)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		seen[ref.ObjectKey] = true
		if perr := s.routePin(ctx, ref.ObjectKey, ref.Class, videoID, uuid.Nil, net); perr != nil {
			return perr
		}
	}
	// Transition any EXISTING ledger row the single-file refs don't enumerate —
	// notably the HLS tree (armed by OnTranscodeComplete) and any orphan — so a
	// privacy flip moves the WHOLE video across swarms, not just its single files. A
	// terminal 'unpinned' row is left alone (nothing to move; the transcode hook
	// re-arms HLS on the new swarm when it next runs, matching the P19 HLS lifecycle).
	rows, err := s.repo.ListIPFSPinsByVideo(ctx, pgUUID(videoID))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if seen[r.ObjectKey] || r.State == "unpinned" {
			continue
		}
		if perr := s.routePin(ctx, r.ObjectKey, MediaClass(r.MediaClass), videoID, uuid.Nil, net); perr != nil {
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

// ownerUnlistedForVideo resolves whether a video's owner currently bars mirroring
// of that owner's public videos. Unlisted is treated as PRIVATE for mirroring
// (spec §7): an unlisted owner's public videos and every derivative must be kept
// off the public network, since a permanent public CID would defeat the
// URL-unguessability the unlisted flag promises. Returns true (⇒ ineligible ⇒
// unpin) when the account is unlisted OR its row is missing (default-deny: an
// orphaned/mid-deletion video's derivatives are unpinned, never left pinned).
// Account-active is deliberately NOT gated here — deactivation/deletion is the
// separate re-evaluation trigger handled by the account delete/unpin path (§3).
func (s *Service) ownerUnlistedForVideo(ctx context.Context, ownerID uuid.UUID) (bool, error) {
	_, unlisted, ok, err := s.lookups.UserFlags(ctx, ownerID)
	if err != nil {
		return false, err
	}
	return unlisted || !ok, nil
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
	privacy, state, ownerID, ok, err := s.lookups.VideoVisibility(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	ownerUnlisted, err := s.ownerUnlistedForVideo(ctx, ownerID)
	if err != nil {
		return err
	}
	// The whole video shares one routing gate; ClassHLS is representative.
	// effectiveNetwork resolves the swarm (public for a public+published+listed video,
	// private for a private/unlisted one when that tier is on) and tier-gates it — a
	// quarantined video, or a private one with the private tier off, routes to NONE and
	// arms nothing here (the privacy fence at transcode completion).
	net := s.effectiveNetwork(Subject{Class: ClassHLS, VideoPrivacy: privacy, VideoState: state, OwnerUnlisted: ownerUnlisted})
	if net == NetworkNone {
		return nil
	}
	hlsKey := media.HLSKeyPrefix(videoID) + "/"
	if err := s.repo.RepinIPFSObject(ctx, sqlcgen.RepinIPFSObjectParams{
		ObjectKey:     hlsKey,
		MediaClass:    string(ClassHLS),
		VideoID:       pgUUID(videoID),
		TargetNetwork: net,
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
				ObjectKey:     f.StorageKey,
				MediaClass:    string(ClassWebM),
				VideoID:       pgUUID(videoID),
				TargetNetwork: net,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReevaluateUser re-evaluates ALL of an owner's mirrored media after an unlisted
// toggle or a deactivate/reactivate:
//   - identity images (their own avatar/banner plus their channels'): eligible ⇒
//     (re)pin, ineligible ⇒ unpin.
//   - the owner's videos and every derivative: unlisted is treated as private for
//     mirroring (spec §3/§7), so flipping a user unlisted must pull ALL their
//     public videos (originals, HLS trees, VP9, thumbnails, storyboards, captions)
//     off the public network, and re-listing must re-pin the ones still eligible.
//     Each video is routed through SyncVideo so the per-video fence lives in ONE
//     place — it re-derives the now-updated owner-unlisted fact and either unpins
//     the whole video's ledger rows or re-pins its current refs.
//
// This is the privacy re-evaluation trigger. Everything here is a durable QUEUE
// operation (upsert/unpin ledger writes) — no synchronous node calls — so the
// worker performs the actual pin/unpin off the request path. No-op when disabled.
//
// It runs OFF the request path (the HTTP handler only enqueues a compact job via
// EnqueueUserReeval; the worker calls this while draining the queue), and it is
// PER-ITEM ERROR-ISOLATED (P19 round-2 audit MAJOR): a transient failure on one
// image or video is logged and counted but NEVER aborts the batch, so it can never
// strand the owner's other now-ineligible objects still pinned. When any item failed
// it returns a non-nil error so the worker reschedules the WHOLE job — the items that
// already succeeded are idempotent no-ops on the retry, only the stragglers re-run.
func (s *Service) ReevaluateUser(ctx context.Context, userID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	active, unlisted, ok, err := s.lookups.UserFlags(ctx, userID)
	if err != nil {
		return err
	}
	if !ok {
		// Owner row gone (deleted): nothing to re-evaluate here — the account
		// delete/unpin path (§3) owns cleanup, and the eligibility sweep is the backstop.
		return nil
	}
	var failures int
	refs, err := s.lookups.OwnerImageRefs(ctx, userID)
	if err != nil {
		// Can't enumerate identity images — count it and still process the videos
		// (they enumerate independently), so an image-lookup hiccup never strands the
		// owner's now-ineligible videos.
		s.logger.Warn("ipfs_reeval_images_list_failed", "user_id", userID, "error", err)
		failures++
	}
	for _, ref := range refs {
		net := s.effectiveNetwork(Subject{Class: ref.Class, OwnerActive: active, OwnerUnlisted: unlisted})
		if net != NetworkNone {
			// Route (or transition across swarms) the image to its now-eligible network:
			// unlisting an owner moves their identity assets public→private, re-listing
			// moves them back — both through the ledger transition, no cross-pin.
			if perr := s.routePin(ctx, ref.ObjectKey, ref.Class, uuid.Nil, userID, net); perr != nil {
				s.logger.Warn("ipfs_reeval_image_repin_failed", "object_key", ref.ObjectKey, "error", perr)
				failures++
			}
		} else if uerr := s.repo.EnqueueIPFSUnpin(ctx, ref.ObjectKey); uerr != nil {
			s.logger.Warn("ipfs_reeval_image_unpin_failed", "object_key", ref.ObjectKey, "error", uerr)
			failures++
		}
	}
	// Re-evaluate the owner's videos: SyncVideo re-checks each against the updated
	// owner-unlisted fact (unpin-all when now ineligible, re-pin refs when eligible).
	videoIDs, err := s.lookups.OwnerVideoIDs(ctx, userID)
	if err != nil {
		s.logger.Warn("ipfs_reeval_videos_list_failed", "user_id", userID, "error", err)
		failures++
		videoIDs = nil
	}
	for _, videoID := range videoIDs {
		if serr := s.SyncVideo(ctx, videoID); serr != nil {
			// Per-video isolation: log-and-continue with a counter so one video's
			// transient error reschedules the job (below) without aborting the batch.
			s.logger.Warn("ipfs_reeval_sync_video_failed", "video_id", videoID, "error", serr)
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("ipfsmirror: reevaluate user %s: %d item(s) failed", userID, failures)
	}
	return nil
}

// EnqueueUserReeval records a durable per-user re-evaluation job (P19 round-2 audit
// MAJOR). The HTTP unlisted-toggle / activation-change handler calls THIS — a single
// cheap idempotent upsert — instead of running the SyncVideo fan-out inline on the
// request goroutine; the mirror worker later expands the job via ReevaluateUser with
// per-video error isolation. Committed durably, so a crash right after the toggle
// still converges once the worker resumes. No-op when disabled.
func (s *Service) EnqueueUserReeval(ctx context.Context, userID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	return s.repo.EnqueueIPFSUserReeval(ctx, userID)
}

// DrainDueUserReevals claims up to batch due per-user re-eval jobs and expands each
// via ReevaluateUser (per-item error-isolated). A fully-successful expansion deletes
// the job; a partial failure reschedules it with backoff so the stragglers retry —
// never dead-lettered (a stranded re-eval is a privacy concern). Returns how many
// jobs reached terminal success. Only the claim-query error is surfaced; per-job
// failures are persisted in the queue, never blocking. No-op when disabled.
func (s *Service) DrainDueUserReevals(ctx context.Context, batch int) (int, error) {
	if !s.enabled {
		return 0, nil
	}
	if batch < 1 {
		batch = 1
	}
	jobs, err := s.repo.ClaimDueIPFSUserReevals(ctx, sqlcgen.ClaimDueIPFSUserReevalsParams{
		LeaseSeconds: reevalLeaseSeconds,
		BatchSize:    int32(batch),
	})
	if err != nil {
		return 0, err
	}
	done := 0
	for _, job := range jobs {
		if rerr := s.ReevaluateUser(ctx, job.UserID); rerr != nil {
			attempts := int(job.Attempts) + 1
			_ = s.repo.RescheduleIPFSUserReeval(ctx, sqlcgen.RescheduleIPFSUserReevalParams{
				UserID:        job.UserID,
				NextAttemptAt: time.Now().UTC().Add(s.backoff(attempts)),
				LastError:     "reevaluation partially failed",
			})
			s.logger.Warn("ipfs_user_reeval_rescheduled", "user_id", job.UserID, "attempts", attempts, "error", rerr)
			continue
		}
		if derr := s.repo.DeleteIPFSUserReeval(ctx, job.UserID); derr != nil {
			s.logger.Warn("ipfs_user_reeval_delete_failed", "user_id", job.UserID, "error", derr)
			continue
		}
		done++
	}
	return done, nil
}

// SweepIneligible is the periodic eligibility BACKSTOP (P19 round-2 audit MAJOR): it
// flips every live (pinned/pending) ledger row whose current committed visibility
// facts make it ineligible to 'unpinning' in ONE set-based query, so any missed
// enqueue — a crashed re-eval job, an unlisted/privacy toggle that raced a worker, or
// a future eligibility-rule change — converges toward removal on the next reconcile
// tick without a per-row Go loop. The worker's reference-checked node removal runs
// when it later drains the flipped rows. Returns how many rows it re-armed. No-op
// when disabled.
func (s *Service) SweepIneligible(ctx context.Context) (int64, error) {
	if !s.enabled {
		return 0, nil
	}
	n, err := s.repo.SweepIneligibleIPFSPins(ctx, int32(eligibilitySweepBatch))
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.logger.Info("ipfs eligibility sweep re-armed ineligible pins", "count", n)
	}
	return n, nil
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
	net := s.effectiveNetwork(Subject{Class: ClassPlaylistCover, PlaylistVisibility: vis})
	if net != NetworkNone {
		return s.routePin(ctx, key, ClassPlaylistCover, uuid.Nil, uuid.Nil, net)
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
		// PRIVACY FENCE (P19.P1): the detail `ipfs` object is a PUBLIC-network signal.
		// A private-swarm pin (private/unlisted media replicated for durability) is
		// NEVER exposed — no private CID or gateway URL ever reaches an API payload
		// (spec §5). Only network='public' rows are eligible to surface.
		if normalizeNetwork(r.Network) != networkPublic {
			continue
		}
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
	total := 0
	// Drain each configured network with its OWN client. Public first, deterministic.
	for _, nc := range s.activeNetworks() {
		n, err := s.drainNetwork(ctx, nc, batch)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// activeNetworks returns the netClient for every configured tier: public when a
// public node client is present, private when the private tier is enabled with a
// client. Each bundles its own node/cluster client + tunables, so a per-network drain
// can never cross wires.
func (s *Service) activeNetworks() []netClient {
	var ncs []netClient
	if s.client != nil {
		ncs = append(ncs, netClient{
			network: networkPublic, client: s.client, cluster: s.cluster,
			addTimeout: s.addTimeout, concurrency: s.concurrency,
		})
	}
	if s.privateEnabled && s.privateClient != nil {
		ncs = append(ncs, netClient{
			network: networkPrivate, client: s.privateClient, cluster: s.privateCluster,
			addTimeout: s.privateAddTimeout, concurrency: s.privateConcurrency,
		})
	}
	return ncs
}

// drainNetwork claims and processes up to batch due rows for ONE network, using that
// network's client exclusively. ClaimDue's @network filter returns only this
// network's rows, so process() only ever touches nc's client — the public client is
// not even in scope when nc is the private tier (the cardinal invariant, spec §8).
func (s *Service) drainNetwork(ctx context.Context, nc netClient, batch int) (int, error) {
	rows, err := s.repo.ClaimDueIPFSPins(ctx, sqlcgen.ClaimDueIPFSPinsParams{
		Network:      nc.network,
		LeaseSeconds: leaseSeconds,
		BatchSize:    int32(batch),
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	sem := make(chan struct{}, nc.concurrency)
	var wg sync.WaitGroup
	var done int64
	for _, row := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func(row sqlcgen.ClaimDueIPFSPinsRow) {
			defer wg.Done()
			defer func() { <-sem }()
			if s.process(ctx, nc, row) {
				atomic.AddInt64(&done, 1)
			}
		}(row)
	}
	wg.Wait()
	return int(done), nil
}

// process handles one claimed row through nc's client within a per-RPC timeout.
// Returns true on a terminal success (pinned/unpinned).
func (s *Service) process(ctx context.Context, nc netClient, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	// Fail-closed routing defense: the row MUST belong to the network we hold a
	// client for. ClaimDue's @network filter already guarantees this — but if a row's
	// network ever mismatched the drain, we RESCHEDULE it rather than pin it through
	// the wrong client. The public client must be provably incapable of pinning a
	// private row (spec §8); this is the last-line assertion of that invariant.
	if normalizeNetwork(row.Network) != nc.network {
		s.logger.Warn("ipfs_route_mismatch", "row_network", row.Network, "drain_network", nc.network,
			"object_key", row.ObjectKey)
		_ = s.repo.RescheduleIPFSPin(ctx, sqlcgen.RescheduleIPFSPinParams{
			ObjectKey:     row.ObjectKey,
			NextAttemptAt: time.Now().UTC().Add(s.backoff(1)),
			LastError:     "network route mismatch",
		})
		return false
	}
	rpcCtx, cancel := context.WithTimeout(ctx, nc.addTimeout)
	defer cancel()
	switch row.State {
	case "pending":
		return s.pin(rpcCtx, nc, row)
	case "unpinning":
		return s.unpin(rpcCtx, nc, row)
	default:
		return false
	}
}

// pin streams the object's bytes from the authoritative store and add+pins them.
// A trailing-slash object key is a directory intent (the HLS tree) → pinDirectory.
func (s *Service) pin(ctx context.Context, nc netClient, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	if strings.HasSuffix(row.ObjectKey, "/") {
		return s.pinDirectory(ctx, nc, row)
	}
	rc, err := s.blobs.Open(ctx, row.ObjectKey)
	if err != nil {
		s.recordFailure(ctx, row, "open source object failed")
		s.logger.Warn("ipfs_pin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "open_source", "error", err)
		return false
	}
	res, err := nc.client.Add(ctx, path.Base(row.ObjectKey), rc)
	_ = rc.Close()
	if err != nil {
		s.recordFailure(ctx, row, "add+pin rpc failed")
		s.logger.Warn("ipfs_pin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "add_pin", "error", err)
		return false
	}
	state, err := s.repo.MarkIPFSPinned(ctx, sqlcgen.MarkIPFSPinnedParams{
		ObjectKey: row.ObjectKey, Cid: res.CID, CarRoot: "", ByteSize: res.Size,
	})
	if err != nil {
		s.logger.Warn("ipfs mark pinned failed", "object_key", row.ObjectKey, "error", err)
		return false
	}
	if state != "pinned" {
		// Lost the lost-update race: a concurrent privacy flip flipped the leased row
		// to 'unpinning' mid-Add, so MarkIPFSPinned recorded the CID but KEPT the row
		// 'unpinning'. The bytes are now pinned on THIS network's node for a non-public
		// object — remove them immediately (row.Cid carries any superseded prior CID).
		return s.pinRacedUnpin(ctx, nc, row, res.CID, row.Cid, state)
	}
	// Best-effort cluster replication (STOR-05) of the now-node-pinned CID.
	s.clusterPin(ctx, nc, res.CID)
	// A re-pin under a STABLE key (e.g. a re-transcoded VP9/WebM alternate) whose
	// content changed leaves the prior CID orphaned — swap it out (reference-checked).
	s.swapUnpin(ctx, nc, row.Cid, res.CID, row.ObjectKey)
	// Info level carries NO cid (CIDs are public capability handles — no spam);
	// the cid is debug-only.
	s.logger.Info("ipfs_pin_ok", "network", nc.network, "media_class", row.MediaClass, "byte_size", res.Size, "attempts", row.Attempts+1)
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
func (s *Service) pinDirectory(ctx context.Context, nc netClient, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	lister, ok := s.blobs.(storage.ObjectLister)
	if !ok {
		s.recordFailure(ctx, row, "backend cannot list a directory tree")
		s.logger.Warn("ipfs_pin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "no_object_lister")
		return false
	}
	prefix := row.ObjectKey
	keys, err := lister.ListKeys(ctx, prefix)
	if err != nil {
		s.recordFailure(ctx, row, "list directory tree failed")
		s.logger.Warn("ipfs_pin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
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
		s.logger.Warn("ipfs_pin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "empty_tree")
		return false
	}
	res, err := nc.client.AddDirectory(ctx, entries)
	if err != nil {
		s.recordFailure(ctx, row, "directory add+pin rpc failed")
		s.logger.Warn("ipfs_pin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
			"attempts", row.Attempts+1, "reason", "add_dir", "error", err)
		return false
	}
	state, err := s.repo.MarkIPFSPinned(ctx, sqlcgen.MarkIPFSPinnedParams{
		ObjectKey: row.ObjectKey, Cid: res.CID, CarRoot: res.CID, ByteSize: res.Size,
	})
	if err != nil {
		s.logger.Warn("ipfs mark pinned failed", "object_key", row.ObjectKey, "error", err)
		return false
	}
	if state != "pinned" {
		// Lost the lost-update race with a concurrent privacy flip mid-AddDirectory:
		// the tree is pinned on this network's node for a now-non-public video. Remove
		// the new car_root now (row.CarRoot carries any superseded prior root to GC too).
		return s.pinRacedUnpin(ctx, nc, row, res.CID, row.CarRoot, state)
	}
	// Best-effort cluster replication (STOR-05) of the now-node-pinned car_root.
	s.clusterPin(ctx, nc, res.CID)
	// Re-transcode swap: a prior car_root that differs from the new tree is orphaned.
	s.swapUnpin(ctx, nc, row.CarRoot, res.CID, row.ObjectKey)
	s.logger.Info("ipfs_pin_ok", "network", nc.network, "media_class", row.MediaClass, "byte_size", res.Size,
		"attempts", row.Attempts+1, "files", len(entries))
	s.logger.Debug("ipfs_pin_ok cid", "object_key", row.ObjectKey, "cid", res.CID)
	return true
}

// pinRacedUnpin cleans up a pin that LOST the lost-update race with a concurrent
// privacy flip: MarkIPFSPinned recorded the freshly-added CID but KEPT the row
// 'unpinning' (the object went non-public while the Add was streaming), so the
// bytes are now pinned on a public node for a non-public object — a privacy leak
// the reconcile scan would NOT catch (it only re-arms 'failed' rows). Rather than
// wait for the next drain to claim the 'unpinning' row, this removes the bytes NOW:
// it GCs any superseded prior root (oldCID) and then runs the reference-checked
// node unpin of the new CID, converging the row to 'unpinned'. Returns true only
// when the node pin was actually removed (row reached the 'unpinned' terminal).
func (s *Service) pinRacedUnpin(ctx context.Context, nc netClient, row sqlcgen.ClaimDueIPFSPinsRow, newCID, oldCID, racedState string) bool {
	// Structured, CID-free at info: the CID is a public capability handle.
	s.logger.Warn("ipfs_pin_raced_unpin", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
		"raced_state", racedState, "attempts", row.Attempts+1)
	// A superseded prior root (re-transcode / webm refresh in flight) is also
	// orphaned by this add — clean it (reference-checked, no-op when absent/unchanged).
	s.swapUnpin(ctx, nc, oldCID, newCID, row.ObjectKey)
	// Reference-checked node unpin of the just-pinned bytes + terminal ledger state.
	// unpin() reads row.Cid/row.State, so point them at the losing pin's outcome.
	row.Cid = newCID
	row.State = "unpinning"
	return s.unpin(ctx, nc, row)
}

// swapUnpin removes a superseded CID from the node after a re-pin replaced a
// stable-key object's content (an HLS re-transcode's old car_root, a refreshed
// VP9/WebM alternate's old CID). It is reference-checked — a CID still shared by
// another live ledger row (content-address dedupe) is left pinned. A first pin
// (oldCID=="") or unchanged content (oldCID==newCID) is a no-op. Best-effort: an
// unpin failure is logged, not surfaced (the row is already pinned to the new CID).
func (s *Service) swapUnpin(ctx context.Context, nc netClient, oldCID, newCID, objectKey string) {
	if oldCID == "" || oldCID == newCID || ipfs.ValidateCID(oldCID) != nil {
		return
	}
	shared, err := s.repo.CountIPFSPinsSharingCID(ctx, sqlcgen.CountIPFSPinsSharingCIDParams{
		Cid: oldCID, ObjectKey: objectKey, Network: nc.network,
	})
	if err != nil || shared > 0 {
		return
	}
	if err := nc.client.Unpin(ctx, oldCID); err != nil {
		s.logger.Warn("ipfs_unpin_failed", "network", nc.network, "object_key", objectKey, "reason", "swap_superseded", "error", err)
		return
	}
	// Mirror the node unpin to the cluster (best-effort) so a superseded CID does
	// not linger replicated.
	s.clusterUnpin(ctx, nc, oldCID)
	s.logger.Info("ipfs_unpin_ok", "network", nc.network, "object_key", objectKey, "reason", "superseded")
}

// clusterPin best-effort replicates a node-pinned CID across the IPFS Cluster
// (STOR-05). A no-op when no cluster is configured. A cluster error is logged, not
// surfaced — replication is a redundancy enhancement, not the authoritative mirror
// action (the local node pin already succeeded). The CID is debug-only (a public
// capability handle — no info-level spam), matching the node-pin log discipline.
func (s *Service) clusterPin(ctx context.Context, nc netClient, cid string) {
	if nc.cluster == nil || cid == "" {
		return
	}
	if err := nc.cluster.ClusterPin(ctx, cid); err != nil {
		s.logger.Warn("ipfs_cluster_pin_failed", "network", nc.network, "reason", "replicate", "error", err)
		return
	}
	s.logger.Debug("ipfs_cluster_pin_ok", "network", nc.network, "cid", cid)
}

// clusterUnpin best-effort removes a cluster pin after the node unpin. A no-op
// when no cluster is configured; a cluster error is logged, not surfaced.
func (s *Service) clusterUnpin(ctx context.Context, nc netClient, cid string) {
	if nc.cluster == nil || cid == "" {
		return
	}
	if err := nc.cluster.ClusterUnpin(ctx, cid); err != nil {
		s.logger.Warn("ipfs_cluster_unpin_failed", "network", nc.network, "reason", "replicate", "error", err)
		return
	}
	s.logger.Debug("ipfs_cluster_unpin_ok", "network", nc.network, "cid", cid)
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
func (s *Service) unpin(ctx context.Context, nc netClient, row sqlcgen.ClaimDueIPFSPinsRow) bool {
	if row.Cid == "" {
		// Enqueued for pin then removed before it was ever pinned: nothing on the
		// node to remove.
		if err := s.repo.MarkIPFSPinUnpinned(ctx, row.ObjectKey); err != nil {
			return false
		}
		return true
	}
	shared, err := s.repo.CountIPFSPinsSharingCID(ctx, sqlcgen.CountIPFSPinsSharingCIDParams{
		Cid: row.Cid, ObjectKey: row.ObjectKey, Network: nc.network,
	})
	if err != nil {
		s.recordFailure(ctx, row, "reference check failed")
		return false
	}
	if shared == 0 {
		if err := nc.client.Unpin(ctx, row.Cid); err != nil {
			s.recordFailure(ctx, row, "unpin rpc failed")
			s.logger.Warn("ipfs_unpin_failed", "network", nc.network, "media_class", row.MediaClass, "object_key", row.ObjectKey,
				"attempts", row.Attempts+1, "error", err)
			return false
		}
		// Mirror the node unpin to the cluster (best-effort).
		s.clusterUnpin(ctx, nc, row.Cid)
	} else {
		s.logger.Debug("ipfs unpin skipped: cid still referenced", "object_key", row.ObjectKey, "shared", shared)
	}
	if err := s.repo.MarkIPFSPinUnpinned(ctx, row.ObjectKey); err != nil {
		return false
	}
	s.logger.Info("ipfs_unpin_ok", "network", nc.network, "media_class", row.MediaClass, "attempts", row.Attempts+1)
	return true
}

// recordFailure reschedules with exponential backoff, or dead-letters to 'failed'
// after maxAttempts. reason is a SAFE, client-invisible category — never the raw
// node error verbatim (which is logged separately).
func (s *Service) recordFailure(ctx context.Context, row sqlcgen.ClaimDueIPFSPinsRow, reason string) {
	attempts := int(row.Attempts) + 1
	if attempts >= s.maxAttempts {
		// State-guarded dead-letter: only fail the row if it is STILL in the state the
		// worker claimed and acted on (row.State — 'pending' for a pin, 'unpinning' for
		// an unpin). A concurrent privacy flip that re-directed it wins (its newer
		// intent survives) instead of being clobbered to 'failed'.
		_ = s.repo.MarkIPFSPinFailed(ctx, sqlcgen.MarkIPFSPinFailedParams{
			ObjectKey: row.ObjectKey, LastError: reason, ExpectedState: row.State,
		})
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

// NetworkStatus is one swarm's slice of the admin status (P19.P2): whether that tier
// is enabled, its node's reachability, its optional replication-cluster health
// (P19.P3), and its pin tally overall + per class. The public block carries the
// pre-P19.P public-mirror counts; the private block is the replication tier's health +
// pinset. NO gateway URL and NO CIDs appear here — the private swarm is replication,
// not distribution (spec §5).
type NetworkStatus struct {
	Enabled          bool
	NodeReachable    bool
	ClusterEnabled   bool
	ClusterReachable bool
	Pins             PinCounts
	ByClass          []ClassCounts
}

// Status is the admin mirror status (backs GET /api/v1/ipfs/status). The top-level
// Pins/ByClass/NodeReachable FOLD across swarms for back-compat; Networks splits them
// per swarm (P19.P2) so the admin UI renders a public + private column.
type Status struct {
	Enabled          bool
	NodeReachable    bool
	GatewayURL       string
	ClusterEnabled   bool
	ClusterReachable bool
	Pins             PinCounts
	ByClass          []ClassCounts
	// Networks is the additive per-swarm split, keyed NetworkPublic / NetworkPrivate.
	Networks map[string]NetworkStatus
}

// Status aggregates ledger counts (overall + per class) and probes node health.
// The health probe never fails the call — IPFS is non-authoritative, so an
// unreachable node yields node_reachable=false, not an error. When an IPFS Cluster
// is configured its /id endpoint is probed the same way for cluster_reachable.
func (s *Service) Status(ctx context.Context) (Status, error) {
	st := Status{Enabled: s.enabled, GatewayURL: s.gatewayURL, ClusterEnabled: s.clusterEnabled}
	if s.client != nil {
		if _, err := s.client.Version(ctx); err == nil {
			st.NodeReachable = true
		} else {
			s.logger.Debug("ipfs_node_unhealthy", "error", err)
		}
	}
	if s.cluster != nil {
		if err := s.cluster.ClusterHealth(ctx); err == nil {
			st.ClusterReachable = true
		} else {
			s.logger.Debug("ipfs_cluster_unhealthy", "error", err)
		}
	}
	// Per-swarm probe (P19.P2/P19.P3): the private tier has its OWN node AND its OWN
	// optional replication cluster, both independent of the public ones. The top-level
	// NodeReachable/ClusterReachable stay the PUBLIC swarm's (back-compat); the private
	// swarm's health lives in Networks[private].
	privateReachable := false
	if s.privateEnabled && s.privateClient != nil {
		if _, err := s.privateClient.Version(ctx); err == nil {
			privateReachable = true
		} else {
			s.logger.Debug("ipfs_private_node_unhealthy", "error", err)
		}
	}
	privateClusterEnabled := s.privateCluster != nil
	privateClusterReachable := false
	if privateClusterEnabled {
		if err := s.privateCluster.ClusterHealth(ctx); err == nil {
			privateClusterReachable = true
		} else {
			s.logger.Debug("ipfs_private_cluster_unhealthy", "error", err)
		}
	}

	// Ledger counts are per (network, state, class). Fold across swarms for the
	// top-level totals (back-compat) AND accumulate a per-swarm split for Networks.
	rows, err := s.repo.CountIPFSPinsByNetworkStateClass(ctx)
	if err != nil {
		return st, err
	}
	byClass := map[string]*ClassCounts{}
	// Per-network accumulators: pins tally + per-class map.
	netPins := map[string]*PinCounts{}
	netByClass := map[string]map[string]*ClassCounts{}
	for _, r := range rows {
		net := normalizeNetwork(r.Network)
		addStateCount(&st.Pins, r.State, r.Count)
		cc := byClass[r.MediaClass]
		if cc == nil {
			cc = &ClassCounts{MediaClass: r.MediaClass}
			byClass[r.MediaClass] = cc
		}
		addStateCount(&cc.PinCounts, r.State, r.Count)

		if netPins[net] == nil {
			netPins[net] = &PinCounts{}
			netByClass[net] = map[string]*ClassCounts{}
		}
		addStateCount(netPins[net], r.State, r.Count)
		ncc := netByClass[net][r.MediaClass]
		if ncc == nil {
			ncc = &ClassCounts{MediaClass: r.MediaClass}
			netByClass[net][r.MediaClass] = ncc
		}
		addStateCount(&ncc.PinCounts, r.State, r.Count)
	}
	st.ByClass = sortClassCounts(byClass)

	// Always publish both swarm blocks (stable two-key object for the admin UI), even
	// when a tier has no rows. Enabled/NodeReachable come from config + the probes.
	st.Networks = map[string]NetworkStatus{
		NetworkPublic: {
			Enabled:          s.publicEnabled,
			NodeReachable:    st.NodeReachable,
			ClusterEnabled:   st.ClusterEnabled,
			ClusterReachable: st.ClusterReachable,
			Pins:             derefPins(netPins[NetworkPublic]),
			ByClass:          sortClassCounts(netByClass[NetworkPublic]),
		},
		NetworkPrivate: {
			Enabled:          s.privateEnabled,
			NodeReachable:    privateReachable,
			ClusterEnabled:   privateClusterEnabled,
			ClusterReachable: privateClusterReachable,
			Pins:             derefPins(netPins[NetworkPrivate]),
			ByClass:          sortClassCounts(netByClass[NetworkPrivate]),
		},
	}
	return st, nil
}

// sortClassCounts flattens a media-class → counts map into a class-sorted slice.
func sortClassCounts(byClass map[string]*ClassCounts) []ClassCounts {
	names := make([]string, 0, len(byClass))
	for name := range byClass {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ClassCounts, 0, len(names))
	for _, name := range names {
		out = append(out, *byClass[name])
	}
	return out
}

// derefPins returns the tally or a zero value when the swarm had no rows.
func derefPins(p *PinCounts) PinCounts {
	if p == nil {
		return PinCounts{}
	}
	return *p
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
