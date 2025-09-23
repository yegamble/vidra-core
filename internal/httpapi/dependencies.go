package httpapi

import (
	"time"

	redis "github.com/redis/go-redis/v9"

	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
)

type HandlerDependencies struct {
	// Repositories
	UserRepo         usecase.UserRepository
	VideoRepo        usecase.VideoRepository
	UploadRepo       usecase.UploadRepository
	EncodingRepo     usecase.EncodingRepository
	MessageRepo      usecase.MessageRepository
	AuthRepo         usecase.AuthRepository
	OAuthRepo        usecase.OAuthRepository
	SubRepo          usecase.SubscriptionRepository
	ViewsRepo        *repository.ViewsRepository
	NotificationRepo *repository.NotificationRepository
	ChannelRepo      *repository.ChannelRepository
	CommentRepo      usecase.CommentRepository
	RatingRepo       usecase.RatingRepository
	PlaylistRepo     usecase.PlaylistRepository
	CaptionRepo      usecase.CaptionRepository
	ModerationRepo   *repository.ModerationRepository
	FederationRepo   *repository.FederationRepository
	HardeningRepo    *repository.FederationHardeningRepository
	SessionRepo      usecase.AuthRepository

	// Services
	UploadService       usecase.UploadService
	MessageService      *usecase.MessageService
	ViewsService        *usecase.ViewsService
	NotificationService usecase.NotificationService
	ChannelService      *usecase.ChannelService
	CommentService      *usecase.CommentService
	RatingService       *usecase.RatingService
	PlaylistService     *usecase.PlaylistService
	CaptionService      *usecase.CaptionService
	AtprotoService      usecase.AtprotoPublisher
	FederationService   usecase.FederationService
	HardeningService    *usecase.FederationHardeningService
	EncodingService     usecase.EncodingService

	// Schedulers
	EncodingScheduler *scheduler.EncodingScheduler

	// Infrastructure
	Redis            *redis.Client
	JWTSecret        string
	RedisPingTimeout time.Duration
	IPFSApi          string
	IPFSCluster      string
	IPFSPingTimeout  time.Duration
}
