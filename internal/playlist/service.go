// Package playlist implements named video playlists for vidra-core: an ordered,
// named collection of videos owned by a user, with public/unlisted/private
// visibility. It is HTTP-agnostic and testable without a server.
package playlist

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no playlist matches the lookup.
	ErrNotFound = errors.New("playlist: not found")
	// ErrForbidden means the caller does not own the playlist.
	ErrForbidden = errors.New("playlist: not owner")
)

// Repository is the data access the playlist service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreatePlaylist(ctx context.Context, arg sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error)
	GetPlaylistByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetPlaylistByIDRow, error)
	ListPlaylistsByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error)
	UpdatePlaylist(ctx context.Context, arg sqlcgen.UpdatePlaylistParams) (sqlcgen.Playlist, error)
	DeletePlaylist(ctx context.Context, id uuid.UUID) error
	AddPlaylistItem(ctx context.Context, arg sqlcgen.AddPlaylistItemParams) error
	RemovePlaylistItem(ctx context.Context, arg sqlcgen.RemovePlaylistItemParams) error
	ListPlaylistItems(ctx context.Context, playlistID uuid.UUID) ([]sqlcgen.ListPlaylistItemsRow, error)
}

// Service holds the playlist application logic.
type Service struct {
	repo Repository
}

// NewService builds the playlist service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
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
func (s *Service) ListOwn(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error) {
	return s.repo.ListPlaylistsByOwner(ctx, ownerID)
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
	return s.repo.UpdatePlaylist(ctx, sqlcgen.UpdatePlaylistParams{
		ID:          id,
		Title:       trimPtr(in.Title),
		Description: trimPtr(in.Description),
		Visibility:  in.Visibility,
	})
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

// ListItems returns a playlist's public, published videos in order, as cards.
func (s *Service) ListItems(ctx context.Context, playlistID uuid.UUID) ([]VideoCard, error) {
	rows, err := s.repo.ListPlaylistItems(ctx, playlistID)
	if err != nil {
		return nil, err
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
	return cards, nil
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
