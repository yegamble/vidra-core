package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"athena/internal/app"
	"athena/internal/config"
	"athena/internal/metrics"
	appMiddleware "athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/usecase"
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
	root.Use(appMiddleware.SizeLimiter(100 * 1024 * 1024)) // 100MB default, override for upload endpoints

	// Mount the pre-registered application routes
	root.Mount("/", appRouter)

	// Start background schedulers and workers
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	defer backgroundCancel()

	if err := application.Start(backgroundCtx); err != nil {
		log.Fatalf("Failed to start background services: %v", err)
	}

	// Start encoding workers if enabled (standalone encoding service)
	var encodingCtx context.Context
	var encodingCancel context.CancelFunc
	if cfg.EnableEncoding {
		encodingCtx, encodingCancel = context.WithCancel(context.Background())

		// Connect to database for encoding workers
		db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("Failed to connect to database for encoding: %v", err)
		}
		defer func() { _ = db.Close() }()

		// Start metrics server for encoding workers on separate mux
		go func() {
			metricsRouter := http.NewServeMux()
			metricsRouter.HandleFunc("/metrics", metrics.Handler)
			metricsServer := &http.Server{
				Addr:         cfg.MetricsAddr,
				Handler:      metricsRouter,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
				IdleTimeout:  30 * time.Second,
			}
			log.Printf("Starting metrics server on %s", cfg.MetricsAddr)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("Metrics server error: %v", err)
			}
		}()

		// Start encoding workers
		encRepo := repository.NewEncodingRepository(db)
		videoRepo := repository.NewVideoRepository(db)
		userRepo := repository.NewUserRepository(db)
		subRepo := repository.NewSubscriptionRepository(db)
		notificationRepo := repository.NewNotificationRepository(db)
		notificationSvc := usecase.NewNotificationService(notificationRepo, subRepo, userRepo)
		// Optional ATProto publisher
		atproto := usecase.NewAtprotoService(nil, cfg, nil, nil)
		encSvc := usecase.NewEncodingService(encRepo, videoRepo, notificationSvc, cfg.StorageDir, cfg, atproto, nil)

		go func() {
			log.Printf("Starting encoding workers (count=%d)...", cfg.EncodingWorkers)
			if err := encSvc.Run(encodingCtx, cfg.EncodingWorkers); err != nil {
				log.Printf("Encoding workers stopped with error: %v", err)
			}
		}()

		// Optional: Start encoding job polling ticker
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-encodingCtx.Done():
					return
				case <-ticker.C:
					// Workers will poll via ProcessNext
				}
			}
		}()
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

	// Cancel encoding workers first if they're running
	if cfg.EnableEncoding && encodingCancel != nil {
		log.Println("Stopping encoding workers...")
		encodingCancel()
	}

	// Cancel background services
	backgroundCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
