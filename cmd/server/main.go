package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"vidra-core/internal/app"
	"vidra-core/internal/config"
	appMiddleware "vidra-core/internal/middleware"
	"vidra-core/internal/obs"
	"vidra-core/internal/setup"
)

var (
	version   = "dev"
	buildTime = ""
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Bootstrap application logger with file output and rotation
	logger, logCloser := obs.NewLoggerWithFile(obs.LoggerConfig{
		Level:    cfg.LogLevel,
		Format:   cfg.LogFormat,
		LogDir:   cfg.LogDir,
		Filename: cfg.LogFilename,
		Rotation: obs.RotationConfig{
			Enabled:    cfg.LogRotationEnabled,
			MaxSizeMB:  cfg.LogRotationMaxSizeMB,
			MaxFiles:   cfg.LogRotationMaxFiles,
			MaxAgeDays: cfg.LogRotationMaxAgeDays,
		},
	})
	defer logCloser.Close()
	obs.SetGlobalLogger(logger)
	slog.SetDefault(logger)

	if cfg.SetupMode {
		logger.Info("Application is in setup mode")
		setupServer := setup.NewServer(fmt.Sprintf("%d", cfg.Port))
		if err := setupServer.Start(); err != nil {
			logger.Error("Setup server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	application, err := app.New(cfg)
	if err != nil {
		logger.Error("Failed to initialize application", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := application.Shutdown(context.Background()); err != nil {
			logger.Error("Failed to shutdown application cleanly", "error", err)
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

	root.Use(chiMiddleware.RealIP)
	root.Use(appMiddleware.LoggingMiddleware(appMiddleware.LoggingConfig{
		Logger:          logger,
		AnonymizeIP:     cfg.LogAnonymizeIP,
		LogHTTPRequests: cfg.LogHTTPRequests,
		LogPingRequests: cfg.LogPingRequests,
	}))
	root.Use(chiMiddleware.Recoverer)
	root.Use(chiMiddleware.Timeout(60 * time.Second))
	root.Use(chiMiddleware.Compress(5))

	root.Use(appMiddleware.CORS(cfg.CORSAllowedOrigins, cfg.CORSAllowedMethods, cfg.CORSAllowedHeaders))
	root.Use(appMiddleware.SizeLimiter(100 * 1024 * 1024))

	root.Mount("/", appRouter)

	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	defer backgroundCancel()

	if err := application.Start(backgroundCtx); err != nil {
		logger.Error("Failed to start background services", "error", err)
		os.Exit(1)
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
				logger.Info("Server starting with encoding workers", "port", cfg.Port, "version", version, "build", buildTime)
			} else {
				logger.Info("Server starting with encoding workers", "port", cfg.Port, "version", version)
			}
		} else {
			if buildTime != "" {
				logger.Info("Server starting", "port", cfg.Port, "version", version, "build", buildTime)
			} else {
				logger.Info("Server starting", "port", cfg.Port, "version", version)
			}
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	backgroundCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Server exited")
}
