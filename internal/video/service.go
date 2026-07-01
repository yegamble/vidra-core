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
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/media"
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
)

// acceptedVideoExts is the allow-list of original-upload file extensions. It is
// deliberately a container/extension gate only — the declared content type is
// client-controlled and not trusted; real content validation is FFprobe's job
// in a later slice.
var acceptedVideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true,
	".avi": true, ".ogv": true, ".ogg": true, ".mpg": true, ".mpeg": true,
	".ts": true, ".flv": true, ".wmv": true, ".3gp": true,
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
	UpsertVideoMetadata(ctx context.Context, arg sqlcgen.UpsertVideoMetadataParams) (sqlcgen.VideoMetadatum, error)
	GetVideoMetadata(ctx context.Context, videoID uuid.UUID) (sqlcgen.VideoMetadatum, error)
	IncrementVideoViews(ctx context.Context, videoID uuid.UUID) (int64, error)
	GetVideoViews(ctx context.Context, videoID uuid.UUID) (int64, error)
	UpsertWatchProgress(ctx context.Context, arg sqlcgen.UpsertWatchProgressParams) (sqlcgen.WatchHistory, error)
	GetWatchProgress(ctx context.Context, arg sqlcgen.GetWatchProgressParams) (sqlcgen.WatchHistory, error)
	ListWatchHistory(ctx context.Context, arg sqlcgen.ListWatchHistoryParams) ([]sqlcgen.ListWatchHistoryRow, error)
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

// Thumbnailer produces a poster image (JPEG bytes) for the media at storageKey.
// durationSeconds (0 if unknown) hints which frame to grab. It is the seam for
// FFmpeg thumbnail extraction; when none is configured videos publish without a
// poster.
type Thumbnailer interface {
	Thumbnail(ctx context.Context, storageKey string, durationSeconds int) ([]byte, error)
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
	repo        Repository
	blobs       storage.Backend
	prober      Prober
	thumbnailer Thumbnailer
	viewDeduper ViewDeduper
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

// WithViewDeduper wires per-viewer view de-duplication. Without it, every
// recorded view counts.
func WithViewDeduper(d ViewDeduper) Option {
	return func(s *Service) { s.viewDeduper = d }
}

// NewService builds the video service. blobs is the media storage backend used
// by uploads; it may be nil when uploads are not wired (e.g. some tests).
func NewService(repo Repository, blobs storage.Backend, opts ...Option) *Service {
	s := &Service{repo: repo, blobs: blobs}
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
}

// CreateDraft creates a new draft video under the given channel. Ownership is
// enforced by the caller (the HTTP layer checks channel ownership first).
func (s *Service) CreateDraft(ctx context.Context, channelID uuid.UUID, in CreateInput) (sqlcgen.Video, error) {
	return s.repo.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID:   channelID,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Privacy:     in.Privacy,
		Category:    nilIfEmpty(in.Category),
		Language:    nilIfEmpty(in.Language),
		License:     nilIfEmpty(in.License),
	})
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
	ext, ok := acceptedExt(in.Filename)
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
	return updated, file, nil
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
	if s.prober != nil {
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
	return s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: state})
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
	_, err = s.repo.IncrementVideoViews(ctx, videoID)
	return err
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

// acceptedExt returns the normalized (lowercased) extension of filename when it
// is an accepted video container, and false otherwise. It is the upload type gate.
func acceptedExt(filename string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(filename))
	if acceptedVideoExts[ext] {
		return ext, true
	}
	return "", false
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
}

// Update changes a video's mutable metadata. Only the owner may update; a
// non-owner gets ErrForbidden and an unknown id gets ErrNotFound.
func (s *Service) Update(ctx context.Context, ownerID, id uuid.UUID, in UpdateInput) (sqlcgen.Video, error) {
	v, err := s.GetByID(ctx, id)
	if err != nil {
		return sqlcgen.Video{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.Video{}, ErrForbidden
	}
	return s.repo.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID:          id,
		Title:       trimPtr(in.Title),
		Description: trimPtr(in.Description),
		Privacy:     in.Privacy,
		Category:    in.Category,
		Language:    in.Language,
		License:     in.License,
	})
}

// Delete removes a video. Only the owner may delete; non-owner → ErrForbidden,
// unknown id → ErrNotFound.
func (s *Service) Delete(ctx context.Context, ownerID, id uuid.UUID) error {
	v, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if v.OwnerID != ownerID {
		return ErrForbidden
	}
	return s.repo.DeleteVideo(ctx, id)
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
		items = append(items, newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName))
	}
	return items, nil
}

// ListPublicByChannel returns only the channel's public, published videos (the
// anonymous view), newest first, with discovery-card data.
func (s *Service) ListPublicByChannel(ctx context.Context, channelID uuid.UUID) ([]FeedItem, error) {
	rows, err := s.repo.ListPublicVideosByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName))
	}
	return items, nil
}

// FeedItem is a video plus discovery-card data: its view count and whether a
// poster image is available.
type FeedItem struct {
	Video              sqlcgen.Video
	Views              int64
	HasThumbnail       bool
	ChannelHandle      string
	ChannelDisplayName string
}

// newFeedItem packages a video's columns and card data into a FeedItem. It lets
// the (structurally identical but distinct) sqlc row types from the feed,
// search, and channel-list queries share one conversion.
func newFeedItem(id, channelID uuid.UUID, title, description, privacy, state string, createdAt, updatedAt time.Time, views int64, hasThumbnail bool, channelHandle, channelDisplayName string) FeedItem {
	return FeedItem{
		Video: sqlcgen.Video{
			ID: id, ChannelID: channelID, Title: title, Description: description,
			Privacy: privacy, State: state, CreatedAt: createdAt, UpdatedAt: updatedAt,
		},
		Views:              views,
		HasThumbnail:       hasThumbnail,
		ChannelHandle:      channelHandle,
		ChannelDisplayName: channelDisplayName,
	}
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

// ListPublic returns the cross-channel public feed in the requested order
// (recent|popular|trending; unknown → recent), each item carrying its view
// count and poster availability. The caller clamps limit/offset.
func (s *Service) ListPublic(ctx context.Context, sort string, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]FeedItem, error) {
	rows, err := s.repo.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		Sort:         NormalizeFeedSort(sort),
		ViewerID:     pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName))
	}
	return items, nil
}

// ListSubscriptions returns public, published videos from the channels the user
// follows, newest first, each carrying its view count and poster availability.
// The caller clamps limit/offset.
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
		items = append(items, newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName))
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
		items = append(items, newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName))
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
// time last watched. The caller clamps limit/offset.
func (s *Service) ListHistory(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]HistoryItem, error) {
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
		items = append(items, HistoryItem{
			FeedItem:        newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName),
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
// paginated, with discovery-card data. The caller validates/clamps query,
// limit, and offset.
func (s *Service) SearchPublic(ctx context.Context, query string, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]FeedItem, error) {
	q := query
	rows, err := s.repo.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
		Query:        &q,
		ViewerID:     pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, newFeedItem(r.ID, r.ChannelID, r.Title, r.Description, r.Privacy, r.State, r.CreatedAt, r.UpdatedAt, r.Views, r.HasThumbnail, r.ChannelHandle, r.ChannelDisplayName))
	}
	return items, nil
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
	CreatedAt          time.Time
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
			CreatedAt:          r.CreatedAt,
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
