// Package account implements the account lifecycle beyond day-to-day auth:
// the §1 hard delete (product-decisions.md — anonymise the users row, purge
// owned content and per-user rows, tombstone comments), the P4 account export
// (a durable job that writes a JSON archive of the user's data to blob
// storage, downloadable for 7 days), and the P4 import foundation (re-create
// safe subsets of such an archive). It is HTTP-agnostic and testable with a
// fake repository + storage backend.
package account

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no (non-deleted) account/export matches the lookup.
	ErrNotFound = errors.New("account: not found")
	// ErrExportActive means the user already has a pending/running export
	// (single active export per user).
	ErrExportActive = errors.New("account: an export is already in progress")
	// ErrExportNotReady means a download was requested before the export
	// finished (or after it failed).
	ErrExportNotReady = errors.New("account: export not ready")
	// ErrExportExpired means the archive passed its expiry and is no longer
	// downloadable (the sweeper may not have collected the row yet).
	ErrExportExpired = errors.New("account: export expired")
	// ErrInvalidArchive means an import payload is not a recognisable Vidra
	// account archive.
	ErrInvalidArchive = errors.New("account: invalid archive")
)

// Repository is the data access the account service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	// Identity + anonymisation.
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	AnonymizeDeletedUser(ctx context.Context, arg sqlcgen.AnonymizeDeletedUserParams) (int64, error)
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error

	// Owned content (channels → videos) + their recorded blob keys.
	ListChannelsByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error)
	ListVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.ListVideosByChannelRow, error)
	DeleteChannel(ctx context.Context, id uuid.UUID) error
	ListVideoFiles(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.VideoFile, error)
	ListCaptionsByVideo(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.Caption, error)
	GetUserImage(ctx context.Context, arg sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error)
	GetChannelImage(ctx context.Context, arg sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error)

	// Comment tombstones + per-user row purges (§1).
	TombstoneUserComments(ctx context.Context, userID pgtype.UUID) error
	DeleteVideoRatingsByUser(ctx context.Context, userID uuid.UUID) error
	DeleteSavedVideosByUser(ctx context.Context, userID uuid.UUID) error
	ClearWatchHistory(ctx context.Context, userID uuid.UUID) error
	DeletePlaylistsByOwner(ctx context.Context, ownerID uuid.UUID) error
	DeleteChannelFollowsByFollower(ctx context.Context, followerID uuid.UUID) error
	DeleteRemoteChannelFollowsByUser(ctx context.Context, userID uuid.UUID) error
	DeleteMutedAccountsByUser(ctx context.Context, muterID uuid.UUID) error
	DeleteMutedInstancesByUser(ctx context.Context, muterID uuid.UUID) error
	DeleteUserBlocksByUser(ctx context.Context, blockerID uuid.UUID) error
	DeleteOAuthIdentitiesByUser(ctx context.Context, userID uuid.UUID) error
	DeleteUserMFA(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteRecoveryCodes(ctx context.Context, userID uuid.UUID) error
	DeleteNotificationsByUser(ctx context.Context, userID uuid.UUID) error
	DeleteNotificationPrefsByUser(ctx context.Context, userID uuid.UUID) error
	DeletePasswordResetTokensByUser(ctx context.Context, userID uuid.UUID) error
	DeleteEmailVerificationTokensByUser(ctx context.Context, userID uuid.UUID) error
	DeleteAccountActorKey(ctx context.Context, userID uuid.UUID) error
	DeleteUserImage(ctx context.Context, arg sqlcgen.DeleteUserImageParams) (int64, error)

	// Export job queue (migration 0057).
	CreateAccountExport(ctx context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error)
	GetLatestAccountExportByUser(ctx context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error)
	DeleteInactiveAccountExportsByUser(ctx context.Context, userID uuid.UUID) ([]string, error)
	DeleteAccountExportsByUser(ctx context.Context, userID uuid.UUID) ([]string, error)
	ClaimDueAccountExports(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueAccountExportsRow, error)
	CompleteAccountExport(ctx context.Context, arg sqlcgen.CompleteAccountExportParams) error
	RescheduleAccountExport(ctx context.Context, arg sqlcgen.RescheduleAccountExportParams) error
	FailAccountExport(ctx context.Context, arg sqlcgen.FailAccountExportParams) error
	ListExpiredAccountExports(ctx context.Context, limit int32) ([]sqlcgen.ListExpiredAccountExportsRow, error)
	DeleteAccountExport(ctx context.Context, id uuid.UUID) error

	// Export data reads.
	ListVideosByOwnerForExport(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.ListVideosByOwnerForExportRow, error)
	ListVideoTags(ctx context.Context, videoID uuid.UUID) ([]string, error)
	ListCommentsByUserForExport(ctx context.Context, userID pgtype.UUID) ([]sqlcgen.ListCommentsByUserForExportRow, error)
	ListFollowedChannelsByUser(ctx context.Context, followerID uuid.UUID) ([]sqlcgen.ListFollowedChannelsByUserRow, error)
	ListSavedVideosByUserForExport(ctx context.Context, userID uuid.UUID) ([]sqlcgen.ListSavedVideosByUserForExportRow, error)
	ListWatchHistoryByUserForExport(ctx context.Context, userID uuid.UUID) ([]sqlcgen.ListWatchHistoryByUserForExportRow, error)
	ListPlaylistsByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error)
	ListPlaylistItemVideoIDs(ctx context.Context, playlistID uuid.UUID) ([]uuid.UUID, error)
	ListNotificationPrefs(ctx context.Context, userID uuid.UUID) ([]sqlcgen.ListNotificationPrefsRow, error)

	// Import writes.
	UpdateUserProfile(ctx context.Context, arg sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error)
	CreatePlaylist(ctx context.Context, arg sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error)
	AddPlaylistItem(ctx context.Context, arg sqlcgen.AddPlaylistItemParams) error
	GetVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	GetChannelByHandle(ctx context.Context, lower string) (sqlcgen.Channel, error)
	FollowChannel(ctx context.Context, arg sqlcgen.FollowChannelParams) (int64, error)
	UpsertNotificationPref(ctx context.Context, arg sqlcgen.UpsertNotificationPrefParams) error
}

