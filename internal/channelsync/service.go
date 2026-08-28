// Package channelsync implements channel auto-sync (backport W2.C4, UPLOAD-13): a
// binding that mirrors an external platform channel's recent uploads into a LOCAL
// channel the caller owns. A periodic worker lists the remote channel through the
// sandboxed yt-dlp extractor (--flat-playlist) and, for each not-yet-seen entry,
// creates a draft video in the target channel and enqueues a `ytdlp` URL import
// (W2.C1). Downloaded bytes therefore reach storage ONLY through the existing
// video.AttachOriginal → video.Process pipeline, so the fail-closed ClamAV scan
// hook fires before anything is servable — the W2 ingestion invariant is
// preserved by construction (no second write path into blob storage).
//
// The whole feature is OFF by default and gated on BOTH CHANNEL_SYNC_ENABLED and
// the yt-dlp import resolver being enabled; when off, the HTTP surface answers
// 503 and no worker runs. Every stored last_error is a SAFE, client-visible
// reason (never a raw internal error, never credentials). It is HTTP-agnostic and
// testable with fake collaborators.
package channelsync

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/safeerr"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/ytdlp"
)

// maxSyncErrorLen bounds the stored (client-visible) last_error string.
const maxSyncErrorLen = 300

// syncedDraftPrivacy is the privacy a channel-synced draft video is created with.
// Synced content lands PRIVATE for the owner to review and publish — an instance
// never auto-publishes mirrored third-party uploads to the public feed. The owner
// promotes each one from their studio.
const syncedDraftPrivacy = "private"

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrDisabled means channel auto-sync is off on this instance (503).
	ErrDisabled = errors.New("channelsync: disabled")
	// ErrInvalidURL means the external channel URL is missing, malformed,
	// non-http(s), or a literal non-public address (422).
	ErrInvalidURL = errors.New("channelsync: invalid external channel url")
	// ErrChannelNotFound means the referenced local channel does not exist (404).
	ErrChannelNotFound = errors.New("channelsync: channel not found")
	// ErrForbidden means the caller does not own the referenced channel/sync (404
	// at the boundary — an unowned resource is invisible).
	ErrForbidden = errors.New("channelsync: not owner")
	// ErrNotFound means no channel sync matches the lookup (404).
	ErrNotFound = errors.New("channelsync: not found")
	// ErrConflict means an identical (channel, external URL) sync already exists
	// (409).
	ErrConflict = errors.New("channelsync: already exists")
	// ErrMaxReached means the caller is at CHANNEL_SYNC_MAX_PER_USER (422).
	ErrMaxReached = errors.New("channelsync: per-user limit reached")
	// ErrCooldown means a manual sync-now was requested less than the configured
	// cooldown after the last completed run (429). It throttles a caller from
	// forcing fresh external listings faster than CHANNEL_SYNC_COOLDOWN.
	ErrCooldown = errors.New("channelsync: sync-now cooldown active")
)

// Repository is the data access the service needs. *sqlcgen.Queries satisfies it
// directly; tests substitute an in-memory fake.
type Repository interface {
	GetChannelByID(ctx context.Context, id uuid.UUID) (sqlcgen.Channel, error)

	CreateChannelSync(ctx context.Context, arg sqlcgen.CreateChannelSyncParams) (sqlcgen.ChannelSync, error)
	ListChannelSyncsByUser(ctx context.Context, arg sqlcgen.ListChannelSyncsByUserParams) ([]sqlcgen.ChannelSync, error)
	GetChannelSyncByID(ctx context.Context, id uuid.UUID) (sqlcgen.ChannelSync, error)
	CountChannelSyncsByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteChannelSync(ctx context.Context, id uuid.UUID) error
	TriggerChannelSyncNow(ctx context.Context, id uuid.UUID) error

	ClaimDueChannelSyncs(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueChannelSyncsRow, error)
	RenewChannelSyncLease(ctx context.Context, id uuid.UUID) error
	FinishChannelSync(ctx context.Context, arg sqlcgen.FinishChannelSyncParams) error
	FailChannelSync(ctx context.Context, arg sqlcgen.FailChannelSyncParams) error
	InsertChannelSyncSeen(ctx context.Context, arg sqlcgen.InsertChannelSyncSeenParams) (int64, error)
}

// Drafter creates a draft video in a channel. *video.Service satisfies it.
type Drafter interface {
	CreateDraft(ctx context.Context, channelID uuid.UUID, in video.CreateInput) (sqlcgen.Video, error)
}

