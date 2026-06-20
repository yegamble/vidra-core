// Command api is the vidra-core HTTP API service entrypoint. It loads
// configuration, opens connections to PostgreSQL and Redis, and serves the
// Echo HTTP API with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/cache"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/video"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Info("configuration loaded", "env", cfg.Environment, "addr", cfg.HTTPAddr())

	// Bound dependency startup so a missing DB/Redis fails fast rather than
	// hanging the process indefinitely.
	startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := store.New(startCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("connected to postgres")

	rdb, err := cache.New(startCtx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	logger.Info("connected to redis")

	var opts []httpapi.Option
	if cfg.RateLimitEnabled {
		counter := ratelimit.NewRedisCounter(rdb.Client)
		limiter := ratelimit.NewLimiter(counter, cfg.RateLimitRequests, cfg.RateLimitWindow)
		authLimiter := ratelimit.NewLimiter(counter, cfg.AuthRateLimitRequests, cfg.RateLimitWindow)
		opts = append(opts,
			httpapi.WithRateLimiter(limiter),
			httpapi.WithAuthRateLimiter(authLimiter),
		)
		logger.Info("rate limiting enabled",
			"requests", cfg.RateLimitRequests,
			"auth_requests", cfg.AuthRateLimitRequests,
			"window", cfg.RateLimitWindow,
		)
	}

	issuer := auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTAccessTTL)
	authsvc := auth.NewService(db.Queries(), issuer, cfg.JWTRefreshTTL)
	opts = append(opts, httpapi.WithAuthService(authsvc, cfg.JWTAccessTTL))

	channelsvc := channel.NewService(db.Queries())
	opts = append(opts, httpapi.WithChannelService(channelsvc))

	blobs, err := newStorageBackend(cfg)
	if err != nil {
		return err
	}
	logger.Info("media storage configured", "backend", cfg.StorageBackend)

	// Wire the FFprobe media prober when ffprobe is on PATH; otherwise uploads
	// finalise by publishing the original unprobed (no metadata) so a host
	// without ffmpeg still works.
	var vopts []video.Option
	if probe, ok := media.DetectFFProbe(blobs); ok {
		vopts = append(vopts, video.WithProber(probe))
		logger.Info("media probe enabled (ffprobe found)")
	} else {
		logger.Warn("media probe disabled (ffprobe not on PATH); originals are published unprobed")
	}
	if thumb, ok := media.DetectThumbnailer(blobs); ok {
		vopts = append(vopts, video.WithThumbnailer(thumb))
		logger.Info("thumbnail generation enabled (ffmpeg found)")
	} else {
		logger.Warn("thumbnail generation disabled (ffmpeg not on PATH); videos publish without a poster")
	}
	vopts = append(vopts, video.WithViewDeduper(cache.NewDeduper(rdb.Client)))
	videosvc := video.NewService(db.Queries(), blobs, vopts...)
	opts = append(opts, httpapi.WithVideoService(videosvc), httpapi.WithMediaStorage(blobs))

	srv := httpapi.New(cfg, db, rdb, opts...)

	// Run the server in the background so we can wait for a shutdown signal.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr())
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return err
	case sig := <-stop:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}

// newStorageBackend builds the media blob backend selected by config. Config
// validation already restricts StorageBackend to the supported set, so the
// default branch is a defensive guard.
func newStorageBackend(cfg *config.Config) (storage.Backend, error) {
	switch cfg.StorageBackend {
	case "local":
		return storage.NewLocal(cfg.StorageLocalRoot)
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}
