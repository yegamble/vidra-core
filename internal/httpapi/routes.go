package httpapi

import (
    "context"
    "fmt"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/jmoiron/sqlx"
    _ "github.com/lib/pq"
    "github.com/redis/go-redis/v9"

    "athena/internal/config"
    "athena/internal/middleware"
    "athena/internal/repository"
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
    dbAuthRepo := repository.NewAuthRepository(db)

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

    // Create server instance with dependencies
    server := NewServer(userRepo, sessionRepo, cfg.JWTSecret, rdb, time.Duration(cfg.RedisPingTimeout)*time.Second)

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
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", StreamVideo)

            r.With(middleware.Auth(cfg.JWTSecret)).Post("/", CreateVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", UpdateVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", DeleteVideoHandler(videoRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", UploadVideoChunk)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", CompleteVideoUpload)
		})

		r.Route("/users", func(r chi.Router) {
			// Admin-style create user; currently just requires auth (role checks TBD)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", CreateUserHandler(userRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", GetCurrentUserHandler(userRepo))
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", UpdateCurrentUserHandler(userRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", GetUserHandler(userRepo))
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", GetUserVideosHandler(videoRepo))
		})
	})
}
