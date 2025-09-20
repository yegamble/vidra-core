package domain

import (
	"time"

	"github.com/google/uuid"
)

// Playlist represents a user's playlist
type Playlist struct {
	ID           uuid.UUID `json:"id" db:"id"`
	UserID       uuid.UUID `json:"user_id" db:"user_id"`
	Name         string    `json:"name" db:"name"`
	Description  *string   `json:"description,omitempty" db:"description"`
	Privacy      Privacy   `json:"privacy" db:"privacy"`
	ThumbnailURL *string   `json:"thumbnail_url,omitempty" db:"thumbnail_url"`
	IsWatchLater bool      `json:"is_watch_later" db:"is_watch_later"`
	ItemCount    int       `json:"item_count,omitempty"` // Computed field
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// PlaylistItem represents an item in a playlist
type PlaylistItem struct {
	ID         uuid.UUID `json:"id" db:"id"`
	PlaylistID uuid.UUID `json:"playlist_id" db:"playlist_id"`
	VideoID    uuid.UUID `json:"video_id" db:"video_id"`
	Position   int       `json:"position" db:"position"`
	AddedAt    time.Time `json:"added_at" db:"added_at"`
	Video      *Video    `json:"video,omitempty"` // Populated when needed
}

// CreatePlaylistRequest represents a request to create a playlist
type CreatePlaylistRequest struct {
	Name         string  `json:"name" validate:"required,min=1,max=255"`
	Description  *string `json:"description,omitempty" validate:"omitempty,max=5000"`
	Privacy      Privacy `json:"privacy" validate:"required,oneof=public unlisted private"`
	ThumbnailURL *string `json:"thumbnail_url,omitempty"`
}

// UpdatePlaylistRequest represents a request to update a playlist
type UpdatePlaylistRequest struct {
	Name         *string  `json:"name,omitempty" validate:"omitempty,min=1,max=255"`
	Description  *string  `json:"description,omitempty" validate:"omitempty,max=5000"`
	Privacy      *Privacy `json:"privacy,omitempty" validate:"omitempty,oneof=public unlisted private"`
	ThumbnailURL *string  `json:"thumbnail_url,omitempty"`
}

// AddToPlaylistRequest represents a request to add a video to a playlist
type AddToPlaylistRequest struct {
	VideoID  uuid.UUID `json:"video_id" validate:"required"`
	Position *int      `json:"position,omitempty"` // Optional, if not provided, append to end
}

// ReorderPlaylistItemRequest represents a request to reorder items in a playlist
type ReorderPlaylistItemRequest struct {
	ItemID      uuid.UUID `json:"item_id" validate:"required"`
	NewPosition int       `json:"new_position" validate:"min=0"`
}

// PlaylistListOptions represents options for listing playlists
type PlaylistListOptions struct {
	UserID  *uuid.UUID
	Privacy *Privacy
	Limit   int
	Offset  int
	OrderBy string // "created_at", "updated_at", "name"
}

// PlaylistWithItems represents a playlist with its items
type PlaylistWithItems struct {
	Playlist
	Items []PlaylistItem `json:"items"`
}

// PlaylistListResponse represents a paginated list of playlists
type PlaylistListResponse struct {
	Playlists []*Playlist `json:"playlists"`
	Total     int         `json:"total"`
	Limit     int         `json:"limit"`
	Offset    int         `json:"offset"`
}
