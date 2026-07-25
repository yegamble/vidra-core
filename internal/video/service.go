// Package video implements video publishing for vidra-core. This first slice
// covers the metadata lifecycle (create draft, read); files, transcoding, and
// playback land in later slices. It is HTTP-agnostic and testable without a
// server.
package video

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no video matches the lookup.
	ErrNotFound = errors.New("video: not found")
	// ErrForbidden means the caller does not own the video.
	ErrForbidden = errors.New("video: not owner")
	// ErrStorageUnavailable means no blob backend is configured (upload routes
	// are only mounted when one is, so this is a guard, not a normal path).
	ErrStorageUnavailable = errors.New("video: storage backend not configured")
	// ErrUnsupportedMedia means the uploaded file's extension is not an accepted
	// video container. This is a cheap first gate; authoritative validation
	// (FFprobe) comes with the transcode pipeline.
	ErrUnsupportedMedia = errors.New("video: unsupported media type")
	// ErrCaptionNotFound means the video has no caption for the requested language.
	ErrCaptionNotFound = errors.New("video: caption not found")
	// ErrInvalidCaption means the caption language or file is invalid (not WebVTT).
	ErrInvalidCaption = errors.New("video: invalid caption")
	// ErrPublished means the operation only applies to a video that has not yet
	// been published (e.g. scheduling a publish time after publication).
	ErrPublished = errors.New("video: already published")
	// ErrNotQuarantined means a quarantine approve/reject targeted a video that
	// is not in the 'quarantined' state.
	ErrNotQuarantined = errors.New("video: not quarantined")
	// ErrPasswordRequired means an operation needs the video to hold at least one
	// password but it has none — e.g. setting privacy=password on a video with no
	// passwords. The HTTP layer maps it to 400 (CORE-17 / W1.C2).
	ErrPasswordRequired = errors.New("video: a password-protected video needs at least one password")
	// ErrLastPassword means deleting/replacing would leave a privacy=password
	// video with zero passwords. The HTTP layer maps it to 409.
	ErrLastPassword = errors.New("video: cannot remove the last password of a password-protected video")
	// ErrPasswordNotFound means the referenced password id does not belong to the
	// video. The HTTP layer maps it to 404.
	ErrPasswordNotFound = errors.New("video: password not found")
	// ErrThumbnailUnavailable means server-side frame extraction is not available
	// (no ffmpeg-backed thumbnailer wired). The HTTP layer maps it to 503 (W2.C5).
	ErrThumbnailUnavailable = errors.New("video: thumbnail frame extraction unavailable")
	// ErrNoProcessedOriginal means a frame-pick was requested before the video has
	// a processed original — no stored original, or no probed duration yet. The
	// HTTP layer maps it to 409 (W2.C5).
	ErrNoProcessedOriginal = errors.New("video: no processed original")
	// ErrThumbnailOutOfRange means the requested frame timestamp is outside the
	// half-open interval [0, duration). The HTTP layer maps it to 422 (W2.C5).
	ErrThumbnailOutOfRange = errors.New("video: thumbnail timestamp out of range")
	// ErrReplaceConflict means a source replacement cannot start right now
	// (config-parity W14): the video is not in the published state (still
	// processing, scheduled, quarantined, failed, or a draft with no source),
	// or another replacement is already in flight. The HTTP layer maps it to
	// 409 replace_conflict.
	ErrReplaceConflict = errors.New("video: replacement conflict")
)

// ReplaceRejectedError means a replacement source was refused before anything
// was swapped (malware scan or media probe, config-parity W14) — the video and
// its current source are untouched. The HTTP layer maps it to 422 with the
// client-safe Reason.
type ReplaceRejectedError struct{ Reason string }

func (e *ReplaceRejectedError) Error() string { return "video: replacement rejected: " + e.Reason }

// PasswordValidationError is a 400-class failure validating a video-password
// write (a plaintext outside 6–100 chars, or a replace-all set outside 1–20
// entries). The HTTP layer maps it to 400.
type PasswordValidationError struct{ Message string }

func (e *PasswordValidationError) Error() string { return e.Message }

// EmbedValidationError is a 400-class failure validating an embed-privacy write
// (an unknown status, a whitelist with an empty/invalid domain list, or
// allowed_domains supplied with a non-whitelist status). The HTTP layer maps it
// to 400.
type EmbedValidationError struct{ Message string }

func (e *EmbedValidationError) Error() string { return e.Message }

// The allow-list of original-upload file extensions, split into the always-on
// base containers and the ADDITIONAL set gated by the runtime
// upload_additional_extensions_enabled setting (config-parity W10, mirroring
// PeerTube's transcoding.allow_additional_extensions split). It is
// deliberately a container/extension gate only — the declared content type is
// client-controlled and not trusted; real content validation is FFprobe's job
// in a later slice.
//
// NOTE: the pre-W10 allowlist was the UNION of both sets, so the setting
// defaults ON — a documented deviation from PeerTube's off default (defaulting
// off would have retroactively rejected containers vidra always accepted).
var baseVideoExts = map[string]bool{
	".mp4": true, ".webm": true, ".ogv": true, ".ogg": true,
}

// additionalVideoExts is the extended container set (PT's "additional
// extensions"), accepted only while upload_additional_extensions_enabled.
var additionalVideoExts = map[string]bool{
	".m4v": true, ".mov": true, ".mkv": true, ".avi": true, ".mpg": true,
	".mpeg": true, ".ts": true, ".flv": true, ".wmv": true, ".3gp": true,
}

// acceptedImageExts maps a custom-thumbnail upload extension to the content type
// served for it. The served Content-Type is derived here (authoritative), not
// from the client-declared type, so a mislabelled upload can't set a bogus type.
var acceptedImageExts = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
}

// Repository is the data access the video service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateVideo(ctx context.Context, arg sqlcgen.CreateVideoParams) (sqlcgen.Video, error)
	GetVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	ListVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.ListVideosByChannelRow, error)
	ListPublicVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.ListPublicVideosByChannelRow, error)
	ListPublicVideosSorted(ctx context.Context, arg sqlcgen.ListPublicVideosSortedParams) ([]sqlcgen.ListPublicVideosSortedRow, error)
	ListSubscriptionVideos(ctx context.Context, arg sqlcgen.ListSubscriptionVideosParams) ([]sqlcgen.ListSubscriptionVideosRow, error)
	ListSavedVideos(ctx context.Context, arg sqlcgen.ListSavedVideosParams) ([]sqlcgen.ListSavedVideosRow, error)
	SaveVideo(ctx context.Context, arg sqlcgen.SaveVideoParams) error
	UnsaveVideo(ctx context.Context, arg sqlcgen.UnsaveVideoParams) error
	SearchPublicVideos(ctx context.Context, arg sqlcgen.SearchPublicVideosParams) ([]sqlcgen.SearchPublicVideosRow, error)
	ListPublicVideosByIDs(ctx context.Context, arg sqlcgen.ListPublicVideosByIDsParams) ([]sqlcgen.ListPublicVideosByIDsRow, error)
	ListRelatedVideosFallback(ctx context.Context, arg sqlcgen.ListRelatedVideosFallbackParams) ([]sqlcgen.ListRelatedVideosFallbackRow, error)
	ListAdminVideos(ctx context.Context, arg sqlcgen.ListAdminVideosParams) ([]sqlcgen.ListAdminVideosRow, error)
	UpdateVideo(ctx context.Context, arg sqlcgen.UpdateVideoParams) (sqlcgen.Video, error)
	DeleteVideo(ctx context.Context, id uuid.UUID) error
	CreateVideoFile(ctx context.Context, arg sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error)
	GetVideoFileByKind(ctx context.Context, arg sqlcgen.GetVideoFileByKindParams) (sqlcgen.VideoFile, error)
	DeleteVideoFilesByVideoAndKind(ctx context.Context, arg sqlcgen.DeleteVideoFilesByVideoAndKindParams) error
	UpsertCaption(ctx context.Context, arg sqlcgen.UpsertCaptionParams) (sqlcgen.Caption, error)
	ListCaptionsByVideo(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.Caption, error)
	GetCaptionByLang(ctx context.Context, arg sqlcgen.GetCaptionByLangParams) (sqlcgen.Caption, error)
	DeleteCaption(ctx context.Context, arg sqlcgen.DeleteCaptionParams) (int64, error)
	SetVideoState(ctx context.Context, arg sqlcgen.SetVideoStateParams) (sqlcgen.Video, error)
	UploadRequiresQuarantine(ctx context.Context, id uuid.UUID) (bool, error)
	ListQuarantinedVideos(ctx context.Context, arg sqlcgen.ListQuarantinedVideosParams) ([]sqlcgen.ListQuarantinedVideosRow, error)
	DeleteVideoTags(ctx context.Context, videoID uuid.UUID) error
	InsertVideoTags(ctx context.Context, arg sqlcgen.InsertVideoTagsParams) error
	ListVideoTags(ctx context.Context, videoID uuid.UUID) ([]string, error)
	ListVideoChapters(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.ListVideoChaptersRow, error)
	VideoHasChapters(ctx context.Context, videoID uuid.UUID) (bool, error)
	DeleteVideoChapters(ctx context.Context, videoID uuid.UUID) error
	InsertVideoChapters(ctx context.Context, arg sqlcgen.InsertVideoChaptersParams) error
	ListVideoPasswords(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.ListVideoPasswordsRow, error)
	ListVideoPasswordHashes(ctx context.Context, videoID uuid.UUID) ([]string, error)
	CountVideoPasswords(ctx context.Context, videoID uuid.UUID) (int64, error)
	InsertVideoPassword(ctx context.Context, arg sqlcgen.InsertVideoPasswordParams) (sqlcgen.InsertVideoPasswordRow, error)
	VideoPasswordExists(ctx context.Context, arg sqlcgen.VideoPasswordExistsParams) (bool, error)
	DeleteVideoPassword(ctx context.Context, arg sqlcgen.DeleteVideoPasswordParams) (int64, error)
	DeleteVideoPasswords(ctx context.Context, videoID uuid.UUID) error
	InsertVideoPasswords(ctx context.Context, arg sqlcgen.InsertVideoPasswordsParams) error
	GetVideoEmbedPrivacy(ctx context.Context, videoID uuid.UUID) (sqlcgen.GetVideoEmbedPrivacyRow, error)
	SetVideoEmbedPrivacy(ctx context.Context, arg sqlcgen.SetVideoEmbedPrivacyParams) error
	ListDueScheduledVideos(ctx context.Context, limit int32) ([]sqlcgen.ListDueScheduledVideosRow, error)
	ListStuckTranscodingVideos(ctx context.Context, arg sqlcgen.ListStuckTranscodingVideosParams) ([]uuid.UUID, error)
	PublishTranscodingVideo(ctx context.Context, id uuid.UUID) (int64, error)
	UpsertVideoMetadata(ctx context.Context, arg sqlcgen.UpsertVideoMetadataParams) (sqlcgen.VideoMetadatum, error)
	GetVideoMetadata(ctx context.Context, videoID uuid.UUID) (sqlcgen.VideoMetadatum, error)
	IncrementVideoViews(ctx context.Context, videoID uuid.UUID) (int64, error)
	GetVideoViews(ctx context.Context, videoID uuid.UUID) (int64, error)
	IncrementVideoViewDay(ctx context.Context, videoID uuid.UUID) error
	ListVideoViewDays(ctx context.Context, arg sqlcgen.ListVideoViewDaysParams) ([]sqlcgen.ListVideoViewDaysRow, error)
	ListChannelViewDays(ctx context.Context, arg sqlcgen.ListChannelViewDaysParams) ([]sqlcgen.ListChannelViewDaysRow, error)
	GetVideoEngagementTotals(ctx context.Context, videoID uuid.UUID) (sqlcgen.GetVideoEngagementTotalsRow, error)
	GetChannelEngagementTotals(ctx context.Context, channelID uuid.UUID) (sqlcgen.GetChannelEngagementTotalsRow, error)
	ListOwnerViewDays(ctx context.Context, arg sqlcgen.ListOwnerViewDaysParams) ([]sqlcgen.ListOwnerViewDaysRow, error)
	GetOwnerChannelStats(ctx context.Context, arg sqlcgen.GetOwnerChannelStatsParams) ([]sqlcgen.GetOwnerChannelStatsRow, error)
	UpsertWatchProgress(ctx context.Context, arg sqlcgen.UpsertWatchProgressParams) (sqlcgen.WatchHistory, error)
	GetWatchProgress(ctx context.Context, arg sqlcgen.GetWatchProgressParams) (sqlcgen.WatchHistory, error)
	ListWatchHistory(ctx context.Context, arg sqlcgen.ListWatchHistoryParams) ([]sqlcgen.ListWatchHistoryRow, error)
	ListWatchHistoryInProgress(ctx context.Context, arg sqlcgen.ListWatchHistoryInProgressParams) ([]sqlcgen.ListWatchHistoryInProgressRow, error)
	DeleteWatchHistoryEntry(ctx context.Context, arg sqlcgen.DeleteWatchHistoryEntryParams) error
	ClearWatchHistory(ctx context.Context, userID uuid.UUID) error
}