// Enqueuer enqueues a URL import for a video. *videoimport.Service satisfies it.
type Enqueuer interface {
	Enqueue(ctx context.Context, videoID uuid.UUID, rawURL, requestedResolver string) (sqlcgen.ImportJob, error)
}

// Lister lists a remote channel's recent uploads. *ytdlp.Client satisfies it;
// tests inject a stub so the sync path runs without the real binary.
type Lister interface {
	Playlist(ctx context.Context, channelURL string, limit int) ([]ytdlp.PlaylistEntry, error)
}

// Service holds the channel auto-sync logic.
type Service struct {
	repo     Repository
	drafter  Drafter
	enqueuer Enqueuer
	lister   Lister

	enabled      bool
	enabledFn    func() bool // when set, supersedes enabled (runtime overlay), resolved per call
	allowPrivate bool
	maxPerUser   int
	maxPerUserFn func() int // when set, supersedes maxPerUser (runtime overlay), resolved per Create
	batch        int
	interval     time.Duration
	cooldown     time.Duration
	logger       *slog.Logger
}

// Option customises the Service.
type Option func(*Service)

// WithEnabled turns the feature on. Wire true only when CHANNEL_SYNC_ENABLED AND
// the yt-dlp import resolver are both on; when false the HTTP surface 503s and
// the worker is a no-op.
func WithEnabled(enabled bool) Option { return func(s *Service) { s.enabled = enabled } }

// WithEnabledFunc makes the feature gate dynamic (channel_sync_enabled,
// config-parity W8): f is resolved per call — sync-create, sync-now, and every
// worker tick's DrainDue — so an admin can pause/resume channel auto-sync
// without a restart (the worker stays constructed and simply picks nothing up
// while off). When set it supersedes WithEnabled; wire f to fold in the boot
// capability (the yt-dlp resolver) so a runtime toggle can never enable a sync
// path the deployment cannot run.
func WithEnabledFunc(f func() bool) Option { return func(s *Service) { s.enabledFn = f } }

// WithAllowPrivateURLs relaxes the SSRF guard's private/loopback block for the
// external channel URL (dev/test only, from HTTP_IMPORT_ALLOW_PRIVATE_URLS).
func WithAllowPrivateURLs(allow bool) Option { return func(s *Service) { s.allowPrivate = allow } }

// WithMaxPerUser caps how many syncs one user may hold (CHANNEL_SYNC_MAX_PER_USER;
// <= 0 disables the cap).
func WithMaxPerUser(n int) Option { return func(s *Service) { s.maxPerUser = n } }

// WithMaxPerUserFunc makes the per-user cap dynamic (channel_sync_max_per_user,
// config-parity W8): f is resolved per Create so an admin can retune the cap at
// runtime. When set it supersedes WithMaxPerUser. <= 0 disables the cap.
func WithMaxPerUserFunc(f func() int) Option { return func(s *Service) { s.maxPerUserFn = f } }

// WithBatch caps how many playlist entries one sync pass imports
// (CHANNEL_SYNC_BATCH).
func WithBatch(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.batch = n
		}
	}
}

