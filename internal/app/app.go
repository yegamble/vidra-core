package app

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
	"github.com/sirupsen/logrus"

	"athena/internal/config"
	"athena/internal/database"
	"athena/internal/httpapi"
	"athena/internal/httpapi/shared"
	"athena/internal/ipfs"
	"athena/internal/livestream"
	"athena/internal/metrics"
	"athena/internal/middleware"
	"athena/internal/payments"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/security"
	"athena/internal/usecase"
	ucactivitypub "athena/internal/usecase/activitypub"
	ucchannel "athena/internal/usecase/channel"
	uccmt "athena/internal/usecase/comment"
	ucenc "athena/internal/usecase/encoding"
	ucipfs "athena/internal/usecase/ipfs_streaming"
	ucn "athena/internal/usecase/notification"
	ucpayments "athena/internal/usecase/payments"
	ucrt "athena/internal/usecase/rating"
	ucup "athena/internal/usecase/upload"
	ucviews "athena/internal/usecase/views"
)

type Application struct {
	Config              *config.Config
	DB                  *sqlx.DB
	Redis               *redis.Client
	Router              chi.Router
	Dependencies        *Dependencies
	schedulers          []scheduler.Scheduler
	atprotoService      usecase.AtprotoPublisher
	encodingScheduler   *scheduler.EncodingScheduler
	federationScheduler *scheduler.FederationScheduler
	firehosePoller      *scheduler.FirehosePoller

	// lifecycle-managed components
	metricsServer      *http.Server
	rtmpServer         *livestream.RTMPServer
	hlsTranscoder      *livestream.HLSTranscoder
	vodConverter       *livestream.VODConverter
	rateLimiterManager *middleware.RateLimiterManager
}

type Dependencies struct {
	UserRepo          usecase.UserRepository
	VideoRepo         usecase.VideoRepository
	UploadRepo        usecase.UploadRepository
	EncodingRepo      usecase.EncodingRepository
	MessageRepo       usecase.MessageRepository
	AuthRepo          usecase.AuthRepository
	OAuthRepo         usecase.OAuthRepository
	SubRepo           usecase.SubscriptionRepository
	ViewsRepo         *repository.ViewsRepository
	NotificationRepo  *repository.NotificationRepository
	ChannelRepo       *repository.ChannelRepository
	CommentRepo       usecase.CommentRepository
	RatingRepo        usecase.RatingRepository
	PlaylistRepo      usecase.PlaylistRepository
	CaptionRepo       usecase.CaptionRepository
	ModerationRepo    *repository.ModerationRepository
	FederationRepo    *repository.FederationRepository
	HardeningRepo     *repository.FederationHardeningRepository
	ImportRepo        *repository.ImportRepository
	SessionRepo       usecase.AuthRepository
	LiveStreamRepo    repository.LiveStreamRepository
	StreamKeyRepo     repository.StreamKeyRepository
	ViewerSessionRepo repository.ViewerSessionRepository
	IOTARepo          *repository.IOTARepository

	UploadService        ucup.Service
	MessageService       *usecase.MessageService
	ViewsService         *ucviews.Service
	NotificationService  ucn.Service
	ChannelService       *ucchannel.Service
	CommentService       *uccmt.Service
	RatingService        *ucrt.Service
	PlaylistService      *usecase.PlaylistService
	CaptionService       *usecase.CaptionService
	ActivityPubService   *ucactivitypub.Service
	AtprotoService       usecase.AtprotoPublisher
	FederationService    usecase.FederationService
	HardeningService     *usecase.FederationHardeningService
	EncodingService      ucenc.Service
	ImportService        any // ucimport.Service
	PaymentService       *ucpayments.PaymentService
	StreamManager        *livestream.StreamManager
	IPFSStreamingService *ucipfs.Service
	IPFSClient           *ipfs.Client
}

func New(cfg *config.Config) (*Application, error) {
	app := &Application{
		Config:             cfg,
		Router:             chi.NewRouter(),
		schedulers:         []scheduler.Scheduler{},
		rateLimiterManager: middleware.NewRateLimiterManager(),
	}

	if err := app.initializeDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	if err := app.initializeRedis(); err != nil {
		return nil, fmt.Errorf("failed to initialize redis: %w", err)
	}

	if err := app.initializeStorageDirectories(); err != nil {
		return nil, fmt.Errorf("failed to initialize storage directories: %w", err)
	}

	if err := app.verifyIPFSConnection(); err != nil {
		return nil, fmt.Errorf("failed to verify IPFS connection: %w", err)
	}

	deps := app.initializeDependencies()
	app.Dependencies = deps
	app.initializeSchedulers(deps)
	app.registerRoutes(deps)

	return app, nil
}