// Prober inspects a stored original file and reports whether it is valid,
// playable media. It is the seam for FFprobe/transcoding: when none is
// configured the original is trusted as-is (the upload already passed the
// extension allow-list) and the video is published directly. The real probe is
// wired once FFmpeg is provisioned in the runtime image.
type Prober interface {
	// Probe validates the object at the given storage key and returns its
	// technical metadata, or a non-nil error when it is not usable media.
	Probe(ctx context.Context, storageKey string) (media.Metadata, error)
}

// Scanner checks stored media for malware before it is published. It is the seam
// for a virus scanner (e.g. ClamAV); when none is configured, uploads are not
// scanned. Fail-closed: a not-clean result OR a scan error keeps the video out of
// the published state.
type Scanner interface {
	// Scan reports whether the object at storageKey is clean. A non-nil error
	// means the scan could not complete (treated as unsafe by the caller).
	Scan(ctx context.Context, storageKey string) (clean bool, err error)
}

// Auditor records security-relevant scan outcomes to the durable audit trail
// (safe ids/outcome only — never file content). *audit.Service satisfies it;
// when none is wired the malware-rejection audit event is simply skipped.
type Auditor interface {
	Record(ctx context.Context, ev audit.Event) error
}

// Thumbnailer produces a poster image (JPEG bytes) for the media at storageKey.
// durationSeconds (0 if unknown) hints which frame to grab. It is the seam for
// FFmpeg thumbnail extraction; when none is configured videos publish without a
// poster.
type Thumbnailer interface {
	Thumbnail(ctx context.Context, storageKey string, durationSeconds int) ([]byte, error)
	// ThumbnailAt extracts the exact frame at atSeconds (fractional seconds) from
	// the media at storageKey as JPEG bytes. It backs the frame-pick poster path
	// (UPLOAD-04 / W2.C5); the caller bounds atSeconds against the media duration.
	ThumbnailAt(ctx context.Context, storageKey string, atSeconds float64) ([]byte, error)
}

// Storyboarder produces a seek-preview sprite sheet (JPEG bytes) plus its WebVTT
// sprite map (bytes) for the media at storageKey. durationSeconds (0 if unknown)
// hints the layout. It is the seam for FFmpeg storyboard generation; when none
// is configured videos publish without a storyboard.
type Storyboarder interface {
	Storyboard(ctx context.Context, storageKey string, durationSeconds int) (sprite, vtt []byte, err error)
}

// ScanMode is the malware-scan fallback policy (MALWARE_SCAN_MODE): what to do
// when the scanner cannot complete (a dial/protocol/IO error). An INFECTED
// result always fails the publish, in every mode.
type ScanMode string

const (
	// ScanModeFailClosed (default) keeps unscannable media out of published:
	// a scan error is treated exactly like an infection (state 'failed').
	ScanModeFailClosed ScanMode = "fail-closed"
	// ScanModeFailOpen publishes anyway on a scan error (logged loudly). An
	// infected result still fails. For dev / degraded-scanner operation.
	ScanModeFailOpen ScanMode = "fail-open"
	// ScanModeQuarantine parks unscannable media in the 'quarantined' state for
	// moderator review instead of publishing or failing it outright.
	ScanModeQuarantine ScanMode = "quarantine"
)

// ValidScanMode reports whether s is one of the three policies.
func ValidScanMode(s string) bool {
	switch ScanMode(s) {
	case ScanModeFailClosed, ScanModeFailOpen, ScanModeQuarantine:
		return true
	}
	return false
}

// viewDedupeWindow is how long a single viewer's repeated views of a video are
// collapsed into one counted view.
const viewDedupeWindow = time.Hour

// ViewDeduper collapses repeated views from the same viewer within a window. It
// is the abuse-protection seam for view counting (Redis-backed in production);
// when none is configured every recorded view counts.
type ViewDeduper interface {
	// First reports whether key is seen for the first time within window (i.e.
	// the view should be counted).
	First(ctx context.Context, key string, window time.Duration) (bool, error)
}

// Service holds the video application logic.
type Service struct {
	repo                 Repository
	blobs                storage.Backend
	prober               Prober
	thumbnailer          Thumbnailer
	storyboarder         Storyboarder
	storyboardGate       func() bool
	additionalExtGate    func() bool
	scanner              Scanner
	scanMode             ScanMode
	auditor              Auditor
	viewDeduper          ViewDeduper
	quarantineNewUploads func() bool
	onPublish            []func(context.Context, uuid.UUID)
	onTranscode          func(context.Context, uuid.UUID, string)
	onUpdate             []func(context.Context, uuid.UUID)
	onDelete             []func(context.Context, uuid.UUID, uuid.UUID, bool)
	// uploadUsageRecorder appends a rolling daily-upload-quota ledger event
	// (config-parity W7) after AttachOriginal stores an original. Best-effort;
	// nil = no daily accounting.
	uploadUsageRecorder func(ctx context.Context, ownerID uuid.UUID, bytes int64) error
	// publishDefaults provides the instance publish defaults seeded into
	// CreateDraft when the caller leaves a field unset (config-parity W9).
	// nil = the shipped pre-W9 behaviour.
	publishDefaults func() PublishDefaults
	// transcodeWillRun reports whether a publish would actually enqueue a
	// transcode job that runs — the runtime transcoding_enabled gate AND a wired
	// transcoder (ffmpeg present). The publish-after-transcode hold consults it so
	// a video is never parked behind a gate that can't open (transcoding
	// off/unavailable => publish immediately). nil => treated as "will not run".
	transcodeWillRun func() bool
	// hasLiveTranscodeJob reports whether a live (pending/running) transcode job
	// exists for a video. Wired to transcode.Service.HasLiveJob so the
	// publish-after-transcode update edge case can decide whether to hold a
	// just-published, not-yet-public video. nil => treated as "no live job".
	hasLiveTranscodeJob func(ctx context.Context, videoID uuid.UUID) bool
}

// Option customises the Service.
type Option func(*Service)

// WithProber wires a media prober used by Process to validate originals before
// publishing. Without it, Process publishes the original unprobed.
func WithProber(p Prober) Option {
	return func(s *Service) { s.prober = p }
}

// WithThumbnailer wires a poster-image generator used by Process. Without it,
// videos publish without a thumbnail.
func WithThumbnailer(t Thumbnailer) Option {
	return func(s *Service) { s.thumbnailer = t }
}

// WithScanner wires a malware scanner run by Process before publishing. Without
// it, uploads are not scanned.
func WithScanner(sc Scanner) Option {
	return func(s *Service) { s.scanner = sc }
}

// WithAuditor wires the durable audit trail so Process can record a
// content.upload.malware_rejected event whenever the scanner keeps an upload out
// of the published state (infection, or unscannable under a non-publishing
// policy). Without it, that event is skipped (the state transition still holds).
func WithAuditor(a Auditor) Option {
	return func(s *Service) { s.auditor = a }
}

// WithScanMode sets the malware-scan fallback policy honored by Process on a
// scan error (fail-closed default). Invalid values are ignored (fail-closed
// stays in effect); the config layer validates the string.
func WithScanMode(mode string) Option {
	return func(s *Service) {
		if ValidScanMode(mode) {
			s.scanMode = ScanMode(mode)
		}
	}
}

// WithStoryboarder wires a storyboard generator used by Process. Without it,
// videos publish without a seek-preview storyboard.
func WithStoryboarder(sb Storyboarder) Option {
	return func(s *Service) { s.storyboarder = sb }
}

// WithStoryboardGate wires a dynamic predicate consulted at Process time for
// storyboard generation (storyboards_enabled, config-parity W8): when it
// returns false the publish proceeds without producing a sprite sheet, so an
// admin can turn storyboards off without a restart. Previously stored
// storyboards keep serving either way (the gate covers generation only). A nil
// decider (the default) leaves generation on whenever a Storyboarder is wired.
func WithStoryboardGate(decide func() bool) Option {
	return func(s *Service) { s.storyboardGate = decide }
}

// WithAdditionalExtGate wires the runtime upload_additional_extensions_enabled
// predicate (config-parity W10), consulted per AttachOriginal so an admin can
// restrict uploads to the base container set (.mp4/.webm/.ogv/.ogg) without a
// restart. A nil decider (the default) keeps the full pre-W10 allow-list.
// Already-stored originals are untouched — the gate covers new attachments only.
func WithAdditionalExtGate(decide func() bool) Option {
	return func(s *Service) { s.additionalExtGate = decide }
}

// WithViewDeduper wires per-viewer view de-duplication. Without it, every
// recorded view counts.
func WithViewDeduper(d ViewDeduper) Option {
	return func(s *Service) { s.viewDeduper = d }
}

// WithUploadUsageRecorder wires the rolling daily-upload-quota ledger
// (config-parity W7, default_user_daily_quota_bytes): fn is called after
// AttachOriginal stores an original, with the owner and the stored byte size.
// AttachOriginal is the single choke point every original passes through
// (direct upload, chunked upload, URL import, live replay), so the rolling
// window sees them all. Best-effort — a recording failure never fails the
// upload (the daily gate is race-tolerant by spec).
func WithUploadUsageRecorder(fn func(ctx context.Context, ownerID uuid.UUID, bytes int64) error) Option {
	return func(s *Service) { s.uploadUsageRecorder = fn }
}

// WithQuarantineNewUploads turns on the upload quarantine gate
// (QUARANTINE_NEW_UPLOADS, product-decisions.md §11): when enabled, Process
// parks a finished upload by a non-privileged owner (role 'user' without
// bypass_quarantine) in the 'quarantined' state instead of publishing. No
// publish hooks fire until a moderator approves it.
func WithQuarantineNewUploads(enabled bool) Option {
	return func(s *Service) { s.quarantineNewUploads = func() bool { return enabled } }
}

// WithQuarantineGate wires a dynamic predicate consulted at Process time for the
// upload quarantine gate, so the effective value can be overridden at runtime
// via the admin instance-settings overlay (fix_plan P10) rather than being fixed
// at boot. Supersedes WithQuarantineNewUploads when both are set (last option
// wins). A nil decider (or neither option) leaves the gate off.
func WithQuarantineGate(decide func() bool) Option {
	return func(s *Service) { s.quarantineNewUploads = decide }
}

// WithPublishHook registers a callback invoked (best-effort, synchronously) after
// a video transitions to the "published" state in Process — the seam federation
// uses to fan a Create out to remote followers, and ATProto uses to enqueue an
// auto cross-post, without this package depending on either. Multiple hooks may
// be registered (each WithPublishHook appends); they run in registration order.
// Without any, Process does no post-publish work.
func WithPublishHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onPublish = append(s.onPublish, fn) }
}

