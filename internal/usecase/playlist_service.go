package usecase

import (
	"athena/internal/domain"
	"context"
	"fmt"

	"github.com/google/uuid"
)

// PlaylistService handles business logic for playlists
type PlaylistService struct {
	playlistRepo PlaylistRepository
	videoRepo    VideoRepository
}

// NewPlaylistService creates a new playlist service
func NewPlaylistService(playlistRepo PlaylistRepository, videoRepo VideoRepository) *PlaylistService {
	return &PlaylistService{
		playlistRepo: playlistRepo,
		videoRepo:    videoRepo,
	}
}

// CreatePlaylist creates a new playlist
func (s *PlaylistService) CreatePlaylist(ctx context.Context, userID uuid.UUID, req *domain.CreatePlaylistRequest) (*domain.Playlist, error) {
	playlist := &domain.Playlist{
		UserID:       userID,
		Name:         req.Name,
		Description:  req.Description,
		Privacy:      req.Privacy,
		ThumbnailURL: req.ThumbnailURL,
		IsWatchLater: false,
	}

	if err := s.playlistRepo.Create(ctx, playlist); err != nil {
		return nil, fmt.Errorf("failed to create playlist: %w", err)
	}

	return playlist, nil
}

// GetPlaylist retrieves a playlist by ID
func (s *PlaylistService) GetPlaylist(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID) (*domain.Playlist, error) {
	playlist, err := s.playlistRepo.GetByID(ctx, playlistID)
	if err != nil {
		return nil, err
	}

	// Check privacy permissions
	if playlist.Privacy == domain.PrivacyPrivate && (userID == nil || *userID != playlist.UserID) {
		return nil, domain.ErrUnauthorized
	}

	return playlist, nil
}

// UpdatePlaylist updates a playlist
func (s *PlaylistService) UpdatePlaylist(ctx context.Context, userID, playlistID uuid.UUID, req domain.UpdatePlaylistRequest) error {
	// Check ownership
	isOwner, err := s.playlistRepo.IsOwner(ctx, playlistID, userID)
	if err != nil {
		return fmt.Errorf("failed to check ownership: %w", err)
	}
	if !isOwner {
		return domain.ErrUnauthorized
	}

	// Don't allow modification of system playlists (Watch Later)
	playlist, err := s.playlistRepo.GetByID(ctx, playlistID)
	if err != nil {
		return err
	}
	if playlist.IsWatchLater && req.Name != nil {
		return fmt.Errorf("cannot rename system playlist")
	}

	if err := s.playlistRepo.Update(ctx, playlistID, req); err != nil {
		return fmt.Errorf("failed to update playlist: %w", err)
	}

	return nil
}

// DeletePlaylist deletes a playlist
func (s *PlaylistService) DeletePlaylist(ctx context.Context, userID, playlistID uuid.UUID) error {
	// Check ownership
	isOwner, err := s.playlistRepo.IsOwner(ctx, playlistID, userID)
	if err != nil {
		return fmt.Errorf("failed to check ownership: %w", err)
	}
	if !isOwner {
		return domain.ErrUnauthorized
	}

	// Don't allow deletion of system playlists
	playlist, err := s.playlistRepo.GetByID(ctx, playlistID)
	if err != nil {
		return err
	}
	if playlist.IsWatchLater {
		return fmt.Errorf("cannot delete system playlist")
	}

	if err := s.playlistRepo.Delete(ctx, playlistID); err != nil {
		return fmt.Errorf("failed to delete playlist: %w", err)
	}

	return nil
}

// ListPlaylists lists playlists with filtering
func (s *PlaylistService) ListPlaylists(ctx context.Context, opts domain.PlaylistListOptions) (*domain.PlaylistListResponse, error) {
	// Set default limits
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 20
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	playlists, total, err := s.playlistRepo.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list playlists: %w", err)
	}

	return &domain.PlaylistListResponse{
		Playlists: playlists,
		Total:     total,
		Limit:     opts.Limit,
		Offset:    opts.Offset,
	}, nil
}

// AddVideoToPlaylist adds a video to a playlist
func (s *PlaylistService) AddVideoToPlaylist(ctx context.Context, userID, playlistID, videoID uuid.UUID, position *int) error {
	// Check ownership
	isOwner, err := s.playlistRepo.IsOwner(ctx, playlistID, userID)
	if err != nil {
		return fmt.Errorf("failed to check ownership: %w", err)
	}
	if !isOwner {
		return domain.ErrUnauthorized
	}

	// Verify video exists
	if _, err := s.videoRepo.GetByID(ctx, videoID.String()); err != nil {
		return fmt.Errorf("video not found: %w", err)
	}

	if err := s.playlistRepo.AddItem(ctx, playlistID, videoID, position); err != nil {
		return fmt.Errorf("failed to add video to playlist: %w", err)
	}

	return nil
}

// RemoveVideoFromPlaylist removes a video from a playlist
func (s *PlaylistService) RemoveVideoFromPlaylist(ctx context.Context, userID, playlistID, itemID uuid.UUID) error {
	// Check ownership
	isOwner, err := s.playlistRepo.IsOwner(ctx, playlistID, userID)
	if err != nil {
		return fmt.Errorf("failed to check ownership: %w", err)
	}
	if !isOwner {
		return domain.ErrUnauthorized
	}

	if err := s.playlistRepo.RemoveItem(ctx, playlistID, itemID); err != nil {
		return fmt.Errorf("failed to remove video from playlist: %w", err)
	}

	return nil
}

// GetPlaylistItems retrieves items from a playlist
func (s *PlaylistService) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID, limit, offset int) ([]*domain.PlaylistItem, error) {
	// Check privacy permissions
	playlist, err := s.playlistRepo.GetByID(ctx, playlistID)
	if err != nil {
		return nil, err
	}

	if playlist.Privacy == domain.PrivacyPrivate && (userID == nil || *userID != playlist.UserID) {
		return nil, domain.ErrUnauthorized
	}

	// Set default limits
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	items, err := s.playlistRepo.GetItems(ctx, playlistID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist items: %w", err)
	}

	return items, nil
}

// ReorderPlaylistItem changes the position of an item in a playlist
func (s *PlaylistService) ReorderPlaylistItem(ctx context.Context, userID, playlistID, itemID uuid.UUID, newPosition int) error {
	// Check ownership
	isOwner, err := s.playlistRepo.IsOwner(ctx, playlistID, userID)
	if err != nil {
		return fmt.Errorf("failed to check ownership: %w", err)
	}
	if !isOwner {
		return domain.ErrUnauthorized
	}

	if err := s.playlistRepo.ReorderItem(ctx, playlistID, itemID, newPosition); err != nil {
		return fmt.Errorf("failed to reorder playlist item: %w", err)
	}

	return nil
}

// GetOrCreateWatchLater gets or creates the user's Watch Later playlist
func (s *PlaylistService) GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error) {
	playlist, err := s.playlistRepo.GetOrCreateWatchLater(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get/create watch later playlist: %w", err)
	}
	return playlist, nil
}

// AddToWatchLater adds a video to the user's Watch Later playlist
func (s *PlaylistService) AddToWatchLater(ctx context.Context, userID, videoID uuid.UUID) error {
	// Get or create Watch Later playlist
	playlist, err := s.GetOrCreateWatchLater(ctx, userID)
	if err != nil {
		return err
	}

	// Add video to the playlist
	return s.AddVideoToPlaylist(ctx, userID, playlist.ID, videoID, nil)
}
