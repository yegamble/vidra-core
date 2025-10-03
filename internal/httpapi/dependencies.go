package httpapi

import (
	"time"

	redis "github.com/redis/go-redis/v9"

	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
	ucap "athena/internal/usecase/activitypub"
	ucchannel "athena/internal/usecase/channel"
	uccmt "athena/internal/usecase/comment"
	"athena/internal/usecase/encoding"
	ucn "athena/internal/usecase/notification"
	ucrt "athena/internal/usecase/rating"
	ucup "athena/internal/usecase/upload"
	ucviews "athena/internal/usecase/views"
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
	ActivityPubRepo  *repository.ActivityPubRepository
	SessionRepo      usecase.AuthRepository

	// Services
	UploadService       ucup.Service
	MessageService      *usecase.MessageService
	ViewsService        *ucviews.Service
	NotificationService ucn.Service
	ChannelService      *ucchannel.Service
	CommentService      *uccmt.Service
	RatingService       *ucrt.Service
	PlaylistService     *usecase.PlaylistService
	CaptionService      *usecase.CaptionService
	AtprotoService      usecase.AtprotoPublisher
	FederationService   usecase.FederationService
	HardeningService    *usecase.FederationHardeningService
	EncodingService     encoding.Service
	ActivityPubService  *ucap.Service

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
