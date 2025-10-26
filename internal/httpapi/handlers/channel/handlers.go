package channel

import (
	"athena/internal/config"
	"athena/internal/repository"
	"athena/internal/usecase"
	"athena/internal/usecase/channel"
	"athena/internal/usecase/notification"
)

type ChannelHandlers struct {
	channelRepo         *repository.ChannelRepository
	channelService      *channel.Service
	subRepo             usecase.SubscriptionRepository
	notificationService notification.Service
	videoRepo           usecase.VideoRepository
	cfg                 *config.Config
}

func NewChannelHandlers(
	channelRepo *repository.ChannelRepository,
	channelService *channel.Service,
	subRepo usecase.SubscriptionRepository,
	notificationService notification.Service,
	videoRepo usecase.VideoRepository,
	cfg *config.Config,
) *ChannelHandlers {
	return &ChannelHandlers{
		channelRepo:         channelRepo,
		channelService:      channelService,
		subRepo:             subRepo,
		notificationService: notificationService,
		videoRepo:           videoRepo,
		cfg:                 cfg,
	}
}