// WithTranscodeHook registers a callback invoked (best-effort, synchronously)
// after a video transitions to "published" in Process, passing the stored
// original's storage key — the seam the HLS transcoding pipeline uses to
// enqueue a durable transcode job without this package depending on the queue.
// It must never block or fail the publish; enqueue errors are the callback's
// concern (log-and-continue).
func WithTranscodeHook(fn func(ctx context.Context, videoID uuid.UUID, sourceKey string)) Option {
	return func(s *Service) { s.onTranscode = fn }
}

// WithUpdateHook registers a callback invoked (best-effort) after a video's
// metadata is updated — federation propagates an Update to remote followers, the
// IPFS mirror re-evaluates derivative pins. Passed the video id. Multiple hooks
// may be registered (each call appends); they run in registration order.
func WithUpdateHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onUpdate = append(s.onUpdate, fn) }
}

// WithDeleteHook registers a callback invoked (best-effort) after a video is
// deleted — federation propagates a Delete, the IPFS mirror unpins the video's
// media. Passed the video id, its channel id, and whether it was public (i.e. had
// been federated). Multiple hooks may be registered (each call appends).
func WithDeleteHook(fn func(context.Context, uuid.UUID, uuid.UUID, bool)) Option {
	return func(s *Service) { s.onDelete = append(s.onDelete, fn) }
}

// NewService builds the video service. blobs is the media storage backend used
// by uploads; it may be nil when uploads are not wired (e.g. some tests).
func NewService(repo Repository, blobs storage.Backend, opts ...Option) *Service {
	s := &Service{repo: repo, blobs: blobs, scanMode: ScanModeFailClosed}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateInput is validated, normalized video-creation data. Privacy must already
// be one of public/unlisted/private (the HTTP layer validates and defaults it).
type CreateInput struct {
	Title       string
	Description string
	Privacy     string
	// Category, Language, License are optional taxonomy ids (empty = unset). The
	// HTTP layer validates non-empty values against the GET /videos/config maps.
	Category string
	Language string
	License  string
	// Tags is the video's free-form tag set (the HTTP layer validates count and
	// length limits; normalization happens here via NormalizeTags).
	Tags []string
	// PublishAt optionally schedules the publish transition: once the upload is
	// processed the video parks in the 'scheduled' state until this time (the
	// HTTP layer validates it lies in the future). Nil = publish immediately on
	// processing.
	PublishAt *time.Time
	// IsSensitive marks the video as sensitive content (instance-platform-info):
	// excluded from public browse/search when the instance policy is "hide";
	// otherwise presentation-only.
	IsSensitive bool
	// SensitiveReason is the creator's optional content-warning text shown next to
	// a sensitive video (empty = none). Stored regardless of IsSensitive; the
	// frontend pairs the two. Trimmed here; the HTTP layer caps its length.
	SensitiveReason string
	// CommentsPolicy is the per-video comment policy (config-parity W9), one of
	// CommentsPolicyEnabled/CommentsPolicyDisabled; "" seeds the instance's
	// default_comment_policy (via the WithPublishDefaultsFunc seam). The HTTP
	// layer validates non-empty values.
	CommentsPolicy string
	// DownloadEnabled is the per-video download policy (config-parity W9). It
	// LAYERS on the instance-wide downloads_enabled setting: instance off => no
	// downloads regardless; instance on => this flag decides. nil seeds the
	// instance's default_download_enabled.
	DownloadEnabled *bool
	// PublishAfterTranscode opts the video into the publish-after-transcode hold:
	// once processed it parks in 'transcoding' (hidden from every public surface)
	// until its HLS transcode completes, rather than publishing while transcoding
	// (the default false — current behavior). Ignored when transcoding is
	// disabled/unavailable (the video publishes immediately) and when a future
	// publish_at is set (time-gating takes precedence, v1 scope).
	PublishAfterTranscode bool
}

// Per-video comment-policy values (config-parity W9). PeerTube's third tier
// requires_approval is deliberately deferred (no comment-approval queue; see
// the parity ledger §13).
const (
	CommentsPolicyEnabled  = "enabled"
	CommentsPolicyDisabled = "disabled"
)

// IsCommentsPolicy reports whether v is a valid per-video comment policy.
func IsCommentsPolicy(v string) bool {
	return v == CommentsPolicyEnabled || v == CommentsPolicyDisabled
}

// PublishDefaults are the instance-level publish defaults (config-parity W9,
// defaults.publish) seeded into a new video wherever the caller leaves the
// matching CreateInput field unset — the HTTP create handler, channel-sync
// drafts, and live-replay drafts all seed through the same seam.
type PublishDefaults struct {
	Privacy         string // one of public|unlisted|private
	License         string // taxonomy licence id; "" = no default licence
	CommentsPolicy  string // CommentsPolicyEnabled | CommentsPolicyDisabled
	DownloadEnabled bool
}

// shippedPublishDefaults preserve pre-W9 behaviour when no provider is wired:
// omitted privacy meant private, no licence, comments and downloads allowed.
func shippedPublishDefaults() PublishDefaults {
	return PublishDefaults{Privacy: "private", CommentsPolicy: CommentsPolicyEnabled, DownloadEnabled: true}
}

// WithPublishDefaultsFunc wires the runtime provider for the instance publish
// defaults (default_video_privacy / default_video_licence /
// default_comment_policy / default_download_enabled), consulted per CreateDraft
// so admin changes apply without a restart.
func WithPublishDefaultsFunc(fn func() PublishDefaults) Option {
	return func(s *Service) { s.publishDefaults = fn }
}

// WithTranscodeReadinessFunc wires the "will a transcode actually run" predicate
// consulted by the publish-after-transcode hold (transcoder wired AND runtime
// transcoding_enabled). When it returns false the flag is ignored and Process
// publishes immediately — a video is never stranded behind a hold that can never
// be released. nil (the default) disables the hold entirely.
func WithTranscodeReadinessFunc(fn func() bool) Option {
	return func(s *Service) { s.transcodeWillRun = fn }
}

// WithLiveTranscodeChecker wires the live-transcode-job predicate consulted by
// the publish-after-transcode update edge case (a PATCH toggling the flag on an
// already-published, not-yet-public video with a job still running holds it in
// 'transcoding' until that job finishes). nil (the default) leaves the flag a
// pure metadata write with no state change.
func WithLiveTranscodeChecker(fn func(ctx context.Context, videoID uuid.UUID) bool) Option {
	return func(s *Service) { s.hasLiveTranscodeJob = fn }
}

// CreateDraft creates a new draft video under the given channel. Ownership is
// enforced by the caller (the HTTP layer checks channel ownership first).
func (s *Service) CreateDraft(ctx context.Context, channelID uuid.UUID, in CreateInput) (sqlcgen.Video, error) {
	// A brand-new video has no passwords, so it can never be created directly as
	// privacy=password (CORE-17: a password video needs >=1 password). The studio
	// flow creates the draft, adds a password, then flips privacy.
	if in.Privacy == PrivacyPassword {
		return sqlcgen.Video{}, ErrPasswordRequired
	}
	// Seed unset fields from the instance publish defaults (config-parity W9).
	// The seam covers every draft producer — the HTTP handler, channel-sync,
	// and live replays — so admin defaults apply uniformly. The seeded privacy
	// can never be "password" (the enum excludes it: a password video needs a
	// password first).
	def := shippedPublishDefaults()
	if s.publishDefaults != nil {
		def = s.publishDefaults()
	}
	privacy := in.Privacy
	if privacy == "" {
		privacy = def.Privacy
	}
	license := in.License
	if license == "" {
		license = def.License
	}
	commentsPolicy := in.CommentsPolicy
	if commentsPolicy == "" {
		commentsPolicy = def.CommentsPolicy
	}
	if commentsPolicy == "" {
		commentsPolicy = CommentsPolicyEnabled // defensive: a provider returning zero values
	}
	downloadEnabled := def.DownloadEnabled
	if in.DownloadEnabled != nil {
		downloadEnabled = *in.DownloadEnabled
	}
	v, err := s.repo.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID:       channelID,
		Title:           strings.TrimSpace(in.Title),
		Description:     strings.TrimSpace(in.Description),
		Privacy:         privacy,
		Category:        nilIfEmpty(in.Category),
		Language:        nilIfEmpty(in.Language),
		License:         nilIfEmpty(license),
		PublishAt:       timestamptz(in.PublishAt),
		IsSensitive:     in.IsSensitive,
		SensitiveReason: strings.TrimSpace(in.SensitiveReason),
		CommentsPolicy:  commentsPolicy,
		DownloadEnabled: downloadEnabled,

		PublishAfterTranscode: in.PublishAfterTranscode,
	})
	if err != nil {
		return sqlcgen.Video{}, err
	}
	if tags := NormalizeTags(in.Tags); len(tags) > 0 {
		if err := s.repo.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{VideoID: v.ID, Tags: tags}); err != nil {
			return sqlcgen.Video{}, err
		}
	}
	return v, nil
}

// MaxTagsPerVideo and MaxTagLen are the free-form tag limits (product-decisions
// §18): at most 5 tags of at most 50 characters each (after normalization).
const (
	MaxTagsPerVideo = 5
	MaxTagLen       = 50
)

// NormalizeTags lowercases and trims tags, dropping empties and duplicates
// while preserving first-seen order. It is the single normalization used on
// both write paths, so stored tags are always exact-matchable lowercased.
func NormalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// Tags returns a video's tag set (alphabetical; empty when none).
func (s *Service) Tags(ctx context.Context, videoID uuid.UUID) ([]string, error) {
	return s.repo.ListVideoTags(ctx, videoID)
}

// UploadInput is a video's original file as read from the request: the declared
// filename and content type (both untrusted, stored for display only) and the
// byte stream itself.
type UploadInput struct {
	Filename    string
	ContentType string
	Reader      io.Reader
}

// AttachOriginal stores the original file for a video and moves it from draft to
// processing. Only the owner may upload (non-owner → ErrForbidden, unknown id →
// ErrNotFound). It is a full replace: any previously stored original record for
// the video is removed first and the blob is overwritten at a deterministic key,
// so a re-upload leaves exactly one original. Transcoding into renditions is a
// later slice; this only lands the source bytes and flips state.
func (s *Service) AttachOriginal(ctx context.Context, ownerID, videoID uuid.UUID, in UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error) {
	if s.blobs == nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrStorageUnavailable
	}
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrForbidden
	}
	ext, ok := s.acceptedExt(in.Filename)
	if !ok {
		// Fall back to the declared content type — e.g. a URL import from a path
		// with no file extension (`/download?id=…`) served as `Content-Type:
		// video/mp4`. The multipart upload path always has a real filename, so
		// this only matters for import. The mapped extension passes back through
		// the same gate so a content-type synonym can't bypass the runtime
		// additional-extensions setting.
		if ctExt, ctOK := extForContentType(in.ContentType); ctOK {
			ext, ok = AcceptedVideoExtGated("x"+ctExt, s.additionalExtsAllowed())
		}
	}
	if !ok {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrUnsupportedMedia
	}

	key := originalKey(videoID, ext)
	if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
		VideoID: videoID,
		Kind:    "original",
	}); err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	size, err := s.blobs.Put(ctx, key, in.Reader)
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	file, err := s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "original",
		StorageKey:   key,
		ContentType:  strings.TrimSpace(in.ContentType),
		OriginalName: strings.TrimSpace(in.Filename),
		SizeBytes:    size,
	})
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	updated, err := s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "processing"})
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	// Daily-quota ledger (config-parity W7): record the stored bytes against
	// the owner's rolling 24h window. Best-effort by design.
	if s.uploadUsageRecorder != nil {
		_ = s.uploadUsageRecorder(ctx, ownerID, size)
	}
	return updated, file, nil
}

