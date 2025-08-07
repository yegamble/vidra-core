package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-redis/redis/v8"
)

// NewRouter constructs the HTTP router
func NewRouter(authHandler *AuthHandler, videoHandler *VideoHandler, chunkHandler *ChunkHandler, jwtSecret string, rdb *redis.Client) http.Handler {
	r := chi.NewRouter()

	// Global middlewares
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(60 * time.Second))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Auth routes
	r.Route("/auth", func(r chi.Router) {
		authHandler.RegisterRoutes(r)
	})

	// Video routes
	r.Route("/videos", func(r chi.Router) {
		// Protected endpoints
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(jwtSecret, rdb))
			r.Post("/", videoHandler.Upload)
			chunkHandler.RegisterRoutes(r)
		})
		// Public endpoints
		r.Get("/{id}", videoHandler.Get)
	})

	return r
}
