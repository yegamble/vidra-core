package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"athena/internal/app"
	"athena/internal/config"
	appMiddleware "athena/internal/middleware"
)

// Populated via -ldflags at build time in Dockerfile
var (
	version   = "dev"
	buildTime = ""
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize the application with all dependencies
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}
	defer func() {
		if err := application.Shutdown(context.Background()); err != nil {
			log.Printf("Failed to shutdown application cleanly: %v", err)
		}
	}()

	// Get the application router with all routes registered,
	// then mount it under a parent router where we apply global middleware.
	appRouter := application.GetRouter()
	root := chi.NewRouter()

	// Apply global middleware on the root BEFORE mounting routes, per chi requirements
	// Security middleware - should be first
	root.Use(appMiddleware.SecurityHeaders())
	root.Use(appMiddleware.RequestID())

	// Standard Chi middleware
	root.Use(middleware.RealIP)
	root.Use(middleware.Logger)
	root.Use(middleware.Recoverer)
	root.Use(middleware.Timeout(60 * time.Second))
	root.Use(middleware.Compress(5))

	// CORS and request size limiting
	root.Use(appMiddleware.CORS())
	// SECURITY: Limit request body size to 10MB by default to prevent DoS.
	// Allow larger payloads (cfg.MaxUploadSize) only for specific video upload endpoints.
	root.Use(appMiddleware.SizeLimiter(10*1024*1024, func(r *http.Request) int64 {
		path := r.URL.Path
		// Only POST requests carry upload bodies
		if r.Method != http.MethodPost {
			return 0
		}

		// Legacy one-shot upload: /api/v1/videos/upload
		if path == "/api/v1/videos/upload" {
			return cfg.MaxUploadSize
		}

		// Legacy chunked upload: /api/v1/videos/{id}/upload
		if strings.HasPrefix(path, "/api/v1/videos/") && strings.HasSuffix(path, "/upload") {
			return cfg.MaxUploadSize
		}

		// New chunked upload: /api/v1/uploads/{sessionId}/chunks
		if strings.HasPrefix(path, "/api/v1/uploads/") && strings.HasSuffix(path, "/chunks") {
			return cfg.MaxUploadSize
		}

		return 0 // Use default 10MB limit
	}))

	// Mount the pre-registered application routes
	root.Mount("/", appRouter)

	// Start background schedulers and workers managed by the app
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	defer backgroundCancel()

	if err := application.Start(backgroundCtx); err != nil {
		log.Fatalf("Failed to start background services: %v", err)
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      root,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if cfg.EnableEncoding {
			if buildTime != "" {
				log.Printf("Server starting on port %d with encoding workers (version=%s, build=%s)", cfg.Port, version, buildTime)
			} else {
				log.Printf("Server starting on port %d with encoding workers (version=%s)", cfg.Port, version)
			}
		} else {
			if buildTime != "" {
				log.Printf("Server starting on port %d (version=%s, build=%s)", cfg.Port, version, buildTime)
			} else {
				log.Printf("Server starting on port %d (version=%s)", cfg.Port, version)
			}
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Cancel background services
	backgroundCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
