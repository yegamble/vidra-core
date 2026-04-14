package shared

import (
	"database/sql"
	"time"

	redis "github.com/redis/go-redis/v9"

	"vidra-core/internal/chat"
	"vidra-core/internal/email"
	"vidra-core/internal/livestream"
	"vidra-core/internal/plugin"
	"vidra-core/internal/port"
	"vidra-core/internal/repository"
	"vidra-core/internal/scheduler"
	"vidra-core/internal/usecase"
	ucat "vidra-core/internal/usecase/auto_tags"
	ucbackup "vidra-core/internal/usecase/backup"
	"vidra-core/internal/usecase/captiongen"
	ucchannel "vidra-core/internal/usecase/channel"
	uccmt "vidra-core/internal/usecase/comment"
	"vidra-core/internal/usecase/encoding"
	ucipfs "vidra-core/internal/usecase/ipfs_streaming"
	ucmigration "vidra-core/internal/usecase/migration_etl"
	ucn "vidra-core/internal/usecase/notification"
	ucpayments "vidra-core/internal/usecase/payments"
	ucrt "vidra-core/internal/usecase/rating"
	ucup "vidra-core/internal/usecase/upload"
	ucviews "vidra-core/internal/usecase/views"
	ucww "vidra-core/internal/usecase/watched_words"
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
	VideoPasswordRepo     port.VideoPasswordRepository
	VideoStoryboardRepo   port.VideoStoryboardRepository
	VideoEmbedRepo        port.VideoEmbedPrivacyRepository

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
	BTCPayService            *ucpayments.BTCPayService
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
	MigrationService         *ucmigration.ETLService

	ChatServer *chat.ChatServer
	ChatRepo   repository.ChatRepository

	RegistrationRepo *repository.RegistrationRepository
	PluginRepo       *repository.PluginRepository
	PluginManager    *plugin.Manager

	WatchedWordsService *ucww.Service
	AutoTagsService     *ucat.Service

	RedundancyService any
	InstanceDiscovery any

	VideoCategoryUseCase usecase.VideoCategoryUseCase
	AnalyticsRepo        repository.AnalyticsRepository
	AnalyticsCollector   any
	ExportService        any // *ucanalytics.ExportService (handler defines its own interface)

	EncodingScheduler *scheduler.EncodingScheduler

	StudioService any // studio.Service (handler defines its own interface)

	// New feature repositories (typed as any; handler packages define their own interfaces)
	ArchiveRepo        any // user.ArchiveRepository
	ChannelSyncRepo    any // channel.ChannelSyncRepository
	PlayerSettingsRepo any // player.PlayerSettingsRepository
	LogRepo            any // admin.LogRepository (deprecated — now file-based)
	AuditLogger        any // *obs.AuditLogger — set by app.go when LOG_DIR is configured

	DB               *sql.DB
	Redis            *redis.Client
	JWTSecret        string
	RedisPingTimeout time.Duration
	IPFSApi          string
	IPFSCluster      string
	IPFSPingTimeout  time.Duration
}
