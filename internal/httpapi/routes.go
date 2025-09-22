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
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
	"strings"
)

func RegisterRoutes(r chi.Router, cfg *config.Config) {
	r.Use(middleware.RateLimit(time.Minute, 100))

	// Initialize database and repositories for handlers that need them
	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		panic(fmt.Errorf("failed to connect to database: %w", err))
	}
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
		fedSvc = usecase.NewFederationService(federationRepo, moderationRepo, atprotoSvc, cfg)
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

	// Create server instance with dependencies
	server := NewServerWithOAuth(
		userRepo,
		sessionRepo,
		oauthRepo,
		cfg.JWTSecret,
		rdb,
		time.Duration(cfg.RedisPingTimeout)*time.Second,
		cfg.IPFSApi,
		cfg.IPFSCluster,
		time.Duration(cfg.IPFSPingTimeout)*time.Second,
		cfg,
	)

	// Register auth routes with appropriate middleware
	r.Post("/auth/register", server.Register)
	r.Post("/auth/login", server.Login)
	r.Post("/auth/refresh", server.RefreshToken)
	r.With(middleware.Auth(cfg.JWTSecret)).Post("/auth/logout", server.Logout)

	// OAuth2 endpoints
	r.Post("/oauth/token", server.OAuthToken)
	r.HandleFunc("/oauth/authorize", server.OAuthAuthorize)
	r.Post("/oauth/revoke", server.OAuthRevoke)
	r.Post("/oauth/introspect", server.OAuthIntrospect)

	// Register health routes
	r.Get("/health", server.HealthCheck)
	r.Get("/ready", server.ReadinessCheck)

	// Additional API routes for videos and users (if they exist)
	r.Route("/api/v1", func(r chi.Router) {
		// Initialize views handler early for use in routes
		viewsHandler := NewViewsHandler(viewsService)

		r.Route("/videos", func(r chi.Router) {
			log.Printf("Registering video routes...")
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", ListVideosHandler(videoRepo))
			// Static routes must come before parameterized routes
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/search", SearchVideosHandler(videoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/qualities", GetSupportedQualities)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/top", viewsHandler.GetTopVideos)
			// Legacy one-shot upload endpoint for Postman collection compatibility
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/upload", UploadVideoFileHandler(videoRepo, cfg))
			// Parameterized routes come after static routes
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", GetVideoHandler(videoRepo, captionService))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", StreamVideoHandler(videoRepo))
			// Subscription feed
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/subscriptions", ListSubscriptionVideosHandler(subRepo))

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", CreateVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", UpdateVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", DeleteVideoHandler(videoRepo))

			// Direct video upload endpoints (for backward compatibility with tests)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", VideoUploadChunkHandler(uploadService, cfg))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", VideoCompleteUploadHandler(uploadService))

			// Views and analytics endpoints for specific videos
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Post("/{id}/views", viewsHandler.TrackView)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/analytics", viewsHandler.GetVideoAnalytics)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/{id}/stats/daily", viewsHandler.GetDailyStats)

			// Comment endpoints
			commentHandlers := NewCommentHandlers(commentService)
			r.Route("/{videoId}/comments", func(r chi.Router) {
				r.Get("/", commentHandlers.GetComments)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/", commentHandlers.CreateComment)
			})

			// Rating endpoints
			ratingHandlers := NewRatingHandlers(ratingService)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}/rating", ratingHandlers.SetRating)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/rating", ratingHandlers.GetRating)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/rating", ratingHandlers.RemoveRating)

			// Watch Later shortcut
			playlistHandlers := NewPlaylistHandlers(playlistService)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/watch-later", playlistHandlers.AddToWatchLater)

			// Caption endpoints
			captionHandlers := NewCaptionHandlers(captionService, videoRepo)
			r.Route("/{id}/captions", func(r chi.Router) {
				r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", captionHandlers.GetCaptions)
				r.With(middleware.Auth(cfg.JWTSecret)).Post("/", captionHandlers.CreateCaption)
				r.Route("/{captionId}", func(r chi.Router) {
					r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/content", captionHandlers.GetCaptionContent)
					r.With(middleware.Auth(cfg.JWTSecret)).Put("/", captionHandlers.UpdateCaption)
					r.With(middleware.Auth(cfg.JWTSecret)).Delete("/", captionHandlers.DeleteCaption)
				})
			})
		})

		// Static HLS handler with privacy gating and cache headers
		r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/hls/*", HLSHandler(videoRepo))

		// Chunked upload endpoints
		r.Route("/uploads", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Post("/initiate", InitiateUploadHandler(uploadService, videoRepo))
			r.Route("/{sessionId}", func(r chi.Router) {
				r.Post("/chunks", UploadChunkHandler(uploadService, cfg))
				r.Post("/complete", CompleteUploadHandler(uploadService, encodingRepo))
				r.Get("/status", GetUploadStatusHandler(uploadService))
				r.Get("/resume", ResumeUploadHandler(uploadService))
			})
		})

		r.Route("/encoding", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/status", EncodingStatusHandlerEnhanced(encodingRepo, cfg, encSched))
		})

		r.Route("/users", func(r chi.Router) {
			// Admin-style create user; currently just requires auth (role checks TBD)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", CreateUserHandler(userRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", GetCurrentUserHandler(userRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", UpdateCurrentUserHandler(userRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/me/avatar", server.UploadAvatar)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", GetUserHandler(userRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", GetUserVideosHandler(videoRepo))
			// Subscriptions
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/subscribe", SubscribeToUserHandler(subRepo, userRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/subscribe", UnsubscribeFromUserHandler(subRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/subscriptions", ListMySubscriptionsHandler(subRepo))

			// User's channels
			channelHandlers := NewChannelHandlers(channelService, subRepo)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/channels", channelHandlers.GetMyChannels)

			// User's ratings
			ratingHandlers := NewRatingHandlers(ratingService)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/ratings", ratingHandlers.GetUserRatings)

			// User's Watch Later playlist
			playlistHandlers := NewPlaylistHandlers(playlistService)
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me/watch-later", playlistHandlers.GetWatchLater)
		})

		r.Route("/messages", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Post("/", SendMessageHandler(messageService))
			r.Get("/", GetMessagesHandler(messageService))
			r.Put("/{messageId}/read", MarkMessageReadHandler(messageService))
			r.Delete("/{messageId}", DeleteMessageHandler(messageService))
		})

		r.Route("/conversations", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Get("/", GetConversationsHandler(messageService))
			r.Get("/unread-count", GetUnreadCountHandler(messageService))
		})

		// Trending endpoint
		r.Get("/trending", viewsHandler.GetTrendingVideos)

		// Fingerprinting for view deduplication
		r.Post("/views/fingerprint", viewsHandler.GenerateFingerprint)

		// Channels
		r.Route("/channels", func(r chi.Router) {
			channelHandlers := NewChannelHandlers(channelService, subRepo)

			// Public routes
			r.Get("/", channelHandlers.ListChannels)
			r.Get("/{id}", channelHandlers.GetChannel)
			r.Get("/{id}/videos", channelHandlers.GetChannelVideos)
			r.Get("/{id}/subscribers", channelHandlers.GetChannelSubscribers)

			// Authenticated routes
			r.Group(func(r chi.Router) {
				r.Use(middleware.Auth(cfg.JWTSecret))
				r.Post("/", channelHandlers.CreateChannel)
				r.Put("/{id}", channelHandlers.UpdateChannel)
				r.Delete("/{id}", channelHandlers.DeleteChannel)
				r.Post("/{id}/subscribe", channelHandlers.SubscribeToChannel)
				r.Delete("/{id}/subscribe", channelHandlers.UnsubscribeFromChannel)
			})
		})

		// Comments (standalone endpoints)
		r.Route("/comments", func(r chi.Router) {
			commentHandlers := NewCommentHandlers(commentService)
			r.Get("/{commentId}", commentHandlers.GetComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{commentId}", commentHandlers.UpdateComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{commentId}", commentHandlers.DeleteComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{commentId}/flag", commentHandlers.FlagComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{commentId}/flag", commentHandlers.UnflagComment)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{commentId}/moderate", commentHandlers.ModerateComment)
		})

		// Notifications
		r.Route("/notifications", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			notificationHandlers := NewNotificationHandlers(notificationService)
			r.Get("/", notificationHandlers.GetNotifications)
			r.Get("/unread-count", notificationHandlers.GetUnreadCount)
			r.Get("/stats", notificationHandlers.GetNotificationStats)
			r.Put("/{id}/read", notificationHandlers.MarkAsRead)
			r.Put("/read-all", notificationHandlers.MarkAllAsRead)
			r.Delete("/{id}", notificationHandlers.DeleteNotification)
		})

		// Federation endpoints
		r.Route("/federation", func(r chi.Router) {
			fedHandlers := NewFederationHandlers(federationRepo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/timeline", fedHandlers.GetTimeline)
		})

		// Playlists
		r.Route("/playlists", func(r chi.Router) {
			playlistHandlers := NewPlaylistHandlers(playlistService)

			// Public routes
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", playlistHandlers.ListPlaylists)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", playlistHandlers.GetPlaylist)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/items", playlistHandlers.GetPlaylistItems)

			// Authenticated routes
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", playlistHandlers.CreatePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", playlistHandlers.UpdatePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", playlistHandlers.DeletePlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/items", playlistHandlers.AddVideoToPlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}/items/{itemId}", playlistHandlers.RemoveVideoFromPlaylist)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}/items/{itemId}/reorder", playlistHandlers.ReorderPlaylistItem)
		})

		// Moderation handlers
		moderationHandlers := NewModerationHandlers(moderationRepo)
		instanceHandlers := NewInstanceHandlers(moderationRepo, userRepo, videoRepo)

		// Abuse reports - any authenticated user can create, admins/mods can manage
		r.Route("/abuse-reports", func(r chi.Router) {
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", moderationHandlers.CreateAbuseReport)
		})

		// Admin moderation endpoints
		r.Route("/admin", func(r chi.Router) {
			r.Use(middleware.Auth(cfg.JWTSecret))
			r.Use(middleware.RequireRole("admin")) // TODO: Add moderator role support

			// Abuse reports management
			r.Route("/abuse-reports", func(r chi.Router) {
				r.Get("/", moderationHandlers.ListAbuseReports)
				r.Get("/{id}", moderationHandlers.GetAbuseReport)
				r.Put("/{id}", moderationHandlers.UpdateAbuseReport)
				r.Delete("/{id}", moderationHandlers.DeleteAbuseReport)
			})

			// Blocklist management
			r.Route("/blocklist", func(r chi.Router) {
				r.Post("/", moderationHandlers.CreateBlocklistEntry)
				r.Get("/", moderationHandlers.ListBlocklistEntries)
				r.Put("/{id}", moderationHandlers.UpdateBlocklistEntry)
				r.Delete("/{id}", moderationHandlers.DeleteBlocklistEntry)
			})

			// Instance configuration (admin only)
			r.Route("/instance/config", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				r.Get("/", instanceHandlers.ListInstanceConfigs)
				r.Get("/{key}", instanceHandlers.GetInstanceConfig)
				r.Put("/{key}", instanceHandlers.UpdateInstanceConfig)
			})

			// OAuth client management (admin only)
			r.Route("/oauth/clients", func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				r.Get("/", server.AdminListOAuthClients)
				r.Post("/", server.AdminCreateOAuthClient)
				r.Put("/{clientId}/secret", server.AdminRotateOAuthClientSecret)
				r.Delete("/{clientId}", server.AdminDeleteOAuthClient)
			})

			// Federation jobs (admin)
			fedAdminHandlers := NewAdminFederationHandlers(federationRepo)
			r.Route("/federation/jobs", func(r chi.Router) {
				r.Get("/", fedAdminHandlers.ListJobs)
				r.Get("/{id}", fedAdminHandlers.GetJob)
				r.Post("/{id}/retry", fedAdminHandlers.RetryJob)
				r.Delete("/{id}", fedAdminHandlers.DeleteJob)
			})

			// Federation actors (admin)
			fedActorsHandlers := NewAdminFederationActorsHandlers(federationRepo)
			r.Route("/federation/actors", func(r chi.Router) {
				r.Get("/", fedActorsHandlers.ListActors)
				r.Post("/", fedActorsHandlers.UpsertActor)
				r.Put("/{actor}", fedActorsHandlers.UpdateActor)
				r.Delete("/{actor}", fedActorsHandlers.DeleteActor)
			})
		})

		// Public instance information
		r.Route("/instance", func(r chi.Router) {
			r.Get("/about", instanceHandlers.GetInstanceAbout)
		})
	})

	// OEmbed endpoint (outside of /api/v1)
	r.Get("/oembed", NewInstanceHandlers(moderationRepo, userRepo, videoRepo).OEmbed)

	// ATProto well-known DID endpoint for handle verification
	r.Get("/.well-known/atproto-did", NewInstanceHandlers(moderationRepo, userRepo, videoRepo).WellKnownAtprotoDID)

	// Custom 404 handler that returns JSON error response
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("NOT_FOUND %s %s", r.Method, r.URL.Path)
		WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "The requested resource was not found"))
	})

	// Custom 405 handler for method not allowed
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusMethodNotAllowed, domain.NewDomainError("METHOD_NOT_ALLOWED", "Method not allowed for this endpoint"))
	})

	// Debug: log all registered routes when log level is debug/trace
	if lvl := strings.ToLower(cfg.LogLevel); lvl == "debug" || lvl == "trace" {
		_ = chi.Walk(r, func(method string, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
			log.Printf("ROUTE %s %s", method, route)
			return nil
		})
	}
}
