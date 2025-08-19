package httpapi

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"athena/internal/config"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/scheduler"
	"athena/internal/usecase"
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

	// Create uploads directory structure
	uploadsDir := "./uploads"
	if err := os.MkdirAll(filepath.Join(uploadsDir, "temp"), 0755); err != nil {
		panic(fmt.Errorf("failed to create temp uploads directory: %w", err))
	}
	if err := os.MkdirAll(filepath.Join(uploadsDir, "completed"), 0755); err != nil {
		panic(fmt.Errorf("failed to create completed uploads directory: %w", err))
	}

	// Initialize services
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, uploadsDir, cfg)
	messageService := usecase.NewMessageService(messageRepo, userRepo)

	// Start a lightweight encoding scheduler in the background to ensure
	// pending jobs are processed even if the standalone encoder is not running.
	// This uses a short interval with a small burst to avoid starvation.
	var encSched *scheduler.EncodingScheduler
	if cfg.EnableEncodingScheduler {
		encSvc := usecase.NewEncodingService(encodingRepo, videoRepo, uploadsDir, cfg)
		interval := time.Duration(cfg.EncodingSchedulerIntervalSeconds) * time.Second
		burst := cfg.EncodingSchedulerBurst
		encSched = scheduler.NewEncodingScheduler(encSvc, interval, burst)
		// Use Background context; lifecycle is tied to the server process.
		go encSched.Start(context.Background())
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
		resp, err := client.Get(cfg.IPFSApi + "/api/v0/version")
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if cfg.RequireIPFS {
				panic(fmt.Errorf("failed to connect to ipfs api at %s", cfg.IPFSApi))
			}
			log.Printf("warning: IPFS API not reachable at %s: %v (continuing as REQUIRE_IPFS=false)", cfg.IPFSApi, err)
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}

	// Create server instance with dependencies
	server := NewServer(
		userRepo,
		sessionRepo,
		cfg.JWTSecret,
		rdb,
		time.Duration(cfg.RedisPingTimeout)*time.Second,
		cfg.IPFSApi,
		time.Duration(cfg.IPFSPingTimeout)*time.Second,
	)

	// Register auth routes with appropriate middleware
	r.Post("/auth/register", server.Register)
	r.Post("/auth/login", server.Login)
	r.Post("/auth/refresh", server.RefreshToken)
	r.With(middleware.Auth(cfg.JWTSecret)).Post("/auth/logout", server.Logout)

	// Register health routes
	r.Get("/health", server.HealthCheck)
	r.Get("/ready", server.ReadinessCheck)

	// Additional API routes for videos and users (if they exist)
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/videos", func(r chi.Router) {
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", ListVideosHandler(videoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/search", SearchVideosHandler(videoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", GetVideoHandler(videoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", StreamVideoHandler(videoRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/qualities", GetSupportedQualities)

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", CreateVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", UpdateVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", DeleteVideoHandler(videoRepo))

			// Direct video upload endpoints (for backward compatibility with tests)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", VideoUploadChunkHandler(uploadService, cfg))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", VideoCompleteUploadHandler(uploadService))
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
    })
}
