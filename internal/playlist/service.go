// Package playlist implements named video playlists for vidra-core: an ordered,
// named collection of videos owned by a user, with public/unlisted/private
// visibility. It is HTTP-agnostic and testable without a server.
package playlist

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no playlist matches the lookup.
	ErrNotFound = errors.New("playlist: not found")
	// ErrForbidden means the caller does not own the playlist.
	ErrForbidden = errors.New("playlist: not owner")
	// ErrInvalidOrder means a reorder request's video ids are not exactly the
	// playlist's current item set (a missing, extra, or duplicated id).
	ErrInvalidOrder = errors.New("playlist: reorder ids do not match the playlist items")
	// ErrUnsupportedMedia means an uploaded cover image is not an accepted type.
	ErrUnsupportedMedia = errors.New("playlist: unsupported image type")
	// ErrStorageUnavailable means no blob backend is configured for cover uploads.
	ErrStorageUnavailable = errors.New("playlist: storage backend not configured")
)

// acceptedImageExts maps an accepted playlist-cover upload extension to the
// content type served for it (authoritative — not the client-declared type).
var acceptedImageExts = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
}

// Repository is the data access the playlist service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreatePlaylist(ctx context.Context, arg sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error)
	GetPlaylistByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetPlaylistByIDRow, error)
	ListPlaylistsByOwner(ctx context.Context, arg sqlcgen.ListPlaylistsByOwnerParams) ([]sqlcgen.ListPlaylistsByOwnerRow, error)
	CountPlaylistsByOwner(ctx context.Context, ownerID uuid.UUID) (int64, error)
	UpdatePlaylist(ctx context.Context, arg sqlcgen.UpdatePlaylistParams) (sqlcgen.Playlist, error)
	DeletePlaylist(ctx context.Context, id uuid.UUID) error
	AddPlaylistItem(ctx context.Context, arg sqlcgen.AddPlaylistItemParams) error
	RemovePlaylistItem(ctx context.Context, arg sqlcgen.RemovePlaylistItemParams) error
	ListPlaylistItems(ctx context.Context, arg sqlcgen.ListPlaylistItemsParams) ([]sqlcgen.ListPlaylistItemsRow, error)
	CountPlaylistItems(ctx context.Context, playlistID uuid.UUID) (int64, error)
	ListPlaylistItemVideoIDs(ctx context.Context, playlistID uuid.UUID) ([]uuid.UUID, error)
	ReorderPlaylistItems(ctx context.Context, arg sqlcgen.ReorderPlaylistItemsParams) error
	SetPlaylistThumbnail(ctx context.Context, arg sqlcgen.SetPlaylistThumbnailParams) error
	ClearPlaylistThumbnail(ctx context.Context, id uuid.UUID) error
}

// Service holds the playlist application logic. blobs is the media backend used
// by cover-image uploads; it may be nil when covers are not wired.
type Service struct {
	repo   Repository
	blobs  storage.Backend
	mirror Mirror
}

// Mirror is the optional IPFS-mirror hook (fix_plan P19). A public playlist's
// cover is add+pinned; a non-public playlist's cover is unpinned. Called
// best-effort — a mirror error never fails the playlist operation. nil disables
// it; *ipfsmirror.Service satisfies it structurally.
type Mirror interface {
	EnqueuePlaylistCover(ctx context.Context, playlistID uuid.UUID) error
	EnqueueUnpin(ctx context.Context, objectKey string) error
}

// Option customises the Service.
type Option func(*Service)

// WithStorage wires the blob backend used for playlist cover uploads.
func WithStorage(b storage.Backend) Option {
	return func(s *Service) { s.blobs = b }
}

// WithMirror wires the optional IPFS-mirror hook.
func WithMirror(m Mirror) Option {
	return func(s *Service) { s.mirror = m }
}

// NewService builds the playlist service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// VideoCard is a playlist item projected as discovery-card data (the same shape
// the feed uses), so the HTTP layer can render it like any other video card.
type VideoCard struct {
	ID                 uuid.UUID
	ChannelID          uuid.UUID
	Title              string
	Description        string
	Privacy            string
	State              string
	CreatedAt          time.Time
	Views              int64
	HasThumbnail       bool
	ChannelHandle      string
	ChannelDisplayName string
}

// CreateInput is validated, normalized playlist-creation data. Visibility must
// already be one of public/unlisted/private (the HTTP layer validates/defaults it).
type CreateInput struct {
	Title       string
	Description string
	Visibility  string
}

// Create makes a new playlist owned by ownerID.
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, in CreateInput) (sqlcgen.Playlist, error) {
	return s.repo.CreatePlaylist(ctx, sqlcgen.CreatePlaylistParams{
		OwnerID:     ownerID,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Visibility:  in.Visibility,
	})
}