func (app *Application) initializeDatabase() error {
	db, err := sqlx.Connect("postgres", app.Config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool per CLAUDE.md
	pool, err := database.NewPool(db, database.DefaultPoolConfig())
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			log.Printf("failed to close DB after pool init error: %v", cerr)
		}
		return fmt.Errorf("failed to configure connection pool: %w", err)
	}

	app.DB = pool.GetDB() // Maintain compatibility
	return nil
}

func (app *Application) initializeRedis() error {
	redisOpts, err := redis.ParseURL(app.Config.RedisURL)
	if err != nil {
		return fmt.Errorf("failed to parse redis url: %w", err)
	}
	rdb := redis.NewClient(redisOpts)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(app.Config.RedisPingTimeout)*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to connect to redis: %w", err)
	}

	app.Redis = rdb
	return nil
}

func (app *Application) initializeStorageDirectories() error {
	storageRoot := app.Config.StorageDir
	storageDirs := []string{
		filepath.Join(storageRoot, "avatars"),
		filepath.Join(storageRoot, "cache"),
		filepath.Join(storageRoot, "captions"),
		filepath.Join(storageRoot, "imports"),
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
			return fmt.Errorf("failed to create storage dir %s: %w", d, err)
		}
	}
	return nil
}

func (app *Application) verifyIPFSConnection() error {
	client := &http.Client{Timeout: time.Duration(app.Config.IPFSPingTimeout) * time.Second}
	resp, err := client.Post(app.Config.IPFSApi+"/api/v0/version", "", nil)
	if err != nil || (resp != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300)) {
		if app.Config.RequireIPFS {
			log.Printf("ERROR: Failed to connect to IPFS API at %s: %v", app.Config.IPFSApi, err)
			return fmt.Errorf("failed to connect to ipfs api at %s: %w", app.Config.IPFSApi, err)
		}
		log.Printf("WARNING: IPFS API not reachable at %s: %v (continuing as REQUIRE_IPFS=false)", app.Config.IPFSApi, err)
		return nil
	}

	log.Printf("INFO: Successfully connected to IPFS API at %s", app.Config.IPFSApi)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return nil
}

