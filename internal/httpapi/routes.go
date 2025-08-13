package httpapi

import (
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"

	"athena/internal/config"
	"athena/internal/middleware"
	"athena/internal/repository"
)

func RegisterRoutes(r chi.Router, cfg *config.Config, db *sqlx.DB) {
	r.Use(middleware.RateLimit(time.Minute, 100))

	// Create server instance
	server := NewServer()

	// Create user handler with repository
	userRepo := repository.NewUserRepository(db)
	userHandler := NewUserHandler(userRepo)

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
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/", ListVideos)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/search", SearchVideos)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", GetVideo)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/stream", StreamVideo)

			r.With(middleware.Auth(cfg.JWTSecret)).Post("/", CreateVideo)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/{id}", UpdateVideo)
			r.With(middleware.Auth(cfg.JWTSecret)).Delete("/{id}", DeleteVideo)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/upload", UploadVideoChunk)
			r.With(middleware.Auth(cfg.JWTSecret)).Post("/{id}/complete", CompleteVideoUpload)
		})

		r.Route("/users", func(r chi.Router) {
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", userHandler.GetCurrentUser)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", userHandler.UpdateCurrentUser)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", userHandler.GetUser)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", userHandler.GetUserVideos)
		})
	})
}