// ReplaceSource stores a NEW source version for an already-published video —
// the video file replacement flow (config-parity W14, PT video_file.update).
//
// SEMANTICS (the in-place-swap source-version model; see also
// media.OriginalVideoKey): the video's identity — id, URLs, metadata, title,
// policies — never changes. The new source lands at a fresh VERSIONED key
// (web-videos/<id>.rN<ext>), is scanned and probed BEFORE anything is swapped
// (a rejected replacement leaves the video fully intact), and only then does
// the kind='original' file row flip to the new key. Playback keeps working
// throughout: the HLS tree players are streaming belongs to the previous
// generation and is only superseded when the re-transcode (enqueued by the
// caller with the returned file's key) promotes the new generation's DB rows;
// mediagc then collects the old generation and the old source blob. The
// progressive-original and download endpoints resolve the file row, so they
// serve the new source from the moment of the swap.
//
// Owner or (canManage) moderator/admin only. The video must be published with
// no replacement already in flight — the HTTP layer additionally guards the
// live-transcode-job and active-replace-session states; here the state gate is
// ErrReplaceConflict. Scan-policy note: an unscannable file is REJECTED under
// both fail-closed and quarantine modes (quarantining a published video would
// unpublish it — not a replacement semantic); fail-open proceeds with a loud
// log, mirroring Process. Thumbnails and captions are deliberately left
// untouched (they may be creator-authored); the storyboard is regenerated
// best-effort since a stale seek-preview would show the previous content.
// Quota accounting (total + rolling daily ledger) is attributed to the video's
// OWNER — storage usage always aggregates against the owning account — even
// when a privileged actor performs the replacement.
func (s *Service) ReplaceSource(ctx context.Context, actorID, videoID uuid.UUID, in UploadInput, canManage bool) (sqlcgen.Video, sqlcgen.VideoFile, error) {
	if s.blobs == nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrStorageUnavailable
	}
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	if v.OwnerID != actorID && !canManage {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrForbidden
	}
	if v.State != "published" {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrReplaceConflict
	}
	ext, ok := s.acceptedExt(in.Filename)
	if !ok {
		if ctExt, ctOK := extForContentType(in.ContentType); ctOK {
			ext, ok = AcceptedVideoExtGated("x"+ctExt, s.additionalExtsAllowed())
		}
	}
	if !ok {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, ErrUnsupportedMedia
	}

	// Next source version: current version + 1, parsed from the current
	// original's key (a published video always has one; a missing row — e.g. a
	// GC'd source — starts the versioned scheme at 1).
	version := 1
	if cur, cerr := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "original"}); cerr == nil {
		version = media.OriginalKeyVersion(cur.StorageKey) + 1
	}
	key := media.OriginalVideoKey(videoID, version, ext)
	size, err := s.blobs.Put(ctx, key, in.Reader)
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	// reject drops the not-yet-referenced new blob and refuses the replacement;
	// the video and its current source stay fully intact.
	reject := func(reason string) (sqlcgen.Video, sqlcgen.VideoFile, error) {
		_ = s.blobs.Delete(ctx, key)
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, &ReplaceRejectedError{Reason: reason}
	}

	// Malware scan first (same fail-closed posture as Process; quarantine mode
	// rejects here — see the method doc).
	if s.scanner != nil {
		clean, serr := s.scanner.Scan(ctx, key)
		switch {
		case serr != nil:
			if s.scanMode == ScanModeFailOpen {
				slog.WarnContext(ctx, "malware scan failed; accepting replacement anyway (MALWARE_SCAN_MODE=fail-open)",
					"video_id", videoID.String(), "error", serr.Error())
			} else {
				s.auditMalwareRejected(ctx, videoID, "scan_error")
				return reject("the file could not be scanned; try again later")
			}
		case !clean:
			s.auditMalwareRejected(ctx, videoID, "infected")
			return reject("the file failed the malware scan")
		}
	}
	// Probe: the replacement must be playable media (when a prober is wired;
	// without one the extension gate is the only validation, as at publish).
	durationHint := 0
	var md media.Metadata
	probed := false
	if s.prober != nil {
		md, err = s.prober.Probe(ctx, key)
		if err != nil {
			return reject("the file is not a playable video")
		}
		durationHint = md.DurationSeconds
		probed = true
	}

	// The swap: from here the new source is the video's original. Downloads and
	// progressive playback follow the file row immediately; HLS keeps serving
	// the previous generation until the re-transcode promotes the new one.
	if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
		VideoID: videoID,
		Kind:    "original",
	}); err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	file, err := s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "original",
		StorageKey:   key,
		ContentType:  strings.TrimSpace(in.ContentType),
		OriginalName: strings.TrimSpace(in.Filename),
		SizeBytes:    size,
	})
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	if probed {
		if _, err := s.repo.UpsertVideoMetadata(ctx, metadataParams(videoID, md)); err != nil {
			return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
		}
	}
	// Daily-quota ledger (W7 convention): replacement bytes count against the
	// OWNER's rolling window like any other stored original. Best-effort.
	if s.uploadUsageRecorder != nil {
		_ = s.uploadUsageRecorder(ctx, v.OwnerID, size)
	}
	// Storyboard regeneration is best-effort and honors the runtime
	// storyboards_enabled gate; the (possibly creator-picked) thumbnail is
	// deliberately kept.
	if s.storyboarder != nil && (s.storyboardGate == nil || s.storyboardGate()) {
		s.generateStoryboard(ctx, videoID, key, durationHint)
	}
	// The video row itself is unchanged apart from updated_at; re-assert the
	// published state to bump it and return the row.
	updated, err := s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "published"})
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err
	}
	// Update hooks: federation fans an Update{Video} out to remote followers
	// (duration/metadata changed), the IPFS mirror re-evaluates its pins.
	for _, hook := range s.onUpdate {
		hook(ctx, videoID)
	}
	return updated, file, nil
}

// auditMalwareRejected records (best-effort) that the malware scanner kept an
// upload out of the published state. outcome is "infected" or "scan_error"; the
// reason carries only the safe video id, the outcome, and the applied policy —
// never the scanned bytes or any file content. A no-op when no auditor is wired.
func (s *Service) auditMalwareRejected(ctx context.Context, videoID uuid.UUID, outcome string) {
	if s.auditor == nil {
		return
	}
	policy := string(s.scanMode)
	if policy == "" {
		policy = string(ScanModeFailClosed)
	}
	_ = s.auditor.Record(ctx, audit.Event{
		Action: observability.ActionUploadMalwareRejected,
		Result: observability.ResultFailure,
		Actor:  audit.ActorSnapshot{Kind: "system"},
		Reason: "video=" + videoID.String() + " outcome=" + outcome + " policy=" + policy,
	})
}

// Process finalises a processing video: it probes the stored original and moves
// the video to published on success or failed on a probe error. When no prober
// is configured the original is trusted (the extension allow-list already
// gated the upload) and the video is published directly. This is the seam the
// transcode pipeline grows into; for now it is the synchronous step the upload
// handler runs after AttachOriginal. originalKey is the stored object's key.
//
// It does not re-check ownership — callers invoke it only after AttachOriginal
// has authorised the upload.
func (s *Service) Process(ctx context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error) {
	state := "published"
	durationHint := 0
	// scanQuarantine records that the scan could not complete and MALWARE_SCAN_MODE
	// is 'quarantine' — the finished upload is parked for moderator review below
	// (after probe/thumbnail so the reviewer has metadata) rather than published.
	scanQuarantine := false
	if s.scanner != nil {
		clean, err := s.scanner.Scan(ctx, originalKey)
		switch {
		case err != nil:
			// The scan could not complete — MALWARE_SCAN_MODE decides.
			switch s.scanMode {
			case ScanModeFailOpen:
				// Publish anyway, but log loudly: the upload is unscanned. Not a
				// rejection (the video publishes), so no rejection audit event.
				slog.WarnContext(ctx, "malware scan failed; publishing anyway (MALWARE_SCAN_MODE=fail-open)",
					"video_id", videoID.String(), "error", err.Error())
			case ScanModeQuarantine:
				scanQuarantine = true
				s.auditMalwareRejected(ctx, videoID, "scan_error")
			default: // fail-closed
				state = "failed"
				s.auditMalwareRejected(ctx, videoID, "scan_error")
			}
		case !clean:
			// Infected media ALWAYS fails, regardless of mode.
			state = "failed"
			s.auditMalwareRejected(ctx, videoID, "infected")
		}
	}
	if state == "published" && s.prober != nil {
		md, err := s.prober.Probe(ctx, originalKey)
		if err != nil {
			state = "failed"
		} else {
			durationHint = md.DurationSeconds
			if _, err := s.repo.UpsertVideoMetadata(ctx, metadataParams(videoID, md)); err != nil {
				return sqlcgen.Video{}, err
			}
		}
	}
	if state == "published" && s.thumbnailer != nil {
		// Thumbnail generation is best-effort: a failure must not block publish.
		s.generateThumbnail(ctx, videoID, originalKey, durationHint)
	}
	if state == "published" && s.storyboarder != nil && (s.storyboardGate == nil || s.storyboardGate()) {
		// Storyboard generation is best-effort: a failure must not block publish.
		// The gate is the runtime storyboards_enabled toggle (config-parity W8);
		// already stored storyboards keep serving when it is off.
		s.generateStoryboard(ctx, videoID, originalKey, durationHint)
	}
	if state == "published" {
		// A scan error under MALWARE_SCAN_MODE=quarantine parks the upload for
		// moderator review (the existing quarantine queue) instead of publishing.
		// No hooks fire — a moderator approve/reject runs the publish transition.
		if scanQuarantine {
			return s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "quarantined"})
		}
		// Quarantine hold (§11): with QUARANTINE_NEW_UPLOADS on, a non-privileged
		// owner's finished upload parks in 'quarantined' before any scheduled hold
		// (moderation trumps scheduling). No hooks fire — approval publishes
		// through the same transition below. Fail-closed: an unreadable gate
		// quarantines rather than silently publishing past moderation.
		if s.quarantineNewUploads != nil && s.quarantineNewUploads() {
			if requires, qerr := s.repo.UploadRequiresQuarantine(ctx, videoID); qerr != nil || requires {
				return s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "quarantined"})
			}
		}
		v, gerr := s.GetByID(ctx, videoID)
		// Scheduled-publish hold (§17): a future publish_at parks the processed
		// video in 'scheduled' instead of publishing. No hooks fire yet — the
		// sweeper (PublishDue) runs them when the video comes due, through the
		// same publish transition below. publish_at takes precedence over the
		// publish-after-transcode flag (v1 scope: no combined time+transcode gate).
		if gerr == nil && v.PublishAt.Valid && v.PublishAt.Time.After(time.Now()) {
			return s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "scheduled"})
		}
		// Publish-after-transcode hold: enqueue the transcode FIRST (publish()
		// normally fires this hook; we are deliberately NOT publishing yet), then
		// park the video in 'transcoding' — hidden from every public surface,
		// which all filter on state='published' — only if a live job verifiably
		// exists. An enqueue failure, or the runtime transcoding gate flipping
		// off between the readiness check and the enqueue, publishes immediately
		// instead: a video must never strand behind a hold that nothing will
		// release. The release paths (completion hook, terminal-failure hook,
		// stuck sweeper) publish it when the transcode finishes.
		if gerr == nil && v.PublishAfterTranscode && s.transcodeWillRun != nil && s.transcodeWillRun() && s.onTranscode != nil {
			s.onTranscode(ctx, videoID, originalKey)
			if s.hasLiveTranscodeJob == nil || s.hasLiveTranscodeJob(ctx, videoID) {
				return s.enterTranscodeHold(ctx, videoID)
			}
			// The enqueue did not stick — fall through to publish(). Its own
			// transcode-enqueue re-fire is an idempotent no-op for a job that DID
			// land, or a retry for one that failed.
		}
		return s.publish(ctx, videoID, originalKey)
	}
	return s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: state})
}

