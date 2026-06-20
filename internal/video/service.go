// Package video implements video publishing for vidra-core. This first slice
// covers the metadata lifecycle (create draft, read); files, transcoding, and
// playback land in later slices. It is HTTP-agnostic and testable without a
// server.
package video

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

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
	ListVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error)
	ListPublicVideosByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error)
	ListPublicVideos(ctx context.Context, arg sqlcgen.ListPublicVideosParams) ([]sqlcgen.Video, error)
	SearchPublicVideos(ctx context.Context, arg sqlcgen.SearchPublicVideosParams) ([]sqlcgen.Video, error)
	UpdateVideo(ctx context.Context, arg sqlcgen.UpdateVideoParams) (sqlcgen.Video, error)
	DeleteVideo(ctx context.Context, id uuid.UUID) error
	CreateVideoFile(ctx context.Context, arg sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error)
	DeleteVideoFilesByVideoAndKind(ctx context.Context, arg sqlcgen.DeleteVideoFilesByVideoAndKindParams) error
	SetVideoState(ctx context.Context, arg sqlcgen.SetVideoStateParams) (sqlcgen.Video, error)
}

// Prober inspects a stored original file and reports whether it is valid,
// playable media. It is the seam for FFprobe/transcoding: when none is
// configured the original is trusted as-is (the upload already passed the
// extension allow-list) and the video is published directly. The real probe is
// wired once FFmpeg is provisioned in the runtime image.
type Prober interface {
	// Probe validates the object at the given storage key, returning a non-nil
	// error when it is not usable media.
	Probe(ctx context.Context, storageKey string) error
}

// Service holds the video application logic.
type Service struct {
	repo   Repository
	blobs  storage.Backend
	prober Prober
}

// Option customises the Service.
type Option func(*Service)

// WithProber wires a media prober used by Process to validate originals before
// publishing. Without it, Process publishes the original unprobed.
func WithProber(p Prober) Option {
	return func(s *Service) { s.prober = p }
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
}

// CreateDraft creates a new draft video under the given channel. Ownership is
// enforced by the caller (the HTTP layer checks channel ownership first).
func (s *Service) CreateDraft(ctx context.Context, channelID uuid.UUID, in CreateInput) (sqlcgen.Video, error) {
	return s.repo.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID:   channelID,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Privacy:     in.Privacy,
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
	if s.prober != nil {
		if err := s.prober.Probe(ctx, originalKey); err != nil {
			state = "failed"
		}
	}
	return s.repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: state})
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

// originalKey builds the storage key for a video's original file from an already
// validated extension. The video id namespaces the key so files never collide
// across videos, and the storage backend itself rejects any traversal.
func originalKey(videoID uuid.UUID, ext string) string {
	return "videos/" + videoID.String() + "/original" + ext
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

// ListByChannel returns every video in a channel (the owner's view), newest first.
func (s *Service) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	return s.repo.ListVideosByChannel(ctx, channelID)
}

// ListPublicByChannel returns only the channel's public videos (the anonymous
// view), newest first.
func (s *Service) ListPublicByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	return s.repo.ListPublicVideosByChannel(ctx, channelID)
}

// ListPublic returns the cross-channel public video feed, newest first, with
// limit/offset pagination. The caller is responsible for clamping limit/offset
// to sane bounds.
func (s *Service) ListPublic(ctx context.Context, limit, offset int32) ([]sqlcgen.Video, error) {
	return s.repo.ListPublicVideos(ctx, sqlcgen.ListPublicVideosParams{Limit: limit, Offset: offset})
}

// SearchPublic returns public videos whose title matches query (case-insensitive
// substring, ranked by trigram similarity then recency), paginated. The caller
// validates/clamps query, limit, and offset.
func (s *Service) SearchPublic(ctx context.Context, query string, limit, offset int32) ([]sqlcgen.Video, error) {
	q := query
	return s.repo.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
		Query:        &q,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
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