// GetByID returns a playlist (with owner id + video count). Miss → ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetPlaylistByIDRow, error) {
	p, err := s.repo.GetPlaylistByID(ctx, id)
	if err != nil {
		return sqlcgen.GetPlaylistByIDRow{}, ErrNotFound
	}
	return p, nil
}

// ListOwn returns all playlists owned by the given user, newest first.
func (s *Service) ListOwn(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]sqlcgen.ListPlaylistsByOwnerRow, int64, error) {
	rows, err := s.repo.ListPlaylistsByOwner(ctx, sqlcgen.ListPlaylistsByOwnerParams{
		OwnerID:      ownerID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountPlaylistsByOwner(ctx, ownerID)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// UpdateInput is a partial playlist update: nil fields are left unchanged.
// Visibility, when set, is already validated by the HTTP layer.
type UpdateInput struct {
	Title       *string
	Description *string
	Visibility  *string
}

// Update changes a playlist's mutable fields. Only the owner may update; a
// non-owner gets ErrForbidden and an unknown id gets ErrNotFound.
func (s *Service) Update(ctx context.Context, ownerID, id uuid.UUID, in UpdateInput) (sqlcgen.Playlist, error) {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return sqlcgen.Playlist{}, err
	}
	if p.OwnerID != ownerID {
		return sqlcgen.Playlist{}, ErrForbidden
	}
	updated, err := s.repo.UpdatePlaylist(ctx, sqlcgen.UpdatePlaylistParams{
		ID:          id,
		Title:       pgconv.TrimPtr(in.Title),
		Description: pgconv.TrimPtr(in.Description),
		Visibility:  in.Visibility,
	})
	if err != nil {
		return updated, err
	}
	// A visibility change flips cover eligibility (public ⇒ pin, else unpin).
	if in.Visibility != nil && s.mirror != nil {
		_ = s.mirror.EnqueuePlaylistCover(ctx, id) // best-effort
	}
	return updated, nil
}

// Delete removes a playlist. Only the owner may delete; non-owner → ErrForbidden,
// unknown id → ErrNotFound.
func (s *Service) Delete(ctx context.Context, ownerID, id uuid.UUID) error {
	p, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if p.OwnerID != ownerID {
		return ErrForbidden
	}
	return s.repo.DeletePlaylist(ctx, id)
}

// AddItem appends videoID to the owner's playlist (idempotent). Only the owner
// may modify the playlist. The caller confirms the video is addable (exists +
// public + published) first.
func (s *Service) AddItem(ctx context.Context, ownerID, playlistID, videoID uuid.UUID) error {
	p, err := s.GetByID(ctx, playlistID)
	if err != nil {
		return err
	}
	if p.OwnerID != ownerID {
		return ErrForbidden
	}
	return s.repo.AddPlaylistItem(ctx, sqlcgen.AddPlaylistItemParams{PlaylistID: playlistID, VideoID: videoID})
}

// RemoveItem removes videoID from the owner's playlist (idempotent). Only the
// owner may modify the playlist.
func (s *Service) RemoveItem(ctx context.Context, ownerID, playlistID, videoID uuid.UUID) error {
	p, err := s.GetByID(ctx, playlistID)
	if err != nil {
		return err
	}
	if p.OwnerID != ownerID {
		return ErrForbidden
	}
	return s.repo.RemovePlaylistItem(ctx, sqlcgen.RemovePlaylistItemParams{PlaylistID: playlistID, VideoID: videoID})
}

// ReorderItems rewrites the order of a playlist's items. Only the owner may
// reorder (non-owner → ErrForbidden, unknown id → ErrNotFound). videoIDs must be
// exactly the playlist's current items — every item present, none extra, no
// duplicates — else ErrInvalidOrder; this matches how a drag-reorder UI submits
// the full new order and rejects a stale/garbage request rather than silently
// applying a partial reorder.
func (s *Service) ReorderItems(ctx context.Context, ownerID, playlistID uuid.UUID, videoIDs []uuid.UUID) error {
	p, err := s.GetByID(ctx, playlistID)
	if err != nil {
		return err
	}
	if p.OwnerID != ownerID {
		return ErrForbidden
	}
	current, err := s.repo.ListPlaylistItemVideoIDs(ctx, playlistID)
	if err != nil {
		return err
	}
	if !samePermutation(videoIDs, current) {
		return ErrInvalidOrder
	}
	if len(videoIDs) == 0 {
		return nil // empty playlist: nothing to rewrite.
	}
	return s.repo.ReorderPlaylistItems(ctx, sqlcgen.ReorderPlaylistItemsParams{
		PlaylistID: playlistID,
		VideoIds:   videoIDs,
	})
}

// samePermutation reports whether want and have contain exactly the same ids
// with no duplicates in want (want is the client-supplied order; have is the
// authoritative item set). Order is irrelevant here — only set equality.
func samePermutation(want, have []uuid.UUID) bool {
	if len(want) != len(have) {
		return false
	}
	seen := make(map[uuid.UUID]bool, len(want))
	for _, id := range want {
		if seen[id] {
			return false // duplicate in the request
		}
		seen[id] = true
	}
	for _, id := range have {
		if !seen[id] {
			return false // an existing item was omitted
		}
	}
	return true
}

// ListItems returns a playlist's public, published videos in order, as cards.
func (s *Service) ListItems(ctx context.Context, playlistID uuid.UUID, limit, offset int32) ([]VideoCard, int64, error) {
	rows, err := s.repo.ListPlaylistItems(ctx, sqlcgen.ListPlaylistItemsParams{
		PlaylistID:   playlistID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountPlaylistItems(ctx, playlistID)
	if err != nil {
		return nil, 0, err
	}
	cards := make([]VideoCard, 0, len(rows))
	for _, r := range rows {
		cards = append(cards, VideoCard{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt,
			Views: r.Views, HasThumbnail: r.HasThumbnail,
			ChannelHandle: r.ChannelHandle, ChannelDisplayName: r.ChannelDisplayName,
		})
	}
	return cards, total, nil
}

// UploadInput is an uploaded cover image: its declared filename (used only to
// derive the accepted extension) and the byte stream.
type UploadInput struct {
	Filename string
	Reader   io.Reader
}

// SetThumbnail stores an owner-uploaded cover image for a playlist, replacing any
// previous cover. Owner-only (non-owner → ErrForbidden, unknown id → ErrNotFound);
// a non-image extension → ErrUnsupportedMedia. Returns the stored bare extension
// (e.g. "jpg"). The blob lives at playlist-thumbnails/<id>.<ext>; when the new
// extension differs from an existing one the stale blob is removed.
func (s *Service) SetThumbnail(ctx context.Context, ownerID, playlistID uuid.UUID, in UploadInput) (string, error) {
	if s.blobs == nil {
		return "", ErrStorageUnavailable
	}
	p, err := s.GetByID(ctx, playlistID)
	if err != nil {
		return "", err
	}
	if p.OwnerID != ownerID {
		return "", ErrForbidden
	}
	dotExt := strings.ToLower(filepath.Ext(in.Filename))
	if _, ok := acceptedImageExts[dotExt]; !ok {
		return "", ErrUnsupportedMedia
	}
	ext := strings.TrimPrefix(dotExt, ".")
	if ext == "jpeg" {
		ext = "jpg" // canonicalize so the served key/extension is stable
	}
	key := media.PlaylistThumbnailKey(playlistID, ext)
	if _, err := s.blobs.Put(ctx, key, in.Reader); err != nil {
		return "", err
	}
	// Remove a stale cover left at a different extension.
	if p.ThumbnailExt != nil && *p.ThumbnailExt != "" && *p.ThumbnailExt != ext {
		staleKey := media.PlaylistThumbnailKey(playlistID, *p.ThumbnailExt)
		_ = s.blobs.Delete(ctx, staleKey)
		if s.mirror != nil {
			_ = s.mirror.EnqueueUnpin(ctx, staleKey) // best-effort: unpin the superseded cover
		}
	}
	if err := s.repo.SetPlaylistThumbnail(ctx, sqlcgen.SetPlaylistThumbnailParams{ID: playlistID, ThumbnailExt: &ext}); err != nil {
		return "", err
	}
	if s.mirror != nil {
		_ = s.mirror.EnqueuePlaylistCover(ctx, playlistID) // best-effort
	}
	return ext, nil
}

// ClearThumbnail removes a playlist's cover (blob + column). Owner-only;
// idempotent when no cover is set.
func (s *Service) ClearThumbnail(ctx context.Context, ownerID, playlistID uuid.UUID) error {
	p, err := s.GetByID(ctx, playlistID)
	if err != nil {
		return err
	}
	if p.OwnerID != ownerID {
		return ErrForbidden
	}
	if p.ThumbnailExt != nil && *p.ThumbnailExt != "" && s.blobs != nil {
		coverKey := media.PlaylistThumbnailKey(playlistID, *p.ThumbnailExt)
		_ = s.blobs.Delete(ctx, coverKey)
		if s.mirror != nil {
			_ = s.mirror.EnqueueUnpin(ctx, coverKey) // best-effort
		}
	}
	return s.repo.ClearPlaylistThumbnail(ctx, playlistID)
}

// ThumbnailContentType returns the served content type for a stored cover
// extension, and ok=false when the extension is not an accepted image type.
func ThumbnailContentType(ext string) (string, bool) {
	ct, ok := acceptedImageExts["."+strings.TrimPrefix(strings.ToLower(ext), ".")]
	return ct, ok
}
