package channel

import (
	"context"
	"fmt"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/google/uuid"
)

type Service struct {
	channelRepo port.ChannelRepository
	userRepo    port.UserRepository
	videoRepo   port.VideoRepository
}

func NewService(channelRepo port.ChannelRepository, userRepo port.UserRepository, videoRepo port.VideoRepository) *Service {
	return &Service{
		channelRepo: channelRepo,
		userRepo:    userRepo,
		videoRepo:   videoRepo,
	}
}

func (s *Service) CreateChannel(ctx context.Context, userID uuid.UUID, req domain.ChannelCreateRequest) (*domain.Channel, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	channel := &domain.Channel{
		AccountID:   userID,
		Handle:      req.Handle,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Support:     req.Support,
	}

	if err := s.channelRepo.Create(ctx, channel); err != nil {
		return nil, err
	}

	channel.Account = user

	return channel, nil
}

func (s *Service) GetChannel(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	return s.channelRepo.GetByID(ctx, id)
}

func (s *Service) GetChannelByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	return s.channelRepo.GetByHandle(ctx, handle)
}

func (s *Service) ListChannels(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	return s.channelRepo.List(ctx, params)
}

func (s *Service) UpdateChannel(ctx context.Context, userID, channelID uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	if err := updates.Validate(); err != nil {
		return nil, err
	}

	isOwner, err := s.channelRepo.CheckOwnership(ctx, channelID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check channel ownership: %w", err)
	}
	if !isOwner {
		return nil, domain.ErrUnauthorized
	}

	return s.channelRepo.Update(ctx, channelID, updates)
}

func (s *Service) DeleteChannel(ctx context.Context, userID, channelID uuid.UUID) error {
	isOwner, err := s.channelRepo.CheckOwnership(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check channel ownership: %w", err)
	}
	if !isOwner {
		return domain.ErrUnauthorized
	}

	channels, err := s.channelRepo.GetChannelsByAccountID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user channels: %w", err)
	}

	if len(channels) <= 1 {
		return fmt.Errorf("cannot delete the last channel")
	}

	return s.channelRepo.Delete(ctx, channelID)
}

func (s *Service) GetUserChannels(ctx context.Context, userID uuid.UUID) ([]domain.Channel, error) {
	return s.channelRepo.GetChannelsByAccountID(ctx, userID)
}

func (s *Service) GetChannelVideos(ctx context.Context, channelID uuid.UUID, page, pageSize int) (*domain.VideoListResponse, error) {
	if s.videoRepo == nil {
		return &domain.VideoListResponse{Total: 0, Page: page, PageSize: pageSize, Data: []domain.Video{}}, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	videos, total, err := s.videoRepo.GetByChannelID(ctx, channelID.String(), pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("getting channel videos: %w", err)
	}
	result := make([]domain.Video, 0, len(videos))
	for _, v := range videos {
		if v != nil {
			result = append(result, *v)
		}
	}
	return &domain.VideoListResponse{
		Total:    int(total),
		Page:     page,
		PageSize: pageSize,
		Data:     result,
	}, nil
}

func (s *Service) EnsureDefaultChannel(ctx context.Context, userID uuid.UUID) (*domain.Channel, error) {
	channel, err := s.channelRepo.GetDefaultChannelForAccount(ctx, userID)
	if err == nil {
		return channel, nil
	}

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
