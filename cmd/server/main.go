package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"athena/internal/app"
	"athena/internal/config"
	appMiddleware "athena/internal/middleware"
	"athena/internal/setup"
)

var (
	version   = "dev"
	buildTime = ""
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.SetupMode {
		log.Println("Application is in setup mode")
		setupServer := setup.NewServer(fmt.Sprintf("%d", cfg.Port))
		if err := setupServer.Start(); err != nil {
			log.Fatalf("Setup server failed: %v", err)
		}
		return
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}
	defer func() {
		if err := application.Shutdown(context.Background()); err != nil {
			log.Printf("Failed to shutdown application cleanly: %v", err)
		}
	}()

	appRouter := application.GetRouter()
	root := chi.NewRouter()

	cdnDomains := []string{}
	seen := make(map[string]bool)
	for _, rawURL := range []string{
		cfg.ObjectStorageConfig.StreamingPlaylistsBaseURL,
		cfg.ObjectStorageConfig.WebVideosBaseURL,
		cfg.ObjectStorageConfig.UserExportsBaseURL,
		cfg.ObjectStorageConfig.OriginalVideoFilesBaseURL,
		cfg.ObjectStorageConfig.CaptionsBaseURL,
	} {
		if rawURL == "" {
			continue
		}
		if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
			origin := u.Scheme + "://" + u.Host
			if !seen[origin] {
				cdnDomains = append(cdnDomains, origin)
				seen[origin] = true
			}
		}
	}

	securityCfg := appMiddleware.SecurityConfig{
		CSPEnabled:    cfg.CSPConfig.Enabled,
		CSPReportOnly: cfg.CSPConfig.ReportOnly,
		CSPReportURI:  cfg.CSPConfig.ReportURI,
		CDNDomains:    cdnDomains,
	}

	root.Use(appMiddleware.SecurityHeaders(securityCfg))
	root.Use(appMiddleware.RequestID())

	root.Use(middleware.RealIP)
	root.Use(middleware.Logger)
	root.Use(middleware.Recoverer)
	root.Use(middleware.Timeout(60 * time.Second))
	root.Use(middleware.Compress(5))

	root.Use(appMiddleware.CORS(cfg.CORSAllowedOrigins))
	root.Use(appMiddleware.SizeLimiter(100 * 1024 * 1024))

	root.Mount("/", appRouter)

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

	backgroundCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
