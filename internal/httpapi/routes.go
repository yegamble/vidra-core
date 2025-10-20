package httpapi

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	redis "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"athena/internal/config"
	"athena/internal/livestream"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
	ucchannel "athena/internal/usecase/channel"
	uccmt "athena/internal/usecase/comment"
	ucenc "athena/internal/usecase/encoding"
	ucn "athena/internal/usecase/notification"
	ucrt "athena/internal/usecase/rating"
	ucup "athena/internal/usecase/upload"
	ucviews "athena/internal/usecase/views"
)

// RegisterRoutes maintains backward compatibility by creating all dependencies internally
// and delegating to RegisterRoutesWithDependencies.
// DEPRECATED: This creates dependencies internally. Use app.New() for better separation of concerns.
func RegisterRoutes(r chi.Router, cfg *config.Config) {
	// Initialize database
	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		panic(fmt.Errorf("failed to connect to database: %w", err))
	}

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	videoRepo := repository.NewVideoRepository(db)
	uploadRepo := repository.NewUploadRepository(db)
	encodingRepo := repository.NewEncodingRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	dbAuthRepo := repository.NewAuthRepository(db)
	oauthRepo := repository.NewOAuthRepository(db)
	subRepo := repository.NewSubscriptionRepository(db)
	viewsRepo := repository.NewViewsRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	channelRepo := repository.NewChannelRepository(db)
	commentRepo := repository.NewCommentRepository(db)
	ratingRepo := repository.NewRatingRepository(db)
	playlistRepo := repository.NewPlaylistRepository(db)
	captionRepo := repository.NewCaptionRepository(db)
	moderationRepo := repository.NewModerationRepository(db)
	federationRepo := repository.NewFederationRepository(db)
	hardeningRepo := repository.NewFederationHardeningRepository(db)
	liveStreamRepo := repository.NewLiveStreamRepository(db)
	streamKeyRepo := repository.NewStreamKeyRepository(db)
	viewerSessionRepo := repository.NewViewerSessionRepository(db)

	// Create storage directory structure
	storageRoot := cfg.StorageDir
	storageDirs := []string{
		filepath.Join(storageRoot, "avatars"),
		filepath.Join(storageRoot, "cache"),
		filepath.Join(storageRoot, "captions"),
		filepath.Join(storageRoot, "logs"),
		filepath.Join(storageRoot, "previews"),
		filepath.Join(storageRoot, "streaming-playlists", "hls"),
		filepath.Join(storageRoot, "thumbnails"),
		filepath.Join(storageRoot, "torrents"),
		filepath.Join(storageRoot, "web-videos"),
		filepath.Join(storageRoot, "storyboards"),
	}
	for _, d := range storageDirs {
		if err := os.MkdirAll(d, 0750); err != nil {
			panic(fmt.Errorf("failed to create storage dir %s: %w", d, err))
		}
	}

	// Initialize services
	uploadService := ucup.NewService(uploadRepo, encodingRepo, videoRepo, storageRoot, cfg)
	messageService := usecase.NewMessageService(messageRepo, userRepo)
	viewsService := ucviews.NewService(viewsRepo, videoRepo)
	notificationService := ucn.NewService(notificationRepo, subRepo, userRepo)
	channelService := ucchannel.NewService(channelRepo, userRepo)
	commentService := uccmt.NewService(commentRepo, videoRepo, userRepo, channelRepo)
	ratingService := ucrt.NewService(ratingRepo, videoRepo)
	playlistService := usecase.NewPlaylistService(playlistRepo, videoRepo)
	captionService := usecase.NewCaptionService(captionRepo, videoRepo, cfg)

	// Initialize shared ATProto publisher (with session persistence + background refresh)
	var atprotoSvc usecase.AtprotoPublisher
	if cfg.EnableATProto {
		var encKey []byte
		if cfg.ATProtoTokenKey != "" {
			if k, err := repository.DecodeTokenKey(cfg.ATProtoTokenKey); err == nil {
				encKey = k
			}
		}
		atprotoSvc = usecase.NewAtprotoService(moderationRepo, cfg, repository.NewAtprotoRepository(db), encKey)
		// Start background refresh based on configuration
		atprotoSvc.StartBackgroundRefresh(context.Background(), time.Duration(cfg.ATProtoRefreshIntervalSeconds)*time.Second)
	}

	// Prepare a lightweight encoding scheduler; lifecycle should be owned by bootstrap layer.
	var encSched *scheduler.EncodingScheduler
	if cfg.EnableEncodingScheduler {
		encSvc := ucenc.NewService(encodingRepo, videoRepo, notificationService, storageRoot, cfg, atprotoSvc, federationRepo)
		interval := time.Duration(cfg.EncodingSchedulerIntervalSeconds) * time.Second
		burst := cfg.EncodingSchedulerBurst
		encSched = scheduler.NewEncodingScheduler(encSvc, interval, burst)
	}

	// Build federation service; lifecycle should be owned by bootstrap layer.
	var fedSvc usecase.FederationService
	if cfg.EnableATProto {
		fedSvc = usecase.NewFederationService(federationRepo, moderationRepo, atprotoSvc, cfg, hardeningRepo)
	}

	// Federation hardening service and admin routes
	var hardeningSvc *usecase.FederationHardeningService
	{
		// Always create; internal handlers will enforce auth/role where needed
		hardeningSvc = usecase.NewFederationHardeningService(hardeningRepo, fedSvc, cfg)
		_ = hardeningSvc.Initialize(context.Background())
	}

	// Initialize Redis session repo
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		panic(fmt.Errorf("failed to parse redis url: %w", err))
	}
	rdb := redis.NewClient(redisOpts)
	// Fail fast if Redis is unreachable
	if err := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.RedisPingTimeout)*time.Second)
		defer cancel()
		return rdb.Ping(ctx).Err()
	}(); err != nil {
		panic(fmt.Errorf("failed to connect to redis: %w", err))
	}
	sessionRepo := repository.NewCompositeAuthRepository(dbAuthRepo, repository.NewRedisSessionRepository(rdb))

	// Initialize StreamManager and HLS transcoder for live streaming (optional based on config)
	var streamManager *livestream.StreamManager
	var hlsTranscoder *livestream.HLSTranscoder
	if cfg.EnableLiveStreaming {
		log.Printf("INFO: Initializing StreamManager for live streaming...")
		// Create a logger for the StreamManager
		logger := logrus.New()
		logger.SetLevel(logrus.InfoLevel)
		if lvl := strings.ToLower(cfg.LogLevel); lvl == "debug" || lvl == "trace" {
			logger.SetLevel(logrus.DebugLevel)
		}
		streamManager = livestream.NewStreamManager(liveStreamRepo, viewerSessionRepo, rdb, logger)

		// Initialize HLS transcoder
		log.Printf("INFO: Initializing HLS transcoder...")
		hlsTranscoder = livestream.NewHLSTranscoder(cfg, liveStreamRepo, logger)

		// Note: VOD converter is initialized in app.go where RTMP server is managed
	}

	// Fail fast if IPFS API is unreachable
	{
		client := &http.Client{Timeout: time.Duration(cfg.IPFSPingTimeout) * time.Second}
		resp, err := client.Post(cfg.IPFSApi+"/api/v0/version", "", nil)
		if err != nil || (resp != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300)) {
			if cfg.RequireIPFS {
				log.Printf("ERROR: Failed to connect to IPFS API at %s: %v", cfg.IPFSApi, err)
				panic(fmt.Errorf("failed to connect to ipfs api at %s: %w", cfg.IPFSApi, err))
			}
			log.Printf("WARNING: IPFS API not reachable at %s: %v (continuing as REQUIRE_IPFS=false)", cfg.IPFSApi, err)
		} else {
			log.Printf("INFO: Successfully connected to IPFS API at %s", cfg.IPFSApi)
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
	}

	// Create dependencies structure
	deps := &HandlerDependencies{
		UserRepo:          userRepo,
		VideoRepo:         videoRepo,
		UploadRepo:        uploadRepo,
		EncodingRepo:      encodingRepo,
		MessageRepo:       messageRepo,
		AuthRepo:          dbAuthRepo,
		OAuthRepo:         oauthRepo,
		SubRepo:           subRepo,
		ViewsRepo:         viewsRepo,
		NotificationRepo:  notificationRepo,
		ChannelRepo:       channelRepo,
		CommentRepo:       commentRepo,
		RatingRepo:        ratingRepo,
		PlaylistRepo:      playlistRepo,
		CaptionRepo:       captionRepo,
		ModerationRepo:    moderationRepo,
		FederationRepo:    federationRepo,
		HardeningRepo:     hardeningRepo,
		SessionRepo:       sessionRepo,
		LiveStreamRepo:    liveStreamRepo,
		StreamKeyRepo:     streamKeyRepo,
		ViewerSessionRepo: viewerSessionRepo,

		UploadService:       uploadService,
		MessageService:      messageService,
		ViewsService:        viewsService,
		NotificationService: notificationService,
		ChannelService:      channelService,
		CommentService:      commentService,
		RatingService:       ratingService,
		PlaylistService:     playlistService,
		CaptionService:      captionService,
		AtprotoService:      atprotoSvc,
		FederationService:   fedSvc,
		HardeningService:    hardeningSvc,
		EncodingService:     nil, // Will be set if needed
		StreamManager:       streamManager,
		HLSTranscoder:       hlsTranscoder,

		EncodingScheduler: encSched,

		Redis:            rdb,
		JWTSecret:        cfg.JWTSecret,
		RedisPingTimeout: time.Duration(cfg.RedisPingTimeout) * time.Second,
		IPFSApi:          cfg.IPFSApi,
		IPFSCluster:      cfg.IPFSCluster,
		IPFSPingTimeout:  time.Duration(cfg.IPFSPingTimeout) * time.Second,
	}

	// Register routes with the dependencies
	RegisterRoutesWithDependencies(r, cfg, deps)
}
