package shared

import (
	"database/sql"
	"time"

	redis "github.com/redis/go-redis/v9"

	"athena/internal/chat"
	"athena/internal/email"
	"athena/internal/livestream"
	"athena/internal/plugin"
	"athena/internal/port"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
	ucbackup "athena/internal/usecase/backup"
	"athena/internal/usecase/captiongen"
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
	NotificationPrefRepo  port.NotificationPreferenceRepository
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
	PasswordResetRepo     repository.PasswordResetRepository
	BlacklistRepo         repository.BlacklistRepository
	ChapterRepo           repository.ChapterRepository
	TokenSessionRepo      port.TokenSessionRepository
	ServerFollowingRepo   port.ServerFollowingRepository
	UserBlockRepo         *repository.UserBlockRepository
	AbuseMessageRepo      *repository.AbuseMessageRepository
	LiveStreamSessionRepo *repository.LiveStreamSessionRepository
	OwnershipRepo         port.VideoOwnershipRepository
	CollaboratorRepo      *repository.ChannelCollaboratorRepository
	RunnerRepo            *repository.RunnerRepository

	UploadService            ucup.Service
	EmailService             email.EmailService
	EmailVerificationService *usecase.EmailVerificationService
	MessageService           *usecase.MessageService
	E2EEService              *usecase.E2EEService
	ViewsService             *ucviews.Service
	NotificationService      ucn.Service
	ChannelService           *ucchannel.Service
	CommentService           *uccmt.Service
	RatingService            *ucrt.Service
	PlaylistService          *usecase.PlaylistService
	CaptionService           *usecase.CaptionService
	CaptionGenService        captiongen.Service
	TwoFAService             *usecase.TwoFAService
	PaymentService           port.PaymentService
	SocialService            *usecase.SocialService
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

	ChatServer *chat.ChatServer
	ChatRepo   repository.ChatRepository

	RegistrationRepo *repository.RegistrationRepository
	PluginRepo       *repository.PluginRepository
	PluginManager    *plugin.Manager

	RedundancyService any
	InstanceDiscovery any

	VideoCategoryUseCase usecase.VideoCategoryUseCase
	AnalyticsRepo        repository.AnalyticsRepository
	AnalyticsCollector   any

	EncodingScheduler *scheduler.EncodingScheduler

	// New feature repositories (typed as any; handler packages define their own interfaces)
	ArchiveRepo        any // user.ArchiveRepository
	ChannelSyncRepo    any // channel.ChannelSyncRepository
	PlayerSettingsRepo any // player.PlayerSettingsRepository
	LogRepo            any // admin.LogRepository

	DB               *sql.DB
	Redis            *redis.Client
	JWTSecret        string
	RedisPingTimeout time.Duration
	IPFSApi          string
	IPFSCluster      string
	IPFSPingTimeout  time.Duration
}
