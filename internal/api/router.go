package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-redis/redis/v8"
)

// NewRouter constructs the HTTP router. It takes initialized handlers and
// authentication parameters (secret and Redis). It wires middlewares
// and registers endpoints. Only video upload is protected by JWT.
func NewRouter(authHandler *AuthHandler, videoHandler *VideoHandler, jwtSecret string, rdb *redis.Client) http.Handler {
	r := chi.NewRouter()
	// Global middlewares
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

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
		// Only POST /videos requires auth
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(jwtSecret, rdb))
			r.Post("/", videoHandler.Upload)
		})
		r.Get("/", videoHandler.List)
		r.Get("/{id}", videoHandler.Get)
	})

	return r
}
