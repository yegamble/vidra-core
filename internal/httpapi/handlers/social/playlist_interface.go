package social

import (
	"context"

	"athena/internal/domain"

	"github.com/google/uuid"
)

type PlaylistServiceInterface interface {
	CreatePlaylist(ctx context.Context, userID uuid.UUID, req *domain.CreatePlaylistRequest) (*domain.Playlist, error)
	GetPlaylist(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID) (*domain.Playlist, error)
	UpdatePlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, req domain.UpdatePlaylistRequest) error
	DeletePlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID) error
	ListPlaylists(ctx context.Context, opts domain.PlaylistListOptions) (*domain.PlaylistListResponse, error)
	AddVideoToPlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, videoID uuid.UUID, position *int) error
	RemoveVideoFromPlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, itemID uuid.UUID) error
	GetPlaylistItems(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID, limit, offset int) ([]domain.PlaylistItem, error)
	ReorderPlaylistItem(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, itemID uuid.UUID, newPosition int) error
	GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error)
	AddToWatchLater(ctx context.Context, userID uuid.UUID, videoID uuid.UUID) error
}
