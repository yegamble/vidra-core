package shared

import (
	"time"

	redis "github.com/redis/go-redis/v9"

	"athena/internal/email"
	"athena/internal/httpapi/handlers/payments"
	"athena/internal/livestream"
	"athena/internal/port"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
	ucbackup "athena/internal/usecase/backup"
	ucchannel "athena/internal/usecase/channel"
	uccmt "athena/internal/usecase/comment"
	"athena/internal/usecase/encoding"
	ucipfs "athena/internal/usecase/ipfs_streaming"
	ucn "athena/internal/usecase/notification"
	ucrt "athena/internal/usecase/rating"
	ucup "athena/internal/usecase/upload"
	ucviews "athena/internal/usecase/views"
)

type HandlerDependencies struct {
	UserRepo              usecase.UserRepository
	VideoRepo             usecase.VideoRepository
	UploadRepo            usecase.UploadRepository
	EncodingRepo          usecase.EncodingRepository
	MessageRepo           usecase.MessageRepository
	AuthRepo              usecase.AuthRepository
	OAuthRepo             usecase.OAuthRepository
	SubRepo               usecase.SubscriptionRepository
	ViewsRepo             *repository.ViewsRepository
	NotificationRepo      *repository.NotificationRepository
	ChannelRepo           *repository.ChannelRepository
	CommentRepo           usecase.CommentRepository
	RatingRepo            usecase.RatingRepository
	PlaylistRepo          usecase.PlaylistRepository
	CaptionRepo           usecase.CaptionRepository
	ModerationRepo        *repository.ModerationRepository
	FederationRepo        *repository.FederationRepository
	HardeningRepo         *repository.FederationHardeningRepository
	ActivityPubRepo       *repository.ActivityPubRepository
	SessionRepo           usecase.AuthRepository
	LiveStreamRepo        repository.LiveStreamRepository
	StreamKeyRepo         repository.StreamKeyRepository
	ViewerSessionRepo     repository.ViewerSessionRepository
	EmailVerificationRepo usecase.EmailVerificationRepository

	UploadService            ucup.Service
	EmailService             email.EmailService
	EmailVerificationService *usecase.EmailVerificationService
	MessageService           *usecase.MessageService
	ViewsService             *ucviews.Service
	NotificationService      ucn.Service
	ChannelService           *ucchannel.Service
	CommentService           *uccmt.Service
	RatingService            *ucrt.Service
	PlaylistService          *usecase.PlaylistService
	CaptionService           *usecase.CaptionService
	TwoFAService             *usecase.TwoFAService
	PaymentService           payments.PaymentService
	AtprotoService           usecase.AtprotoPublisher
	FederationService        usecase.FederationService
	HardeningService         *usecase.FederationHardeningService
	EncodingService          encoding.Service
	ActivityPubService       port.ActivityPubService
	ImportService            any
	StreamManager            *livestream.StreamManager
	HLSTranscoder            *livestream.HLSTranscoder
	IPFSStreamingService     *ucipfs.Service
	BackupService            *ucbackup.Service

	EncodingScheduler *scheduler.EncodingScheduler

	Redis            *redis.Client
	JWTSecret        string
	RedisPingTimeout time.Duration
	IPFSApi          string
	IPFSCluster      string
	IPFSPingTimeout  time.Duration
}