func (app *Application) initializeDependencies() *Dependencies {
	deps := &Dependencies{
		UserRepo:          repository.NewUserRepository(app.DB),
		VideoRepo:         repository.NewVideoRepository(app.DB),
		UploadRepo:        repository.NewUploadRepository(app.DB),
		EncodingRepo:      repository.NewEncodingRepository(app.DB),
		MessageRepo:       repository.NewMessageRepository(app.DB),
		AuthRepo:          repository.NewAuthRepository(app.DB),
		OAuthRepo:         repository.NewOAuthRepository(app.DB),
		SubRepo:           repository.NewSubscriptionRepository(app.DB),
		ViewsRepo:         repository.NewViewsRepository(app.DB),
		NotificationRepo:  repository.NewNotificationRepository(app.DB),
		ChannelRepo:       repository.NewChannelRepository(app.DB),
		CommentRepo:       repository.NewCommentRepository(app.DB),
		RatingRepo:        repository.NewRatingRepository(app.DB),
		PlaylistRepo:      repository.NewPlaylistRepository(app.DB),
		CaptionRepo:       repository.NewCaptionRepository(app.DB),
		ModerationRepo:    repository.NewModerationRepository(app.DB),
		FederationRepo:    repository.NewFederationRepository(app.DB),
		HardeningRepo:     repository.NewFederationHardeningRepository(app.DB),
		ImportRepo:        repository.NewImportRepository(app.DB),
		LiveStreamRepo:    repository.NewLiveStreamRepository(app.DB),
		StreamKeyRepo:     repository.NewStreamKeyRepository(app.DB),
		ViewerSessionRepo: repository.NewViewerSessionRepository(app.DB),
	}

	// Initialize IOTA repository if enabled
	if app.Config.EnableIOTA {
		deps.IOTARepo = repository.NewIOTARepository(app.DB)
	}

	redisSessionRepo := repository.NewRedisSessionRepository(app.Redis)
	deps.SessionRepo = repository.NewCompositeAuthRepository(deps.AuthRepo, redisSessionRepo)

	deps.UploadService = ucup.NewService(deps.UploadRepo, deps.EncodingRepo, deps.VideoRepo, app.Config.StorageDir, app.Config)
	deps.MessageService = usecase.NewMessageService(deps.MessageRepo, deps.UserRepo)
	deps.ViewsService = ucviews.NewService(deps.ViewsRepo, deps.VideoRepo)
	deps.NotificationService = ucn.NewService(deps.NotificationRepo, deps.SubRepo, deps.UserRepo)
	deps.ChannelService = ucchannel.NewService(deps.ChannelRepo, deps.UserRepo)
	deps.CommentService = uccmt.NewService(deps.CommentRepo, deps.VideoRepo, deps.UserRepo, deps.ChannelRepo)
	deps.RatingService = ucrt.NewService(deps.RatingRepo, deps.VideoRepo)
	deps.PlaylistService = usecase.NewPlaylistService(deps.PlaylistRepo, deps.VideoRepo)
	deps.CaptionService = usecase.NewCaptionService(deps.CaptionRepo, deps.VideoRepo, app.Config)

	if app.Config.EnableATProto {
		var encKey []byte
		if app.Config.ATProtoTokenKey != "" {
			if k, err := repository.DecodeTokenKey(app.Config.ATProtoTokenKey); err == nil {
				encKey = k
			}
		}
		atprotoRepo := repository.NewAtprotoRepository(app.DB)
		deps.AtprotoService = usecase.NewAtprotoService(deps.ModerationRepo, app.Config, atprotoRepo, encKey)
		deps.AtprotoService.StartBackgroundRefresh(context.Background(), time.Duration(app.Config.ATProtoRefreshIntervalSeconds)*time.Second)
		app.atprotoService = deps.AtprotoService
	}

	// Create IPFS client for video uploads and pinning
	deps.IPFSClient = ipfs.NewClient(
		app.Config.IPFSApi,
		app.Config.IPFSCluster,
		120*time.Second, // Timeout for IPFS operations (longer for large files)
	)

	deps.EncodingService = ucenc.NewService(
		deps.EncodingRepo,
		deps.VideoRepo,
		deps.NotificationService,
		app.Config.StorageDir,
		app.Config,
		deps.AtprotoService,
		deps.FederationRepo,
		deps.IPFSClient,
	)

	if app.Config.EnableATProto {
		deps.FederationService = usecase.NewFederationService(
			deps.FederationRepo,
			deps.ModerationRepo,
			deps.AtprotoService,
			app.Config,
			deps.HardeningRepo,
		)
	}

	deps.HardeningService = usecase.NewFederationHardeningService(deps.HardeningRepo, deps.FederationService, app.Config)
	_ = deps.HardeningService.Initialize(context.Background())

	// Initialize ActivityPub service if enabled
	if app.Config.EnableActivityPub {
		// Create encryption for ActivityPub private keys
		encryption, err := security.NewActivityPubKeyEncryption(app.Config.ActivityPubKeyEncryptionKey)
		if err != nil {
			log.Fatalf("Failed to initialize ActivityPub key encryption: %v", err)
		}

		activityPubRepo := repository.NewActivityPubRepository(app.DB, encryption)
		deps.ActivityPubService = ucactivitypub.NewService(
			activityPubRepo,
			deps.UserRepo,
			deps.VideoRepo,
			deps.CommentRepo,
			app.Config,
		)
	}

	// Initialize IPFS streaming service
	deps.IPFSStreamingService = ucipfs.NewService(app.Config)

	// Initialize livestream manager
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	deps.StreamManager = livestream.NewStreamManager(
		deps.LiveStreamRepo,
		deps.ViewerSessionRepo,
		app.Redis,
		logger,
	)

	// Initialize HLS transcoder and RTMP server for live streaming (if enabled)
	if app.Config.EnableLiveStreaming {
		log.Println("Initializing HLS transcoder...")
		hlsTranscoder := livestream.NewHLSTranscoder(
			app.Config,
			deps.LiveStreamRepo,
			logger,
		)

		// Initialize VOD converter (2 workers by default)
		log.Println("Initializing VOD converter...")
		vodConverter := livestream.NewVODConverter(
			app.Config,
			deps.LiveStreamRepo,
			deps.VideoRepo,
			logger,
			2, // 2 concurrent VOD conversion workers
		)

		log.Println("Initializing RTMP server for live streaming...")
		app.rtmpServer = livestream.NewRTMPServer(
			app.Config,
			deps.LiveStreamRepo,
			deps.StreamKeyRepo,
			deps.StreamManager,
			hlsTranscoder,
			vodConverter,
			logger,
		)

		// Store transcoder and converter in app for later use (e.g., handlers, shutdown)
		app.hlsTranscoder = hlsTranscoder
		app.vodConverter = vodConverter
	}

	// Wire up import service dependencies
	app.WireImportDependencies(deps)

	// Initialize Payment Service if IOTA is enabled
	if app.Config.EnableIOTA && deps.IOTARepo != nil {
		// Create IOTA client
		iotaClient := payments.NewIOTAClient(app.Config.IOTANodeURL)

		// Parse encryption key if provided
		var encKey []byte
		if app.Config.IOTAWalletEncryptionKey != "" {
			if k, err := repository.DecodeTokenKey(app.Config.IOTAWalletEncryptionKey); err == nil {
				encKey = k
			} else {
				log.Printf("Warning: Failed to decode IOTA wallet encryption key, using default")
				// Use a default key derived from JWT secret as fallback
				encKey = []byte(app.Config.JWTSecret)[:32] // Take first 32 bytes for AES-256
			}
		} else {
			// Use JWT secret as default encryption key
			encKey = []byte(app.Config.JWTSecret)
			if len(encKey) > 32 {
				encKey = encKey[:32] // Ensure it's 32 bytes for AES-256
			} else if len(encKey) < 32 {
				// Pad with zeros if too short
				padded := make([]byte, 32)
				copy(padded, encKey)
				encKey = padded
			}
		}

		// Create payment service
		deps.PaymentService = ucpayments.NewPaymentService(
			deps.IOTARepo,
			iotaClient,
			encKey,
		)

		log.Println("IOTA payment service initialized")
	}

	return deps
}