// publish is THE publish transition: it flips the state and fires the
// federation-announce and transcode-enqueue hooks. Both Process (immediate
// publish) and PublishDue (scheduled publish coming due) run through it, so
// scheduled videos get the exact same side effects as direct ones. (Releasing a
// publish-after-transcode hold takes the separate CAS path in
// ReleaseTranscodeHold: same publish hooks, no transcode re-enqueue.)
func (s *Service) publish(ctx context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error) {
	v, err := s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "published"})
	if err == nil && v.State == "published" {
		for _, hook := range s.onPublish {
			hook(ctx, videoID)
		}
		// Best-effort HLS transcode enqueue: only after a successful publish (a
		// failed scan/probe never reaches here) and never able to block it.
		if s.onTranscode != nil {
			s.onTranscode(ctx, videoID, originalKey)
		}
	}
	return v, err
}

// enterTranscodeHold parks a video in the 'transcoding' publish-after-transcode
// hold, then immediately re-checks for a live job: the transcode may have raced
// to completion between the caller's live-job check and the hold write — its
// completion hook saw a non-held state and no-op'd — so without this re-check
// the video would sit stranded until the stuck sweeper. When no job remains the
// hold is released on the spot (the CAS in ReleaseTranscodeHold keeps a
// concurrent release from double-firing hooks).
func (s *Service) enterTranscodeHold(ctx context.Context, videoID uuid.UUID) (sqlcgen.Video, error) {
	held, err := s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "transcoding"})
	if err != nil {
		return held, err
	}
	if s.hasLiveTranscodeJob != nil && !s.hasLiveTranscodeJob(ctx, videoID) {
		if rerr := s.ReleaseTranscodeHold(ctx, videoID); rerr == nil {
			held.State = "published"
		}
	}
	return held, nil
}

// ReleaseTranscodeHold publishes a video held in the 'transcoding' state
// (publish-after-transcode) so the federation/ATProto/search/IPFS publish hooks
// fire at true publish time. The transition is a CAS — state flips to published
// only WHERE it is still 'transcoding', and the hooks fire only when this call
// won that transition — so the concurrent release triggers (completion hook,
// terminal-failure hook, stuck sweeper, PATCH flag-off) publish exactly once.
// A no-op (nil) for a video in any other state, including one already released
// by a racer. The transcode-enqueue hook is deliberately NOT re-fired: the
// transcode already ran while the video was held.
func (s *Service) ReleaseTranscodeHold(ctx context.Context, videoID uuid.UUID) error {
	n, err := s.repo.PublishTranscodingVideo(ctx, videoID)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil // not held, or a concurrent release won the CAS
	}
	for _, hook := range s.onPublish {
		hook(ctx, videoID)
	}
	return nil
}

// ReleaseStuckTranscodeHolds is the safety-net sweeper: it publishes videos that
// have been parked in the 'transcoding' hold longer than olderThan (a crashed
// worker or a lost job), releasing each through ReleaseTranscodeHold. Returns how
// many were released; a per-video failure is skipped so the next sweep retries.
func (s *Service) ReleaseStuckTranscodeHolds(ctx context.Context, olderThan time.Duration, limit int32) (int, error) {
	ids, err := s.repo.ListStuckTranscodingVideos(ctx, sqlcgen.ListStuckTranscodingVideosParams{
		Cutoff:      time.Now().Add(-olderThan),
		ResultLimit: limit,
	})
	if err != nil {
		return 0, err
	}
	released := 0
	for _, id := range ids {
		if err := s.ReleaseTranscodeHold(ctx, id); err == nil {
			released++
		}
	}
	return released, nil
}

// QuarantinedVideo is a queue entry in the moderation quarantine review list:
// the held video with its owning channel and account.
type QuarantinedVideo struct {
	ID                 uuid.UUID
	Title              string
	Privacy            string
	State              string
	ChannelHandle      string
	ChannelDisplayName string
	OwnerUsername      string
	CreatedAt          time.Time
}

// ListQuarantined returns quarantined videos newest first for the moderation
// queue. The caller clamps limit/offset.
func (s *Service) ListQuarantined(ctx context.Context, limit, offset int32) ([]QuarantinedVideo, error) {
	rows, err := s.repo.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]QuarantinedVideo, 0, len(rows))
	for _, r := range rows {
		items = append(items, QuarantinedVideo{
			ID:                 r.ID,
			Title:              r.Title,
			Privacy:            r.Privacy,
			State:              r.State,
			ChannelHandle:      r.ChannelHandle,
			ChannelDisplayName: r.ChannelDisplayName,
			OwnerUsername:      r.OwnerUsername,
			CreatedAt:          r.CreatedAt,
		})
	}
	return items, nil
}

// ApproveQuarantined releases a quarantined video through THE publish
// transition, so the federation-announce and transcode-enqueue hooks fire at
// approval time exactly as they would on a direct publish. Unknown id →
// ErrNotFound; a video in any other state → ErrNotQuarantined (approval is not
// a general-purpose publish button).
func (s *Service) ApproveQuarantined(ctx context.Context, videoID uuid.UUID) (sqlcgen.Video, error) {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.Video{}, err
	}
	if v.State != "quarantined" {
		return sqlcgen.Video{}, ErrNotQuarantined
	}
	// A quarantined video always has a stored original (it finished processing
	// before the hold) — same invariant the scheduled sweeper relies on.
	f, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "original"})
	if err != nil {
		return sqlcgen.Video{}, err
	}
	return s.publish(ctx, videoID, f.StorageKey)
}

// RejectQuarantined fails a quarantined video (it never publishes; no hooks
// fire). The caller records the moderator's reason in the audit trail and
// notifies the owner. Returns the video row (with owner id) so the caller can
// address that notification. Unknown id → ErrNotFound; any other state →
// ErrNotQuarantined.
func (s *Service) RejectQuarantined(ctx context.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	}
	if v.State != "quarantined" {
		return sqlcgen.GetVideoByIDRow{}, ErrNotQuarantined
	}
	if _, err := s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "failed"}); err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	}
	v.State = "failed"
	return v, nil
}

// PublishDue transitions scheduled videos whose publish_at has arrived to
// published, through the same publish transition Process uses (federation
// announce + transcode enqueue hooks fire per video). It returns how many were
// published this pass; a per-video failure is skipped (the row stays
// 'scheduled', so the next sweep retries it) rather than aborting the batch.
func (s *Service) PublishDue(ctx context.Context, limit int32) (int, error) {
	rows, err := s.repo.ListDueScheduledVideos(ctx, limit)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, r := range rows {
		if _, err := s.publish(ctx, r.ID, r.StorageKey); err == nil {
			published++
		}
	}
	return published, nil
}

// FileForView authorises serving a stored file of the given kind ("original",
// "thumbnail", …) for a video and returns it. Visibility mirrors GetByID:
// public/unlisted to anyone, private only to its owner; everyone else — and any
// video without a stored file of that kind — gets ErrNotFound so existence is
// not leaked.
func (s *Service) FileForView(ctx context.Context, videoID, viewerID uuid.UUID, authed bool, kind string) (sqlcgen.VideoFile, error) {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.VideoFile{}, err // ErrNotFound
	}
	if v.Privacy == "private" && (!authed || viewerID != v.OwnerID) {
		return sqlcgen.VideoFile{}, ErrNotFound
	}
	f, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: kind})
	if err != nil {
		return sqlcgen.VideoFile{}, ErrNotFound
	}
	return f, nil
}

// RecordView counts a view of a published video, deduping per viewer within a
// window when a deduper is configured. Visibility mirrors GetByID (private →
// owner only, else ErrNotFound). viewerKey identifies the viewer (already
// hashed by the caller). Non-published videos are a silent no-op (no error) so
// owner previews do not inflate counts. The deduper is best-effort: an error is
// treated as "count it".
func (s *Service) RecordView(ctx context.Context, videoID, viewerID uuid.UUID, authed bool, viewerKey string) error {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return err // ErrNotFound
	}
	if v.Privacy == "private" && (!authed || viewerID != v.OwnerID) {
		return ErrNotFound
	}
	if v.State != "published" {
		return nil
	}
	if s.viewDeduper != nil {
		key := "view:" + videoID.String() + ":" + viewerKey
		if first, derr := s.viewDeduper.First(ctx, key, viewDedupeWindow); derr == nil && !first {
			return nil // already counted this viewer in the window
		}
	}
	if _, err = s.repo.IncrementVideoViews(ctx, videoID); err != nil {
		return err
	}
	// The per-day rollup rides the same deduped write (§8 creator stats).
	return s.repo.IncrementVideoViewDay(ctx, videoID)
}

// Views returns a video's current view count (0 when none recorded).
func (s *Service) Views(ctx context.Context, videoID uuid.UUID) int64 {
	n, err := s.repo.GetVideoViews(ctx, videoID)
	if err != nil {
		return 0
	}
	return n
}

// HasThumbnail reports whether a poster image has been stored for the video.
func (s *Service) HasThumbnail(ctx context.Context, videoID uuid.UUID) bool {
	_, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "thumbnail"})
	return err == nil
}

// thumbnailReadCap bounds ReadThumbnail's in-memory read so a caller never loads
// an unexpectedly large blob. It is generous (5 MiB) so downstream callers (e.g.
// the ATProto embed's own ≤1 MiB rule) remain the authority on what to attach.
const thumbnailReadCap = 5 << 20

// ReadThumbnail returns a video's stored poster image bytes and content type, or
// ok=false when none exists (a missing thumbnail is not an error). It satisfies
// the atproto.Thumbnails seam without that package depending on the storage-key
// layout. The read is bounded at thumbnailReadCap.
func (s *Service) ReadThumbnail(ctx context.Context, videoID uuid.UUID) ([]byte, string, bool, error) {
	if s.blobs == nil {
		return nil, "", false, nil
	}
	f, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "thumbnail"})
	if err != nil {
		return nil, "", false, nil
	}
	rc, err := s.blobs.Open(ctx, f.StorageKey)
	if err != nil {
		return nil, "", false, nil
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, thumbnailReadCap))
	if err != nil {
		return nil, "", false, err
	}
	ct := f.ContentType
	if ct == "" {
		ct = "image/jpeg"
	}
	return data, ct, true, nil
}

// generateThumbnail extracts a poster for the video and stores it as a
// kind="thumbnail" file, replacing any previous one. Best-effort: any failure
// is swallowed so it never blocks publishing.
func (s *Service) generateThumbnail(ctx context.Context, videoID uuid.UUID, originalKey string, durationHint int) {
	if s.blobs == nil {
		return
	}
	jpg, err := s.thumbnailer.Thumbnail(ctx, originalKey, durationHint)
	if err != nil || len(jpg) == 0 {
		return
	}
	key := thumbnailKey(videoID)
	if _, err := s.blobs.Put(ctx, key, bytes.NewReader(jpg)); err != nil {
		return
	}
	_ = s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{VideoID: videoID, Kind: "thumbnail"})
	_, _ = s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "thumbnail",
		StorageKey:   key,
		ContentType:  "image/jpeg",
		OriginalName: "thumbnail.jpg",
		SizeBytes:    int64(len(jpg)),
	})
}

// HasStoryboard reports whether a storyboard sprite sheet has been stored for
// the video (a kind='storyboard' video_file).
func (s *Service) HasStoryboard(ctx context.Context, videoID uuid.UUID) bool {
	_, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "storyboard"})
	return err == nil
}

