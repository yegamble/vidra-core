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

	"athena/internal/config"
	"athena/internal/httpapi"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
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
}

type Dependencies struct {
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
}

func New(cfg *config.Config) (*Application, error) {
	app := &Application{
		Config:     cfg,
		Router:     chi.NewRouter(),
		schedulers: []scheduler.Scheduler{},
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
	app.DB = db
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
		UserRepo:         repository.NewUserRepository(app.DB),
		VideoRepo:        repository.NewVideoRepository(app.DB),
		UploadRepo:       repository.NewUploadRepository(app.DB),
		EncodingRepo:     repository.NewEncodingRepository(app.DB),
		MessageRepo:      repository.NewMessageRepository(app.DB),
		AuthRepo:         repository.NewAuthRepository(app.DB),
		OAuthRepo:        repository.NewOAuthRepository(app.DB),
		SubRepo:          repository.NewSubscriptionRepository(app.DB),
		ViewsRepo:        repository.NewViewsRepository(app.DB),
		NotificationRepo: repository.NewNotificationRepository(app.DB),
		ChannelRepo:      repository.NewChannelRepository(app.DB),
		CommentRepo:      repository.NewCommentRepository(app.DB),
		RatingRepo:       repository.NewRatingRepository(app.DB),
		PlaylistRepo:     repository.NewPlaylistRepository(app.DB),
		CaptionRepo:      repository.NewCaptionRepository(app.DB),
		ModerationRepo:   repository.NewModerationRepository(app.DB),
		FederationRepo:   repository.NewFederationRepository(app.DB),
		HardeningRepo:    repository.NewFederationHardeningRepository(app.DB),
	}

	redisSessionRepo := repository.NewRedisSessionRepository(app.Redis)
	deps.SessionRepo = repository.NewCompositeAuthRepository(deps.AuthRepo, redisSessionRepo)

	deps.UploadService = usecase.NewUploadService(deps.UploadRepo, deps.EncodingRepo, deps.VideoRepo, app.Config.StorageDir, app.Config)
	deps.MessageService = usecase.NewMessageService(deps.MessageRepo, deps.UserRepo)
	deps.ViewsService = usecase.NewViewsService(deps.ViewsRepo, deps.VideoRepo)
	deps.NotificationService = usecase.NewNotificationService(deps.NotificationRepo, deps.SubRepo, deps.UserRepo)
	deps.ChannelService = usecase.NewChannelService(deps.ChannelRepo, deps.UserRepo)
	deps.CommentService = usecase.NewCommentService(deps.CommentRepo, deps.VideoRepo, deps.UserRepo, deps.ChannelRepo)
	deps.RatingService = usecase.NewRatingService(deps.RatingRepo, deps.VideoRepo)
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

	deps.EncodingService = usecase.NewEncodingService(
		deps.EncodingRepo,
		deps.VideoRepo,
		deps.NotificationService,
		app.Config.StorageDir,
		app.Config,
		deps.AtprotoService,
		deps.FederationRepo,
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
	httpapi.RegisterRoutesWithDependencies(app.Router, app.Config, &httpapi.HandlerDependencies{
		UserRepo:            deps.UserRepo,
		VideoRepo:           deps.VideoRepo,
		UploadRepo:          deps.UploadRepo,
		EncodingRepo:        deps.EncodingRepo,
		MessageRepo:         deps.MessageRepo,
		AuthRepo:            deps.AuthRepo,
		OAuthRepo:           deps.OAuthRepo,
		SubRepo:             deps.SubRepo,
		ViewsRepo:           deps.ViewsRepo,
		NotificationRepo:    deps.NotificationRepo,
		ChannelRepo:         deps.ChannelRepo,
		CommentRepo:         deps.CommentRepo,
		RatingRepo:          deps.RatingRepo,
		PlaylistRepo:        deps.PlaylistRepo,
		CaptionRepo:         deps.CaptionRepo,
		ModerationRepo:      deps.ModerationRepo,
		FederationRepo:      deps.FederationRepo,
		HardeningRepo:       deps.HardeningRepo,
		SessionRepo:         deps.SessionRepo,
		UploadService:       deps.UploadService,
		MessageService:      deps.MessageService,
		ViewsService:        deps.ViewsService,
		NotificationService: deps.NotificationService,
		ChannelService:      deps.ChannelService,
		CommentService:      deps.CommentService,
		RatingService:       deps.RatingService,
		PlaylistService:     deps.PlaylistService,
		CaptionService:      deps.CaptionService,
		AtprotoService:      deps.AtprotoService,
		FederationService:   deps.FederationService,
		HardeningService:    deps.HardeningService,
		EncodingService:     deps.EncodingService,
		EncodingScheduler:   app.encodingScheduler,
		Redis:               app.Redis,
		JWTSecret:           app.Config.JWTSecret,
		RedisPingTimeout:    time.Duration(app.Config.RedisPingTimeout) * time.Second,
		IPFSApi:             app.Config.IPFSApi,
		IPFSCluster:         app.Config.IPFSCluster,
		IPFSPingTimeout:     time.Duration(app.Config.IPFSPingTimeout) * time.Second,
	})
}

func (app *Application) Start(ctx context.Context) error {
	for _, s := range app.schedulers {
		go s.Start(ctx)
	}
	return nil
}

func (app *Application) Shutdown(ctx context.Context) error {
	for _, s := range app.schedulers {
		s.Stop()
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