func (app *Application) initializeSchedulers(deps *Dependencies) {
	if app.Config.EnableEncodingScheduler && deps.EncodingService != nil {
		interval := time.Duration(app.Config.EncodingSchedulerIntervalSeconds) * time.Second
		burst := app.Config.EncodingSchedulerBurst
		app.encodingScheduler = scheduler.NewEncodingScheduler(deps.EncodingService, interval, burst)
		app.schedulers = append(app.schedulers, app.encodingScheduler)
	}

	if app.Config.EnableATProto && app.Config.EnableFederationScheduler && deps.FederationService != nil {
		fInterval := time.Duration(app.Config.FederationSchedulerIntervalSeconds) * time.Second
		fBurst := app.Config.FederationSchedulerBurst
		app.federationScheduler = scheduler.NewFederationScheduler(deps.FederationService, fInterval, fBurst)
		app.schedulers = append(app.schedulers, app.federationScheduler)
	}

	if app.Config.EnableATProto && app.Config.EnableATProtoFirehose && deps.FederationService != nil {
		fhInterval := time.Duration(app.Config.ATProtoFirehosePollIntervalSeconds) * time.Second
		app.firehosePoller = scheduler.NewFirehosePoller(deps.FederationService, fhInterval, 3)
		app.schedulers = append(app.schedulers, app.firehosePoller)
	}
}

func (app *Application) registerRoutes(deps *Dependencies) {
	httpapi.RegisterRoutesWithDependencies(app.Router, app.Config, app.rateLimiterManager, &shared.HandlerDependencies{
		UserRepo:             deps.UserRepo,
		VideoRepo:            deps.VideoRepo,
		UploadRepo:           deps.UploadRepo,
		EncodingRepo:         deps.EncodingRepo,
		MessageRepo:          deps.MessageRepo,
		AuthRepo:             deps.AuthRepo,
		OAuthRepo:            deps.OAuthRepo,
		SubRepo:              deps.SubRepo,
		ViewsRepo:            deps.ViewsRepo,
		NotificationRepo:     deps.NotificationRepo,
		ChannelRepo:          deps.ChannelRepo,
		CommentRepo:          deps.CommentRepo,
		RatingRepo:           deps.RatingRepo,
		PlaylistRepo:         deps.PlaylistRepo,
		CaptionRepo:          deps.CaptionRepo,
		ModerationRepo:       deps.ModerationRepo,
		FederationRepo:       deps.FederationRepo,
		HardeningRepo:        deps.HardeningRepo,
		SessionRepo:          deps.SessionRepo,
		LiveStreamRepo:       deps.LiveStreamRepo,
		StreamKeyRepo:        deps.StreamKeyRepo,
		ViewerSessionRepo:    deps.ViewerSessionRepo,
		UploadService:        deps.UploadService,
		MessageService:       deps.MessageService,
		ViewsService:         deps.ViewsService,
		NotificationService:  deps.NotificationService,
		ChannelService:       deps.ChannelService,
		CommentService:       deps.CommentService,
		RatingService:        deps.RatingService,
		PlaylistService:      deps.PlaylistService,
		CaptionService:       deps.CaptionService,
		AtprotoService:       deps.AtprotoService,
		FederationService:    deps.FederationService,
		HardeningService:     deps.HardeningService,
		EncodingService:      deps.EncodingService,
		ImportService:        deps.ImportService,
		PaymentService:       deps.PaymentService,
		StreamManager:        deps.StreamManager,
		HLSTranscoder:        app.hlsTranscoder,
		IPFSStreamingService: deps.IPFSStreamingService,
		EncodingScheduler:    app.encodingScheduler,
		Redis:                app.Redis,
		JWTSecret:            app.Config.JWTSecret,
		RedisPingTimeout:     time.Duration(app.Config.RedisPingTimeout) * time.Second,
		IPFSApi:              app.Config.IPFSApi,
		IPFSCluster:          app.Config.IPFSCluster,
		IPFSPingTimeout:      time.Duration(app.Config.IPFSPingTimeout) * time.Second,
	})
}

