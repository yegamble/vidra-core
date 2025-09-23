package httpapi

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	redis "github.com/redis/go-redis/v9"

	"athena/internal/config"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
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
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, storageRoot, cfg)
	messageService := usecase.NewMessageService(messageRepo, userRepo)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	notificationService := usecase.NewNotificationService(notificationRepo, subRepo, userRepo)
	channelService := usecase.NewChannelService(channelRepo, userRepo)
	commentService := usecase.NewCommentService(commentRepo, videoRepo, userRepo, channelRepo)
	ratingService := usecase.NewRatingService(ratingRepo, videoRepo)
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

	// Start a lightweight encoding scheduler in the background to ensure
	// pending jobs are processed even if the standalone encoder is not running.
	// This uses a short interval with a small burst to avoid starvation.
	var encSched *scheduler.EncodingScheduler
	if cfg.EnableEncodingScheduler {
		encSvc := usecase.NewEncodingService(encodingRepo, videoRepo, notificationService, storageRoot, cfg, atprotoSvc, federationRepo)
		interval := time.Duration(cfg.EncodingSchedulerIntervalSeconds) * time.Second
		burst := cfg.EncodingSchedulerBurst
		encSched = scheduler.NewEncodingScheduler(encSvc, interval, burst)
		// Use Background context; lifecycle is tied to the server process.
		go encSched.Start(context.Background())
	}

	// Start federation scheduler (ingestion + publish retries)
	var fedSvc usecase.FederationService
	if cfg.EnableATProto {
		fedSvc = usecase.NewFederationService(federationRepo, moderationRepo, atprotoSvc, cfg, hardeningRepo)
		if cfg.EnableFederationScheduler {
			fInterval := time.Duration(cfg.FederationSchedulerIntervalSeconds) * time.Second
			fBurst := cfg.FederationSchedulerBurst
			go scheduler.NewFederationScheduler(fedSvc, fInterval, fBurst).Start(context.Background())
		}
		// Optional near real-time firehose-style poller using the same ingestion path
		if cfg.EnableATProtoFirehose {
			fhInterval := time.Duration(cfg.ATProtoFirehosePollIntervalSeconds) * time.Second
			// Use a modest burst to pull several pages quickly
			go scheduler.NewFirehosePoller(fedSvc, fhInterval, 3).Start(context.Background())
		}
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
		UserRepo:         userRepo,
		VideoRepo:        videoRepo,
		UploadRepo:       uploadRepo,
		EncodingRepo:     encodingRepo,
		MessageRepo:      messageRepo,
		AuthRepo:         dbAuthRepo,
		OAuthRepo:        oauthRepo,
		SubRepo:          subRepo,
		ViewsRepo:        viewsRepo,
		NotificationRepo: notificationRepo,
		ChannelRepo:      channelRepo,
		CommentRepo:      commentRepo,
		RatingRepo:       ratingRepo,
		PlaylistRepo:     playlistRepo,
		CaptionRepo:      captionRepo,
		ModerationRepo:   moderationRepo,
		FederationRepo:   federationRepo,
		HardeningRepo:    hardeningRepo,
		SessionRepo:      sessionRepo,

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
