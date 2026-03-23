package port

import (
	"vidra-core/internal/domain"
	"context"

	"github.com/google/uuid"
)

// PlaylistRepository defines the interface for playlist data operations
type PlaylistRepository interface {
	Create(ctx context.Context, playlist *domain.Playlist) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Playlist, error)
	Update(ctx context.Context, id uuid.UUID, updates domain.UpdatePlaylistRequest) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, opts domain.PlaylistListOptions) ([]*domain.Playlist, int, error)

	// Playlist items management
	AddItem(ctx context.Context, playlistID, videoID uuid.UUID, position *int) error
	RemoveItem(ctx context.Context, playlistID, itemID uuid.UUID) error
	GetItems(ctx context.Context, playlistID uuid.UUID, limit, offset int) ([]*domain.PlaylistItem, error)
	ReorderItem(ctx context.Context, playlistID, itemID uuid.UUID, newPosition int) error

	// Utility methods
	IsOwner(ctx context.Context, playlistID, userID uuid.UUID) (bool, error)
	GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error)
}