// VideoDeleter deletes one video as its owner, firing the registered delete
// hooks (federation Delete fan-out). Satisfied by *video.Service; a fake in
// tests.
type VideoDeleter interface {
	Delete(ctx context.Context, ownerID, id uuid.UUID) error
}

// Service holds the account lifecycle logic. blobs may be nil (no media
// cleanup / export storage — export requests then fail at drain time, so wire
// it in production). videos may be nil only in tests.
type Service struct {
	repo   Repository
	blobs  storage.Backend
	videos VideoDeleter
	mirror Mirror

	baseURL string
	now     func() time.Time
	// exportTTLFn, when set, supersedes the static ExportTTL (runtime overlay,
	// user_export_expiration_hours; config-parity W8), resolved per finished
	// export. A returned 0 means archives never expire.
	exportTTLFn func() time.Duration
}

// Mirror is the optional IPFS-mirror unpin hook (fix_plan P19). On a hard account
// delete, each removed media blob (the account's + its channels' avatars/banners,
// and any leftover object) is best-effort pulled off the mirror so a deleted
// user's content does not linger pinned. A video's own derivatives are additionally
// unpinned via the video-service delete hook (idempotent). nil disables it;
// *ipfsmirror.Service satisfies it structurally.
type Mirror interface {
	EnqueueUnpin(ctx context.Context, objectKey string) error
}

// Option customises the Service.
type Option func(*Service)

// WithMirror wires the optional IPFS-mirror unpin hook.
func WithMirror(m Mirror) Option { return func(s *Service) { s.mirror = m } }

// WithBaseURL sets the instance's public origin, used to render each exported
// video's original_download_url. Empty renders relative API paths.
func WithBaseURL(u string) Option {
	return func(s *Service) { s.baseURL = u }
}

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithExportTTLFunc makes the export-archive retention dynamic
// (user_export_expiration_hours, config-parity W8): f is resolved once per
// finished export (at completion time, stamping expires_at) so an admin can
// retune the retention without a restart. A returned 0 means the archive never
// expires (the sweeper then never collects it; it is still replaced by the
// user's next export request). Already-stamped rows keep the expiry they were
// completed with. When unset the static ExportTTL (7 days) applies.
func WithExportTTLFunc(f func() time.Duration) Option {
	return func(s *Service) { s.exportTTLFn = f }
}

// NewService builds the account service.
func NewService(repo Repository, blobs storage.Backend, videos VideoDeleter, opts ...Option) *Service {
	s := &Service{repo: repo, blobs: blobs, videos: videos, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
