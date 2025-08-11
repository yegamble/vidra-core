package httpapi

import (
	"time"

	"github.com/go-chi/chi/v5"

	"athena/internal/config"
	"athena/internal/generated"
	"athena/internal/middleware"
)

func RegisterRoutes(r chi.Router, cfg *config.Config) {
	r.Use(middleware.RateLimit(time.Minute, 100))

	// Create server instance
	server := NewServer()

	// Register OpenAPI generated routes with auth middleware
	handler := generated.HandlerFromMuxWithBaseURL(server, r, "")

	// Add auth middleware to specific routes
	r.Route("/auth/logout", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		r.Post("/", server.Logout)
	})

	// Mount the generated handler
	r.Mount("/", handler)

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
			r.With(middleware.Auth(cfg.JWTSecret)).Get("/me", GetCurrentUser)
			r.With(middleware.Auth(cfg.JWTSecret)).Put("/me", UpdateCurrentUser)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}", GetUser)
			r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/{id}/videos", GetUserVideos)
		})
	})
}