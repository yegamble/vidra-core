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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"athena/internal/config"
	"athena/internal/httpapi"
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

	// Initialize database connection for encoding workers if enabled
	var db *sqlx.DB
	if cfg.EnableEncoding {
		db, err = sqlx.Connect("postgres", cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("Failed to connect to database for encoding: %v", err)
		}
		defer func() { _ = db.Close() }()
	}

	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))
	router.Use(middleware.Compress(5))
	router.Use(appMiddleware.CORS())

	httpapi.RegisterRoutes(router, cfg)

	// Start encoding workers if enabled
	var encodingCtx context.Context
	var encodingCancel context.CancelFunc
	if cfg.EnableEncoding {
		encodingCtx, encodingCancel = context.WithCancel(context.Background())

		// Start metrics server for encoding workers on separate mux
		go func() {
			metricsRouter := http.NewServeMux()
			metricsRouter.HandleFunc("/metrics", metrics.Handler)
			log.Printf("Starting metrics server on %s", cfg.MetricsAddr)
			if err := http.ListenAndServe(cfg.MetricsAddr, metricsRouter); err != nil {
				log.Printf("Metrics server error: %v", err)
			}
		}()

		// Start encoding workers
		encRepo := repository.NewEncodingRepository(db)
		videoRepo := repository.NewVideoRepository(db)
		encSvc := usecase.NewEncodingService(encRepo, videoRepo, cfg.StorageDir, cfg)

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
		Handler:      router,
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