func (app *Application) Start(ctx context.Context) error {
	for _, s := range app.schedulers {
		go s.Start(ctx)
	}

	// Start RTMP server if enabled
	if app.Config.EnableLiveStreaming && app.rtmpServer != nil {
		go func() {
			log.Printf("Starting RTMP server on %s:%d...", app.Config.RTMPHost, app.Config.RTMPPort)
			if err := app.rtmpServer.Start(ctx); err != nil {
				log.Printf("RTMP server stopped: %v", err)
			}
		}()
	}

	// Optionally start encoding workers within the app lifecycle
	if app.Config.EnableEncoding && app.Dependencies != nil && app.Dependencies.EncodingService != nil {
		workers := app.Config.EncodingWorkers
		encSvc := app.Dependencies.EncodingService
		go func() {
			log.Printf("Starting encoding workers (count=%d)...", workers)
			if err := encSvc.Run(ctx, workers); err != nil {
				log.Printf("Encoding workers stopped with error: %v", err)
			}
		}()

		// Start a lightweight metrics server if configured
		if app.Config.MetricsAddr != "" {
			mux := http.NewServeMux()
			mux.HandleFunc("/metrics", metrics.Handler)
			app.metricsServer = &http.Server{
				Addr:         app.Config.MetricsAddr,
				Handler:      mux,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
				IdleTimeout:  30 * time.Second,
			}
			go func() {
				log.Printf("Starting metrics server on %s", app.Config.MetricsAddr)
				if err := app.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("Metrics server error: %v", err)
				}
			}()
		}
	}

	return nil
}

func (app *Application) Shutdown(ctx context.Context) error {
	// Shutdown rate limiters first (graceful, allows requests to continue)
	if app.rateLimiterManager != nil {
		log.Println("Shutting down rate limiters...")
		if err := app.rateLimiterManager.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown rate limiters: %v", err)
		}
	}

	for _, s := range app.schedulers {
		s.Stop()
	}

	// Stop VOD converter if running (before stopping RTMP/HLS to allow queue processing)
	if app.vodConverter != nil {
		log.Println("Stopping VOD converter...")
		if err := app.vodConverter.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown VOD converter: %v", err)
		}
	}

	// Stop HLS transcoder if running
	if app.hlsTranscoder != nil {
		log.Println("Stopping HLS transcoder...")
		if err := app.hlsTranscoder.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown HLS transcoder: %v", err)
		}
	}

	// Stop RTMP server if running
	if app.rtmpServer != nil {
		log.Println("Stopping RTMP server...")
		if err := app.rtmpServer.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown RTMP server: %v", err)
		}
	}

	// Stop StreamManager if running
	if app.Dependencies != nil && app.Dependencies.StreamManager != nil {
		log.Println("Stopping StreamManager...")
		if err := app.Dependencies.StreamManager.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown StreamManager: %v", err)
		}
	}

	// Stop metrics server if running
	if app.metricsServer != nil {
		if err := app.metricsServer.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown metrics server: %v", err)
		}
	}

	if app.DB != nil {
		if err := app.DB.Close(); err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
	}

	if app.Redis != nil {
		if err := app.Redis.Close(); err != nil {
			return fmt.Errorf("failed to close redis: %w", err)
		}
	}

	return nil
}

func (app *Application) GetRouter() chi.Router {
	return app.Router
}

func (app *Application) GetEncodingScheduler() *scheduler.EncodingScheduler {
	return app.encodingScheduler
}