// generateStoryboard renders the seek-preview sprite sheet + WebVTT map for the
// video and stores them as kind='storyboard'/'storyboard_vtt' files, replacing
// any previous pair. Best-effort: any failure is swallowed so it never blocks
// publishing.
func (s *Service) generateStoryboard(ctx context.Context, videoID uuid.UUID, originalKey string, durationHint int) {
	if s.blobs == nil {
		return
	}
	sprite, vtt, err := s.storyboarder.Storyboard(ctx, originalKey, durationHint)
	if err != nil || len(sprite) == 0 || len(vtt) == 0 {
		return
	}
	jpgKey := media.StoryboardKeyJPG(videoID)
	vttKey := media.StoryboardKeyVTT(videoID)
	if _, err := s.blobs.Put(ctx, jpgKey, bytes.NewReader(sprite)); err != nil {
		return
	}
	if _, err := s.blobs.Put(ctx, vttKey, bytes.NewReader(vtt)); err != nil {
		return
	}
	_ = s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{VideoID: videoID, Kind: "storyboard"})
	_ = s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{VideoID: videoID, Kind: "storyboard_vtt"})
	_, _ = s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "storyboard",
		StorageKey:   jpgKey,
		ContentType:  "image/jpeg",
		OriginalName: "storyboard.jpg",
		SizeBytes:    int64(len(sprite)),
	})
	_, _ = s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "storyboard_vtt",
		StorageKey:   vttKey,
		ContentType:  "text/vtt",
		OriginalName: "storyboard.vtt",
		SizeBytes:    int64(len(vtt)),
	})
}

// acceptedImageExt returns the served content type for filename when it is an
// accepted thumbnail image, and ok=false otherwise. It is the thumbnail-upload
// type gate (mirrors acceptedExt for originals).
func acceptedImageExt(filename string) (contentType string, ok bool) {
	ct, ok := acceptedImageExts[strings.ToLower(filepath.Ext(filename))]
	return ct, ok
}

// SetThumbnail stores a creator-supplied poster image for a video, replacing any
// previous (uploaded or auto-generated) thumbnail. Owner-only (non-owner →
// ErrForbidden, unknown id → ErrNotFound); a non-image extension → ErrUnsupportedMedia.
// It does not change the video's state — a thumbnail can be set at any point. The
// blob is stored at the deterministic thumbnail key, so exactly one poster exists
// and the GET /thumbnail endpoint serves it with the content type derived here.
func (s *Service) SetThumbnail(ctx context.Context, ownerID, videoID uuid.UUID, in UploadInput) (sqlcgen.VideoFile, error) {
	if s.blobs == nil {
		return sqlcgen.VideoFile{}, ErrStorageUnavailable
	}
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.VideoFile{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.VideoFile{}, ErrForbidden
	}
	contentType, ok := acceptedImageExt(in.Filename)
	if !ok {
		return sqlcgen.VideoFile{}, ErrUnsupportedMedia
	}

	key := thumbnailKey(videoID)
	if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
		VideoID: videoID,
		Kind:    "thumbnail",
	}); err != nil {
		return sqlcgen.VideoFile{}, err
	}
	size, err := s.blobs.Put(ctx, key, in.Reader)
	if err != nil {
		return sqlcgen.VideoFile{}, err
	}
	return s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "thumbnail",
		StorageKey:   key,
		ContentType:  contentType,
		OriginalName: strings.TrimSpace(in.Filename),
		SizeBytes:    size,
	})
}

// SetThumbnailFromFrame extracts the exact frame at atSeconds from the video's
// processed original and stores it as the poster, replacing any previous (uploaded
// or auto-generated) thumbnail (UPLOAD-04 / W2.C5). Owner-only (non-owner →
// ErrForbidden, unknown id → ErrNotFound). It requires a processed original — a
// stored original AND a probed positive duration — else ErrNoProcessedOriginal
// (409). atSeconds must lie in the half-open interval [0, duration) else
// ErrThumbnailOutOfRange (422). Frame extraction needs an ffmpeg-backed
// thumbnailer; without one it returns ErrThumbnailUnavailable (503). The frame is
// stored at the deterministic thumbnail key so exactly one poster exists and GET
// /thumbnail serves it. Unlike SetThumbnail, it does not change the video's state.
func (s *Service) SetThumbnailFromFrame(ctx context.Context, ownerID, videoID uuid.UUID, atSeconds float64) (sqlcgen.VideoFile, error) {
	if s.blobs == nil {
		return sqlcgen.VideoFile{}, ErrStorageUnavailable
	}
	if s.thumbnailer == nil {
		return sqlcgen.VideoFile{}, ErrThumbnailUnavailable
	}
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.VideoFile{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.VideoFile{}, ErrForbidden
	}
	// A frame-pick needs a processed original: the stored source to extract from
	// and a probed duration to bound the timestamp. Either missing → 409.
	orig, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "original"})
	if err != nil {
		return sqlcgen.VideoFile{}, ErrNoProcessedOriginal
	}
	md, ok, err := s.GetMetadata(ctx, videoID)
	if err != nil {
		return sqlcgen.VideoFile{}, err
	}
	if !ok || md.DurationSeconds == nil || *md.DurationSeconds <= 0 {
		return sqlcgen.VideoFile{}, ErrNoProcessedOriginal
	}
	if atSeconds < 0 || atSeconds >= float64(*md.DurationSeconds) {
		return sqlcgen.VideoFile{}, ErrThumbnailOutOfRange
	}

	jpg, err := s.thumbnailer.ThumbnailAt(ctx, orig.StorageKey, atSeconds)
	if err != nil {
		return sqlcgen.VideoFile{}, err
	}
	if len(jpg) == 0 {
		return sqlcgen.VideoFile{}, errors.New("video: frame extraction produced no image")
	}
	key := thumbnailKey(videoID)
	if err := s.repo.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
		VideoID: videoID,
		Kind:    "thumbnail",
	}); err != nil {
		return sqlcgen.VideoFile{}, err
	}
	if _, err := s.blobs.Put(ctx, key, bytes.NewReader(jpg)); err != nil {
		return sqlcgen.VideoFile{}, err
	}
	return s.repo.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID:      videoID,
		Kind:         "thumbnail",
		StorageKey:   key,
		ContentType:  "image/jpeg",
		OriginalName: "frame.jpg",
		SizeBytes:    int64(len(jpg)),
	})
}

// thumbnailKey is the deterministic storage key for a video's poster image.
// PeerTube-aligned layout: one top-level dir per asset kind (see
// .ralph/specs/storage-layout.md), so thumbnails live under thumbnails/.
func thumbnailKey(videoID uuid.UUID) string {
	return "thumbnails/" + videoID.String() + ".jpg"
}

// GetMetadata returns a video's stored technical metadata. The bool is false
// when none has been recorded (e.g. published without a prober, or not yet
// processed); a lookup miss is reported as absent, not an error.
func (s *Service) GetMetadata(ctx context.Context, videoID uuid.UUID) (sqlcgen.VideoMetadatum, bool, error) {
	m, err := s.repo.GetVideoMetadata(ctx, videoID)
	if err != nil {
		return sqlcgen.VideoMetadatum{}, false, nil
	}
	return m, true, nil
}

// metadataParams maps probe Metadata to upsert params, leaving unknown (zero)
// measures NULL so the API can distinguish "not determined" from a real value.
func metadataParams(videoID uuid.UUID, md media.Metadata) sqlcgen.UpsertVideoMetadataParams {
	return sqlcgen.UpsertVideoMetadataParams{
		VideoID:         videoID,
		DurationSeconds: posInt32(md.DurationSeconds),
		Width:           posInt32(md.Width),
		Height:          posInt32(md.Height),
	}
}

// posInt32 returns a pointer to n as int32 when n is positive, else nil (NULL).
func posInt32(n int) *int32 {
	if n <= 0 {
		return nil
	}
	v := int32(n)
	return &v
}

// AcceptedVideoExt reports the normalized (lowercased) extension of filename
// when it is an accepted video container (base OR additional set), else
// ("", false). This is the permissive/full-universe form — the pre-W10
// allowlist — used where only container RECOGNITION matters (e.g. the import
// auto-resolver's routing); enforcement sites that honor the runtime
// upload_additional_extensions_enabled gate use AcceptedVideoExtGated.
func AcceptedVideoExt(filename string) (string, bool) {
	return AcceptedVideoExtGated(filename, true)
}

// AcceptedVideoExtGated is the gate-aware upload type check (config-parity
// W10): with additional=false only the base container set is accepted; with
// additional=true the full (pre-W10) universe is. Exposed so the HTTP layer
// validates a declared filename up front against the exact same allow-list
// AttachOriginal enforces.
func AcceptedVideoExtGated(filename string, additional bool) (string, bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	if baseVideoExts[ext] || (additional && additionalVideoExts[ext]) {
		return ext, true
	}
	return "", false
}

// acceptedExt is the service-level upload type gate: the base set plus, while
// the wired additional-extensions gate allows it (nil = allowed, the pre-W10
// behavior), the additional set.
func (s *Service) acceptedExt(filename string) (string, bool) {
	return AcceptedVideoExtGated(filename, s.additionalExtsAllowed())
}

// additionalExtsAllowed resolves the runtime additional-extensions gate
// (upload_additional_extensions_enabled); nil = allowed.
func (s *Service) additionalExtsAllowed() bool {
	return s.additionalExtGate == nil || s.additionalExtGate()
}

// videoContentTypeExts maps common video-container MIME types to a canonical
// extension (always one the base/additional allow-list covers). Used as the fallback type
// gate for URL imports whose path carries no usable extension.
var videoContentTypeExts = map[string]string{
	"video/mp4":        ".mp4",
	"video/x-m4v":      ".m4v",
	"video/quicktime":  ".mov",
	"video/webm":       ".webm",
	"video/x-matroska": ".mkv",
	"video/x-msvideo":  ".avi",
	"video/ogg":        ".ogv",
	"application/ogg":  ".ogv",
	"video/mpeg":       ".mpeg",
	"video/mp2t":       ".ts",
	"video/x-flv":      ".flv",
	"video/x-ms-wmv":   ".wmv",
	"video/3gpp":       ".3gp",
}

// AcceptedVideoContentType reports the canonical extension for a video-container
// media type (exact media type, no parameters), and false otherwise. It lets
// other packages (e.g. the import auto-resolver) reuse the SAME container
// allow-list the upload gate applies, without duplicating the map.
func AcceptedVideoContentType(mediaType string) (string, bool) {
	ext, ok := videoContentTypeExts[strings.ToLower(strings.TrimSpace(mediaType))]
	return ext, ok
}

// extForContentType returns a canonical extension for a video-container content
// type (media type only; any ";charset=…" parameters and case are ignored), and
// false when the type is not a recognised video container.
func extForContentType(contentType string) (string, bool) {
	mediaType := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	ext, ok := videoContentTypeExts[mediaType]
	return ext, ok
}

// originalKey builds the storage key for the video file served for web playback.
// PeerTube-aligned layout: it lives under web-videos/ (one top-level dir per asset
// kind — see .ralph/specs/storage-layout.md), named by video id. With no transcode
// pipeline yet this is the unmodified upload; when transcoding lands the archived
// source moves to original-video-files/ and renditions stay here.
func originalKey(videoID uuid.UUID, ext string) string {
	return "web-videos/" + videoID.String() + ext
}

// GetByID returns a video joined with its owning account's id (for the caller's
// privacy/authorization decision). Miss → ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.repo.GetVideoByID(ctx, id)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, ErrNotFound
	}
	return v, nil
}

// OriginalFileKey returns the storage key of a video's stored original file, or
// ErrNotFound when it has none (never uploaded / already GC'd). The auto-caption
// worker uses it to locate the source media it extracts audio from.
func (s *Service) OriginalFileKey(ctx context.Context, videoID uuid.UUID) (string, error) {
	f, err := s.repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "original"})
	if err != nil {
		return "", ErrNotFound
	}
	return f.StorageKey, nil
}

