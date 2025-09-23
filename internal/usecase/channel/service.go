package channel

import (
    "context"
    "fmt"

    "athena/internal/domain"
    "athena/internal/port"
    "github.com/google/uuid"
)

// Service handles channel business logic
type Service struct {
    channelRepo port.ChannelRepository
    userRepo    port.UserRepository
}

// NewService creates a new channel service
func NewService(channelRepo port.ChannelRepository, userRepo port.UserRepository) *Service {
    return &Service{
        channelRepo: channelRepo,
        userRepo:    userRepo,
    }
}

// CreateChannel creates a new channel for a user
func (s *Service) CreateChannel(ctx context.Context, userID uuid.UUID, req domain.ChannelCreateRequest) (*domain.Channel, error) {
    // Validate request
    if err := req.Validate(); err != nil {
        return nil, err
    }

    // Check if user exists (convert UUID to string for user repo)
    user, err := s.userRepo.GetByID(ctx, userID.String())
    if err != nil {
        return nil, fmt.Errorf("failed to get user: %w", err)
    }

    // Create the channel
    channel := &domain.Channel{
        AccountID:   userID, // Use the UUID parameter directly
        Handle:      req.Handle,
        DisplayName: req.DisplayName,
        Description: req.Description,
        Support:     req.Support,
    }

    if err := s.channelRepo.Create(ctx, channel); err != nil {
        return nil, err
    }

    // Set the account information
    channel.Account = user

    return channel, nil
}

// GetChannel retrieves a channel by ID
func (s *Service) GetChannel(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
    return s.channelRepo.GetByID(ctx, id)
}

// GetChannelByHandle retrieves a channel by handle
func (s *Service) GetChannelByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
    return s.channelRepo.GetByHandle(ctx, handle)
}

// ListChannels retrieves a paginated list of channels
func (s *Service) ListChannels(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
    return s.channelRepo.List(ctx, params)
}

// UpdateChannel updates a channel
func (s *Service) UpdateChannel(ctx context.Context, userID, channelID uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
    // Validate request
    if err := updates.Validate(); err != nil {
        return nil, err
    }

    // Check if the user owns the channel
    isOwner, err := s.channelRepo.CheckOwnership(ctx, channelID, userID)
    if err != nil {
        return nil, fmt.Errorf("failed to check channel ownership: %w", err)
    }
    if !isOwner {
        return nil, domain.ErrUnauthorized
    }

    // Update the channel
    return s.channelRepo.Update(ctx, channelID, updates)
}

// DeleteChannel deletes a channel
func (s *Service) DeleteChannel(ctx context.Context, userID, channelID uuid.UUID) error {
    // Check if the user owns the channel
    isOwner, err := s.channelRepo.CheckOwnership(ctx, channelID, userID)
    if err != nil {
        return fmt.Errorf("failed to check channel ownership: %w", err)
    }
    if !isOwner {
        return domain.ErrUnauthorized
    }

    // Check if this is the user's last channel
    channels, err := s.channelRepo.GetChannelsByAccountID(ctx, userID)
    if err != nil {
        return fmt.Errorf("failed to get user channels: %w", err)
    }

    if len(channels) <= 1 {
        return fmt.Errorf("cannot delete the last channel")
    }

    // Delete the channel
    return s.channelRepo.Delete(ctx, channelID)
}

// GetUserChannels retrieves all channels for a user
func (s *Service) GetUserChannels(ctx context.Context, userID uuid.UUID) ([]domain.Channel, error) {
    return s.channelRepo.GetChannelsByAccountID(ctx, userID)
}

// GetChannelVideos retrieves videos for a channel
func (s *Service) GetChannelVideos(ctx context.Context, channelID uuid.UUID, page, pageSize int) (*domain.VideoListResponse, error) {
    // This would need to be implemented in the video repository
    // For now, returning a placeholder
    return &domain.VideoListResponse{
        Total:    0,
        Page:     page,
        PageSize: pageSize,
        Data:     []domain.Video{},
    }, nil
}

// EnsureDefaultChannel ensures a user has at least one channel
func (s *Service) EnsureDefaultChannel(ctx context.Context, userID uuid.UUID) (*domain.Channel, error) {
    // Check if user already has a channel
    channel, err := s.channelRepo.GetDefaultChannelForAccount(ctx, userID)
    if err == nil {
        return channel, nil
    }

    // If no channel exists, create a default one
    if err == domain.ErrNotFound {
        user, err := s.userRepo.GetByID(ctx, userID.String())
        if err != nil {
            return nil, fmt.Errorf("failed to get user: %w", err)
        }

        displayName := user.DisplayName
        if displayName == "" {
            displayName = user.Username
        }

        req := domain.ChannelCreateRequest{
            Handle:      user.Username + "_channel",
            DisplayName: displayName + "'s Channel",
            Description: &user.Bio,
        }

        return s.CreateChannel(ctx, userID, req)
    }

    return nil, err
}