// WithInterval sets the cadence used to reschedule a sync after a run
// (CHANNEL_SYNC_INTERVAL).
func WithInterval(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithCooldown sets the minimum spacing between manual sync-now triggers,
// measured from the last completed run (CHANNEL_SYNC_COOLDOWN). A request inside
// the window is rejected with ErrCooldown (429). <= 0 disables the throttle.
func WithCooldown(d time.Duration) Option {
	return func(s *Service) {
		if d >= 0 {
			s.cooldown = d
		}
	}
}

// WithLister wires the yt-dlp channel lister. Required for the worker to do any
// real work; without it a due sync fails safely ("channel sync is not available").
func WithLister(l Lister) Option { return func(s *Service) { s.lister = l } }

// WithLogger overrides the logger used for unexpected (non-client-facing) errors.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewService builds the channel-sync service. drafter/enqueuer are the video +
// import seams the worker drives.
func NewService(repo Repository, drafter Drafter, enqueuer Enqueuer, opts ...Option) *Service {
	s := &Service{
		repo:       repo,
		drafter:    drafter,
		enqueuer:   enqueuer,
		maxPerUser: 5,
		batch:      15,
		interval:   time.Hour,
		cooldown:   time.Minute,
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Enabled reports whether channel auto-sync is EFFECTIVELY on: the runtime
// provider when wired (which folds in the boot capability), else the static
// boot flag (CHANNEL_SYNC_ENABLED and the yt-dlp import resolver). The
// handlers refuse up front when it is off.
func (s *Service) Enabled() bool {
	if s.enabledFn != nil {
		return s.enabledFn()
	}
	return s.enabled
}

// effectiveMaxPerUser resolves the current per-user sync cap (<= 0 = no cap):
// the live overlay value when a provider is wired, else the static value.
func (s *Service) effectiveMaxPerUser() int {
	if s.maxPerUserFn != nil {
		return s.maxPerUserFn()
	}
	return s.maxPerUser
}

// Interval is the configured sync cadence (used by the worker ticker).
func (s *Service) Interval() time.Duration { return s.interval }

// Cooldown is the minimum spacing enforced between manual sync-now triggers (the
// handler uses it to set Retry-After on a 429). 0 means the throttle is off.
func (s *Service) Cooldown() time.Duration { return s.cooldown }

func (s *Service) guard() urlsafety.Guard { return urlsafety.Guard{AllowPrivate: s.allowPrivate} }

// Create binds a local channel (owned by userID) to an external channel URL for
// auto-sync. It validates the URL (SSRF guard), enforces channel ownership and
// the per-user cap, and creates the sync in waiting_first_run (due immediately).
// Errors: ErrDisabled, ErrInvalidURL, ErrChannelNotFound, ErrForbidden,
// ErrMaxReached, ErrConflict.
func (s *Service) Create(ctx context.Context, userID, channelID uuid.UUID, externalURL string) (sqlcgen.ChannelSync, error) {
	if !s.Enabled() {
		return sqlcgen.ChannelSync{}, ErrDisabled
	}
	target, err := s.guard().ValidateURL(strings.TrimSpace(externalURL))
	if err != nil {
		return sqlcgen.ChannelSync{}, ErrInvalidURL
	}
	ch, err := s.repo.GetChannelByID(ctx, channelID)
	if err != nil {
		return sqlcgen.ChannelSync{}, ErrChannelNotFound
	}
	if ch.OwnerID != userID {
		return sqlcgen.ChannelSync{}, ErrForbidden
	}
	if max := s.effectiveMaxPerUser(); max > 0 {
		count, err := s.repo.CountChannelSyncsByUser(ctx, userID)
		if err != nil {
			return sqlcgen.ChannelSync{}, err
		}
		if int(count) >= max {
			return sqlcgen.ChannelSync{}, ErrMaxReached
		}
	}
	sync, err := s.repo.CreateChannelSync(ctx, sqlcgen.CreateChannelSyncParams{
		ChannelID:          channelID,
		UserID:             userID,
		ExternalChannelUrl: target.String(),
	})
	if err != nil {
		if pgconv.IsUniqueViolation(err) {
			return sqlcgen.ChannelSync{}, ErrConflict
		}
		return sqlcgen.ChannelSync{}, err
	}
	return sync, nil
}

// ListForUser returns all channel syncs owned by userID (newest first).
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]sqlcgen.ChannelSync, int64, error) {
	rows, err := s.repo.ListChannelSyncsByUser(ctx, sqlcgen.ListChannelSyncsByUserParams{
		UserID:       userID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	// CountChannelSyncsByUser already existed for the per-user cap; it asks the
	// same question as this list, so it is the total.
	total, err := s.repo.CountChannelSyncsByUser(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Delete removes a channel sync the caller owns. Unknown id → ErrNotFound; a sync
// owned by someone else → ErrForbidden (the handler maps both to 404).
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	sync, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteChannelSync(ctx, sync.ID)
}

// SyncNow schedules an owned sync to run on the next worker tick. Unknown id →
// ErrNotFound; not owner → ErrForbidden. A server-side cooldown throttles repeat
// manual triggers: if the last run completed less than s.cooldown ago the request
// is rejected with ErrCooldown (429) rather than forcing another external listing.
func (s *Service) SyncNow(ctx context.Context, userID, id uuid.UUID) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	sync, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	if s.cooldown > 0 && sync.LastSyncAt.Valid {
		if since := time.Since(sync.LastSyncAt.Time); since < s.cooldown {
			return ErrCooldown
		}
	}
	return s.repo.TriggerChannelSyncNow(ctx, sync.ID)
}

// owned fetches a sync and verifies ownership.
func (s *Service) owned(ctx context.Context, userID, id uuid.UUID) (sqlcgen.ChannelSync, error) {
	sync, err := s.repo.GetChannelSyncByID(ctx, id)
	if err != nil {
		return sqlcgen.ChannelSync{}, ErrNotFound
	}
	if sync.UserID != userID {
		return sqlcgen.ChannelSync{}, ErrForbidden
	}
	return sync, nil
}

// DrainDue claims up to limit due syncs and runs each. A per-sync failure is
// recorded on the row (state=failed, safe last_error) and rescheduled; only the
// claim-query error is returned. Returns the number of syncs that completed
// successfully. Intended to be called on a ticker by a single worker.
func (s *Service) DrainDue(ctx context.Context, limit int) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	rows, err := s.repo.ClaimDueChannelSyncs(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range rows {
		if serr := s.runSync(ctx, row); serr != nil {
			s.recordFailure(ctx, row.ID, serr)
			continue
		}
		_ = s.repo.FinishChannelSync(ctx, sqlcgen.FinishChannelSyncParams{
			ID:        row.ID,
			NextRunAt: s.nextRun(),
		})
		done++
	}
	return done, nil
}

// runSync performs one sync pass: list the remote channel, then for each unseen
// entry create a draft + enqueue a ytdlp import. Per-ENTRY failures (a bad URL, a
// quota rejection, a transient draft/enqueue error) are logged and skipped so one
// bad upload never fails the whole sync; only a lister failure (or an unusable
// config) fails the pass. A returned error is always a SAFE reason.
func (s *Service) runSync(ctx context.Context, row sqlcgen.ClaimDueChannelSyncsRow) error {
	if s.lister == nil {
		return safeerr.New("channel sync is not available")
	}
	target, err := s.guard().ValidateURL(strings.TrimSpace(row.ExternalChannelUrl))
	if err != nil {
		return safeerr.New("the channel URL is not a public http(s) URL")
	}
	entries, err := s.lister.Playlist(ctx, target.String(), s.batch)
	if err != nil {
		return safeerr.New("could not list the external channel")
	}
	// Defense in depth: --playlist-end already bounds the listing, but do NOT
	// trust the extractor to have honored it. Clamp to s.batch so a misbehaving or
	// malicious channel listing can never make one pass draft/enqueue more than the
	// configured batch (bounding both work and quota consumption per run).
	if s.batch > 0 && len(entries) > s.batch {
		entries = entries[:s.batch]
	}
	for _, entry := range entries {
		s.importEntry(ctx, row, entry)
	}
	return nil
}

// importEntry claims one playlist entry via the dedupe ledger and, when it is
// new, creates a draft video and enqueues its ytdlp import. Every failure here is
// per-entry: it is logged (never with credentials) and swallowed so the sync as a
// whole stays healthy.
func (s *Service) importEntry(ctx context.Context, row sqlcgen.ClaimDueChannelSyncsRow, entry ytdlp.PlaylistEntry) {
	inserted, err := s.repo.InsertChannelSyncSeen(ctx, sqlcgen.InsertChannelSyncSeenParams{
		SyncID:     row.ID,
		ExternalID: entry.ExternalID,
	})
	if err != nil {
		s.logger.Warn("channel sync dedupe insert failed", "sync", row.ID, "error", err)
		return
	}
	if inserted == 0 {
		return // already handled on an earlier pass
	}
	draft, err := s.drafter.CreateDraft(ctx, row.ChannelID, video.CreateInput{
		Title:   entry.Title,
		Privacy: syncedDraftPrivacy,
	})
	if err != nil {
		s.logger.Warn("channel sync draft create failed", "sync", row.ID, "error", err)
		return
	}
	if _, err := s.enqueuer.Enqueue(ctx, draft.ID, entry.URL, videoimport.ResolverYtdlp); err != nil {
		// Quota or a disabled resolver etc. — skip this item, keep the sync healthy.
		s.logger.Warn("channel sync import enqueue failed", "sync", row.ID, "video", draft.ID, "error", err)
		return
	}
}

// recordFailure marks a sync failed with a bounded safe message and reschedules
// it for the next cadence (a transient failure self-heals on a later tick).
func (s *Service) recordFailure(ctx context.Context, id uuid.UUID, cause error) {
	msg := cause.Error()
	if len(msg) > maxSyncErrorLen {
		msg = msg[:maxSyncErrorLen]
	}
	_ = s.repo.FailChannelSync(ctx, sqlcgen.FailChannelSyncParams{
		ID:        id,
		LastError: msg,
		NextRunAt: s.nextRun(),
	})
}

// nextRun is the wall-clock time of the next scheduled sync pass.
func (s *Service) nextRun() time.Time { return time.Now().UTC().Add(s.interval) }