// UpdateInput is a partial video update: nil fields are left unchanged. Privacy,
// when set, is already validated by the HTTP layer.
type UpdateInput struct {
	Title       *string
	Description *string
	Privacy     *string
	// Category, Language, License: nil leaves the field unchanged; a non-nil value
	// (validated by the HTTP layer) sets it. Clearing back to unset is not yet
	// supported (the COALESCE update cannot distinguish keep from clear).
	Category *string
	Language *string
	License  *string
	// Tags: nil leaves the tag set unchanged; a non-nil slice replaces it in
	// full (an empty slice clears all tags).
	Tags *[]string
	// PublishAt: nil leaves the schedule unchanged; a non-nil (future — the
	// HTTP layer validates) time sets it. Only accepted while the video is not
	// yet published (ErrPublished otherwise).
	PublishAt *time.Time
	// IsSensitive: nil leaves the sensitive-content flag unchanged; a non-nil
	// value sets it.
	IsSensitive *bool
	// SensitiveReason: nil leaves the content-warning text unchanged; a non-nil
	// value sets it (an empty string clears it). Trimmed here; the HTTP layer
	// caps its length.
	SensitiveReason *string
	// CommentsPolicy: nil leaves the per-video comment policy unchanged; a
	// non-nil value (validated by the HTTP layer) sets it (config-parity W9).
	CommentsPolicy *string
	// DownloadEnabled: nil leaves the per-video download policy unchanged; a
	// non-nil value sets it.
	DownloadEnabled *bool
	// PublishAfterTranscode: nil leaves the flag unchanged; a non-nil value sets
	// it. Setting it true on an already-published, not-yet-public video with a
	// live transcode job holds the video in 'transcoding' until that job finishes
	// (see UpdateForActor). It is otherwise a plain metadata write.
	PublishAfterTranscode *bool
}

// Update changes a video's mutable metadata. It preserves the owner-only
// service contract used by creator flows; a non-owner gets ErrForbidden and an
// unknown id gets ErrNotFound.
func (s *Service) Update(ctx context.Context, ownerID, id uuid.UUID, in UpdateInput) (sqlcgen.Video, error) {
	return s.UpdateForActor(ctx, ownerID, id, in, false)
}

// UpdateForActor changes a video's mutable metadata for its owner or, when
// canManage is true, a moderator/admin acting through the HTTP policy layer.
// actorID remains distinct from the video's owner so callers can preserve the
// real actor in audit/event context. Unknown ids still return ErrNotFound.
func (s *Service) UpdateForActor(ctx context.Context, actorID, id uuid.UUID, in UpdateInput, canManage bool) (sqlcgen.Video, error) {
	v, err := s.GetByID(ctx, id)
	if err != nil {
		return sqlcgen.Video{}, err
	}
	if v.OwnerID != actorID && !canManage {
		return sqlcgen.Video{}, ErrForbidden
	}
	if in.PublishAt != nil && v.State == "published" {
		// A publish time only makes sense before publication (§17).
		return sqlcgen.Video{}, ErrPublished
	}
	// Switching to privacy=password requires the video to already hold >=1
	// password (CORE-17). Checked BEFORE the write so a rejected switch leaves the
	// privacy untouched.
	if in.Privacy != nil && *in.Privacy == PrivacyPassword {
		has, hErr := s.HasPassword(ctx, id)
		if hErr != nil {
			return sqlcgen.Video{}, hErr
		}
		if !has {
			return sqlcgen.Video{}, ErrPasswordRequired
		}
	}
	updated, err := s.repo.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID:              id,
		Title:           trimPtr(in.Title),
		Description:     trimPtr(in.Description),
		Privacy:         in.Privacy,
		Category:        in.Category,
		Language:        in.Language,
		License:         in.License,
		PublishAt:       timestamptz(in.PublishAt),
		IsSensitive:     in.IsSensitive,
		SensitiveReason: trimPtr(in.SensitiveReason),
		CommentsPolicy:  in.CommentsPolicy,
		DownloadEnabled: in.DownloadEnabled,

		PublishAfterTranscode: in.PublishAfterTranscode,
	})
	if err != nil {
		return sqlcgen.Video{}, err
	}
	if in.Tags != nil {
		// Full replace: clear then insert the normalized set.
		if err := s.repo.DeleteVideoTags(ctx, id); err != nil {
			return sqlcgen.Video{}, err
		}
		if tags := NormalizeTags(*in.Tags); len(tags) > 0 {
			if err := s.repo.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{VideoID: id, Tags: tags}); err != nil {
				return sqlcgen.Video{}, err
			}
		}
	}
	// Publish-after-transcode toggle edge cases. v is the pre-update row, so its
	// State/Privacy are the values "at the start" of this update.
	//
	// ON: for a video that is ALREADY published but has never been publicly
	// visible (privacy was not 'public' at the start) with a live transcode job
	// AND transcoding actually running (a zombie pending job while the runtime
	// gate is off must not hold a video nothing will release), park it in
	// 'transcoding' until that job completes — so the toggle works even when a
	// fast upload published (auto-created private) before the user hit Publish.
	// A publicly-visible video is never yanked back to hidden, and a video with
	// no live job just stores the flag (state unchanged). enterTranscodeHold
	// re-checks the live job after the hold write: the last job may complete in
	// the window between the check and the write (its completion hook saw
	// 'published' and no-op'd), in which case the hold is released immediately
	// rather than stranding until the sweeper.
	//
	// OFF: for a currently HELD video, honor the intent and release the hold now
	// (the CAS publishes exactly once even against a racing completion hook).
	if in.PublishAfterTranscode != nil {
		switch {
		case *in.PublishAfterTranscode &&
			v.State == "published" && v.Privacy != "public" &&
			s.transcodeWillRun != nil && s.transcodeWillRun() &&
			s.hasLiveTranscodeJob != nil && s.hasLiveTranscodeJob(ctx, id):
			held, herr := s.enterTranscodeHold(ctx, id)
			if herr != nil {
				return sqlcgen.Video{}, herr
			}
			updated = held
		case !*in.PublishAfterTranscode && v.State == "transcoding":
			if rerr := s.ReleaseTranscodeHold(ctx, id); rerr != nil {
				return sqlcgen.Video{}, rerr
			}
			updated.State = "published"
		}
	}
	for _, hook := range s.onUpdate {
		hook(ctx, id)
	}
	return updated, nil
}

// PrefillMetadata fills a video's title/description from an importer's metadata
// probe (e.g. yt-dlp), but ONLY where the current value is empty — it never
// overwrites text the creator already entered. Blank inputs are ignored. Unknown
// id → ErrNotFound. It does NOT re-check ownership: the caller (the URL-import
// worker) has already authorised the video via AttachOriginal, and this only
// touches the same draft's own metadata.
func (s *Service) PrefillMetadata(ctx context.Context, videoID uuid.UUID, title, description string) error {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return err
	}
	var titlePtr, descPtr *string
	if strings.TrimSpace(v.Title) == "" {
		if t := strings.TrimSpace(title); t != "" {
			titlePtr = &t
		}
	}
	if strings.TrimSpace(v.Description) == "" {
		if d := strings.TrimSpace(description); d != "" {
			descPtr = &d
		}
	}
	if titlePtr == nil && descPtr == nil {
		return nil // nothing to fill — the creator already set both
	}
	_, err = s.repo.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID:          videoID,
		Title:       titlePtr,
		Description: descPtr,
	})
	return err
}

// Delete removes a video under the owner-only service contract.
func (s *Service) Delete(ctx context.Context, ownerID, id uuid.UUID) error {
	return s.DeleteForActor(ctx, ownerID, id, false)
}

// DeleteForActor removes a video for its owner or, when canManage is true, a
// moderator/admin acting through the HTTP policy layer. actorID is used only
// for authorization; the handler retains it as the audit actor.
func (s *Service) DeleteForActor(ctx context.Context, actorID, id uuid.UUID, canManage bool) error {
	v, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if v.OwnerID != actorID && !canManage {
		return ErrForbidden
	}
	if err := s.repo.DeleteVideo(ctx, id); err != nil {
		return err
	}
	for _, hook := range s.onDelete {
		hook(ctx, id, v.ChannelID, v.Privacy == "public")
	}
	return nil
}

// ListByChannel returns every video in a channel (the owner's view), newest
// first, with discovery-card data.
func (s *Service) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]FeedItem, error) {
	rows, err := s.repo.ListVideosByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		it.PublishAt = TimePtr(r.PublishAt) // studio view: badge scheduled videos
		items = append(items, it)
	}
	return items, nil
}

// ListPublicByChannel returns only the channel's public, published videos (the
// anonymous view), newest first, with discovery-card data. When hideSensitive is
// true, sensitive videos are excluded for the instance-level "hide" policy.
func (s *Service) ListPublicByChannel(ctx context.Context, channelID uuid.UUID, hideSensitive bool) ([]FeedItem, error) {
	rows, err := s.repo.ListPublicVideosByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		if hideSensitive && r.IsSensitive {
			continue
		}
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, it)
	}
	return items, nil
}

// FeedItem is a video plus discovery-card data: its view count, whether a
// poster image is available, and the probed duration (nil when unknown) so a
// card can show a length badge / resume progress bar. PublishAt is populated
// only on the owner (studio) channel list, where scheduled videos are visible.
//
// A FeedItem may also be a REMOTE video card (remote-content §3-4): Remote is
// true, Domain names the origin instance, WatchURL/StreamURL point at the
// origin (Vidra never mirrors remote media; the cached poster is served at
// GET /remote-videos/{id}/thumbnail), ChannelHandle carries the remote
// channel's name@domain identity, and Video.ChannelID/Privacy/State are zero
// values (a remote video has no local channel or lifecycle).
type FeedItem struct {
	Video              sqlcgen.Video
	Views              int64
	HasThumbnail       bool
	ChannelHandle      string
	ChannelDisplayName string
	// AuthorDisplayName is the uploader ACCOUNT's display name (config-parity
	// W5/W9: miniature_prefer_author_display_name), carried alongside the
	// channel identity on local cards; empty on remote cards (no local
	// account).
	AuthorDisplayName string
	DurationSeconds   *int32
	PublishAt         *time.Time

	Remote    bool
	Domain    string
	WatchURL  string
	StreamURL *string
}

// newFeedItem packages a video's columns and card data into a FeedItem. It lets
// the (structurally identical but distinct) sqlc row types from the feed,
// search, and channel-list queries share one conversion.
func newFeedItem(id, channelID uuid.UUID, title, description, privacy, state string, createdAt, updatedAt time.Time, views int64, hasThumbnail bool, channelHandle, channelDisplayName string, durationSeconds *int32, isSensitive bool, sensitiveReason string) FeedItem {
	return FeedItem{
		Video: sqlcgen.Video{
			ID: id, ChannelID: channelID, Title: title, Description: description,
			Privacy: privacy, State: state, CreatedAt: createdAt, UpdatedAt: updatedAt,
			IsSensitive: isSensitive, SensitiveReason: sensitiveReason,
		},
		Views:              views,
		HasThumbnail:       hasThumbnail,
		ChannelHandle:      channelHandle,
		ChannelDisplayName: channelDisplayName,
		DurationSeconds:    durationSeconds,
	}
}

// remoteCard stamps the remote-card fields (remote-content §3-4) onto a
// FeedItem built from one of the UNION feed queries. Local rows carry empty
// remote fields, so this is a no-op for them.
func remoteCard(it FeedItem, remote bool, domain, watchURL string, streamURL *string) FeedItem {
	it.Remote = remote
	it.Domain = domain
	it.WatchURL = watchURL
	it.StreamURL = streamURL
	return it
}

// feedSorts are the accepted feed ordering modes.
var feedSorts = map[string]bool{"recent": true, "popular": true, "trending": true}

// NormalizeFeedSort returns sort when it is a recognised mode, else "recent".
func NormalizeFeedSort(sort string) string {
	if feedSorts[sort] {
		return sort
	}
	return "recent"
}

// FeedScopeAll is the feed ?scope value that adds federated remote videos to
// the public feed (remote-content §4); the default scope is local-only.
const FeedScopeAll = "all"

// NormalizeFeedScope returns "all" when asked for it, else "local" (the
// default; unknown values fall back like NormalizeFeedSort).
func NormalizeFeedScope(scope string) string {
	if scope == FeedScopeAll {
		return FeedScopeAll
	}
	return "local"
}

// FeedFilter narrows the public feed. Zero values mean "no filter". Tag is
// matched exactly against the stored (lowercased) tag set; Category and
// Language are taxonomy ids the HTTP layer has already validated.
type FeedFilter struct {
	Tag      string
	Category string
	Language string
	// HideSensitive excludes is_sensitive videos (the server-side enforcement of
	// the "hide" sensitive-content policy on the public browse/search surfaces).
	HideSensitive bool
}

// ListPublic returns the cross-channel public feed in the requested order
// (recent|popular|trending; unknown → recent), optionally narrowed by filter,
// each item carrying its view count and poster availability. scope=all
// (remote-content §4) adds federated remote videos (excluded whenever a
// tag/category/language filter is active — remote videos carry no local
// taxonomy); the default local scope never includes them. The caller clamps
// limit/offset.
func (s *Service) ListPublic(ctx context.Context, sort, scope string, filter FeedFilter, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]FeedItem, error) {
	rows, err := s.repo.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		Sort:          NormalizeFeedSort(sort),
		IncludeRemote: NormalizeFeedScope(scope) == FeedScopeAll,
		ViewerID:      pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		Tag:           nilIfEmpty(strings.ToLower(filter.Tag)),
		Category:      nilIfEmpty(filter.Category),
		Language:      nilIfEmpty(filter.Language),
		HideSensitive: filter.HideSensitive,
		ResultLimit:   limit,
		ResultOffset:  offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, remoteCard(it, r.Remote, r.Domain, r.WatchUrl, r.StreamUrl))
	}
	return items, nil
}

// ListSubscriptions returns the user's subscriptions feed (remote-content §3):
// public, published videos from the LOCAL channels they follow UNIONed with the
// ingested remote videos of their ACCEPTED remote-channel follows, newest
// first, each carrying discovery-card data. The caller clamps limit/offset.
func (s *Service) ListSubscriptions(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]FeedItem, error) {
	rows, err := s.repo.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{
		FollowerID:   userID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, remoteCard(it, r.Remote, r.Domain, r.WatchUrl, r.StreamUrl))
	}
	return items, nil
}

// Save adds videoID to userID's library (idempotent). The caller confirms the
// video is saveable (exists + public + published) first.
func (s *Service) Save(ctx context.Context, videoID, userID uuid.UUID) error {
	return s.repo.SaveVideo(ctx, sqlcgen.SaveVideoParams{UserID: userID, VideoID: videoID})
}

// Unsave removes videoID from userID's library (idempotent).
func (s *Service) Unsave(ctx context.Context, videoID, userID uuid.UUID) error {
	return s.repo.UnsaveVideo(ctx, sqlcgen.UnsaveVideoParams{UserID: userID, VideoID: videoID})
}

// ListSaved returns userID's saved public, published videos as feed cards,
// newest-saved first. The caller clamps limit/offset.
func (s *Service) ListSaved(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]FeedItem, error) {
	rows, err := s.repo.ListSavedVideos(ctx, sqlcgen.ListSavedVideosParams{
		UserID:       userID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, it)
	}
	return items, nil
}

// HistoryItem is a watched video as a discovery card plus the viewer's saved
// resume position (seconds) and when they last watched it.
type HistoryItem struct {
	FeedItem
	PositionSeconds int32
	WatchedAt       time.Time
}

// RecordProgress upserts the caller's resume position (seconds, clamped to >= 0)
// for a video and moves it to the top of their watch history. The caller
// confirms the video is watchable (exists + public + published) first.
func (s *Service) RecordProgress(ctx context.Context, videoID, userID uuid.UUID, position int32) error {
	if position < 0 {
		position = 0
	}
	_, err := s.repo.UpsertWatchProgress(ctx, sqlcgen.UpsertWatchProgressParams{
		UserID:          userID,
		VideoID:         videoID,
		PositionSeconds: position,
	})
	return err
}

// Progress returns the caller's saved resume position for a video. The bool is
// false when no progress has been recorded (a miss is reported as absent — and a
// position of 0 — not an error).
func (s *Service) Progress(ctx context.Context, videoID, userID uuid.UUID) (int32, bool, error) {
	row, err := s.repo.GetWatchProgress(ctx, sqlcgen.GetWatchProgressParams{UserID: userID, VideoID: videoID})
	if err != nil {
		return 0, false, nil
	}
	return row.PositionSeconds, true, nil
}

// ListHistory returns the user's watch history (public, published videos),
// most-recently-watched first, as cards carrying the resume position and the
// time last watched. The caller clamps limit/offset. When inProgress is true it
// returns the "Continue watching" subset: entries with position >= 5s that are
// not effectively finished (>= 95% of the known duration), excluding
// barely-started and completed videos.
func (s *Service) ListHistory(ctx context.Context, userID uuid.UUID, limit, offset int32, inProgress bool) ([]HistoryItem, error) {
	if inProgress {
		rows, err := s.repo.ListWatchHistoryInProgress(ctx, sqlcgen.ListWatchHistoryInProgressParams{
			UserID:       userID,
			ResultLimit:  limit,
			ResultOffset: offset,
		})
		if err != nil {
			return nil, err
		}
		items := make([]HistoryItem, 0, len(rows))
		for _, r := range rows {
			it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
			it.AuthorDisplayName = r.AuthorDisplayName
			items = append(items, HistoryItem{
				FeedItem:        it,
				PositionSeconds: r.PositionSeconds,
				WatchedAt:       r.WatchedAt,
			})
		}
		return items, nil
	}
	rows, err := s.repo.ListWatchHistory(ctx, sqlcgen.ListWatchHistoryParams{
		UserID:       userID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]HistoryItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, HistoryItem{
			FeedItem:        it,
			PositionSeconds: r.PositionSeconds,
			WatchedAt:       r.WatchedAt,
		})
	}
	return items, nil
}

// RemoveHistoryEntry removes a single video from the user's history (idempotent).
func (s *Service) RemoveHistoryEntry(ctx context.Context, videoID, userID uuid.UUID) error {
	return s.repo.DeleteWatchHistoryEntry(ctx, sqlcgen.DeleteWatchHistoryEntryParams{UserID: userID, VideoID: videoID})
}

// ClearHistory removes all of the user's watch history (idempotent).
func (s *Service) ClearHistory(ctx context.Context, userID uuid.UUID) error {
	return s.repo.ClearWatchHistory(ctx, userID)
}

// SearchPublic returns public, published videos whose title matches query
// (case-insensitive substring, ranked by trigram similarity then recency),
// paginated, with discovery-card data. Ingested federated remote videos are
// UNIONed in by title match, flagged remote (remote-content §4). Optional
// tag/category/language facet filters narrow the local results (mirroring the
// feed); any active filter excludes remote videos (they carry no local
// taxonomy). The caller validates/clamps query, limit, and offset.
func (s *Service) SearchPublic(ctx context.Context, query string, filter FeedFilter, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]FeedItem, error) {
	rows, err := s.repo.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
		Query:         query,
		ViewerID:      pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		Tag:           nilIfEmpty(strings.ToLower(filter.Tag)),
		Category:      nilIfEmpty(filter.Category),
		Language:      nilIfEmpty(filter.Language),
		HideSensitive: filter.HideSensitive,
		ResultLimit:   limit,
		ResultOffset:  offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, remoteCard(it, r.Remote, r.Domain, r.WatchUrl, r.StreamUrl))
	}
	return items, nil
}

// HydrateByIDs resolves a ranked list of video ids to discovery cards under the
// full canonical predicate (search-service W4): the returned cards are in the
// SAME order as ids, with any id that fails visibility (blocked, owner
// unlisted/muted/blocked, sensitive-under-hide, non-public) dropped. This is how
// core hydrates the visibility-safe id list vidra-search returns for
// search/recommendations — the search index never holds per-viewer state.
func (s *Service) HydrateByIDs(ctx context.Context, ids []uuid.UUID, viewerID uuid.UUID, viewerAuthed, hideSensitive bool) ([]FeedItem, error) {
	if len(ids) == 0 {
		return []FeedItem{}, nil
	}
	rows, err := s.repo.ListPublicVideosByIDs(ctx, sqlcgen.ListPublicVideosByIDsParams{
		Ids:           ids,
		ViewerID:      pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		HideSensitive: hideSensitive,
	})
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]FeedItem, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		byID[r.ID] = it
	}
	items := make([]FeedItem, 0, len(rows))
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			items = append(items, it)
		}
	}
	return items, nil
}

// RelatedFallback is the server-side "related videos" fallback (search-service
// W4) used when vidra-search is unavailable or returns nothing: same-channel +
// same-category candidates for the source video, under the full canonical
// predicate, ordered same-channel first then by popularity. category may be nil.
func (s *Service) RelatedFallback(ctx context.Context, videoID, channelID uuid.UUID, category *string, viewerID uuid.UUID, viewerAuthed, hideSensitive bool, limit int32) ([]FeedItem, error) {
	rows, err := s.repo.ListRelatedVideosFallback(ctx, sqlcgen.ListRelatedVideosFallbackParams{
		ChannelID:     channelID,
		ExcludeID:     videoID,
		ViewerID:      pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		HideSensitive: hideSensitive,
		Category:      nilIfEmpty(derefString(category)),
		ResultLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		it := newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName, r.DurationSeconds, r.IsSensitive, r.SensitiveReason)
		it.AuthorDisplayName = r.AuthorDisplayName
		items = append(items, it)
	}
	return items, nil
}

// derefString returns the pointee or "" when nil.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// AdminVideo is a video as seen in the admin/moderator videos overview: any
// privacy/state, with the owning channel, view count, and current block status.
type AdminVideo struct {
	ID                 uuid.UUID
	Title              string
	Privacy            string
	State              string
	ChannelHandle      string
	ChannelDisplayName string
	Views              int64
	PublishedAt        time.Time
	DurationSeconds    *int32
	IsLocal            bool
	OriginDomain       string
	WatchURL           string
	IsSensitive        bool
	ExternalLink       bool
	HasThumbnail       bool
	HasOriginal        bool
	HLSCount           int32
	WebVideoCount      int32
	SizeBytes          int64
	Blocked            bool
}

// ListAdmin returns all videos (any privacy/state) newest first for the
// admin/moderator overview. A non-empty query filters by title substring. The
// caller clamps limit/offset.
func (s *Service) ListAdmin(ctx context.Context, query string, limit, offset int32) ([]AdminVideo, error) {
	var q *string
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		q = &trimmed
	}
	rows, err := s.repo.ListAdminVideos(ctx, sqlcgen.ListAdminVideosParams{
		Query:        q,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]AdminVideo, 0, len(rows))
	for _, r := range rows {
		items = append(items, AdminVideo{
			ID:                 r.ID,
			Title:              r.Title,
			Privacy:            r.Privacy,
			State:              r.State,
			ChannelHandle:      r.ChannelHandle,
			ChannelDisplayName: r.ChannelDisplayName,
			Views:              r.Views,
			PublishedAt:        r.CreatedAt,
			DurationSeconds:    r.DurationSeconds,
			IsLocal:            r.IsLocal,
			OriginDomain:       r.OriginDomain,
			WatchURL:           r.WatchUrl,
			IsSensitive:        r.IsSensitive,
			ExternalLink:       r.ExternalLink,
			HasThumbnail:       r.HasThumbnail,
			HasOriginal:        r.HasOriginal,
			HLSCount:           r.HlsCount,
			WebVideoCount:      r.WebVideoCount,
			SizeBytes:          r.SizeBytes,
			Blocked:            r.Blocked,
		})
	}
	return items, nil
}

// trimPtr trims a non-nil string pointer's value, leaving nil untouched so a
// COALESCE update skips the column.
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	t := strings.TrimSpace(*p)
	return &t
}

// nilIfEmpty maps an optional string to a nullable column value: a blank string
// (after trimming) becomes NULL, otherwise the trimmed value.
func nilIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// timestamptz maps an optional time to its nullable column value (nil = NULL,
// which COALESCE-updates leave unchanged).
func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// TimePtr converts a nullable database timestamp to a *time.Time for JSON
// views (nil when NULL).
func TimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}
