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

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/block"
	"github.com/vidra/vidra-core/internal/cache"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/version"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/watchword"
)

func main() {
	// Bootstrap logger for pre-config diagnostics; replaced by the configured
	// logger (LOG_LEVEL/LOG_FORMAT) once config is loaded in run().
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger, err := observability.NewLogger(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	opts := []httpapi.Option{httpapi.WithLogger(logger)}
	logger.Info("configuration loaded",
		"env", cfg.Environment,
		"addr", cfg.HTTPAddr(),
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
	)

	// OpenTelemetry tracing (no-op with zero cost when OTEL_ENABLED is false).
	otelShutdown, err := observability.SetupTracing(context.Background(), observability.TracingConfig{
		Enabled:        cfg.OTelEnabled,
		Endpoint:       cfg.OTelExporterEndpoint,
		Protocol:       cfg.OTelExporterProtocol,
		ServiceName:    cfg.OTelServiceName,
		ServiceVersion: version.Version,
	})
	if err != nil {
		return err
	}
	defer func() { _ = otelShutdown(context.Background()) }()
	if cfg.OTelEnabled {
		logger.Info("opentelemetry tracing enabled",
			"endpoint", cfg.OTelExporterEndpoint,
			"protocol", cfg.OTelExporterProtocol,
			"service", cfg.OTelServiceName,
		)
	}

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

	if cfg.ImportAllowPrivateURLs {
		logger.Warn("URL-import SSRF guard RELAXED — private/loopback addresses are fetchable by video URL import; NEVER enable this in production (HTTP_IMPORT_ALLOW_PRIVATE_URLS)")
	}

	issuer := auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTAccessTTL)
	var authOpts []auth.Option
	var captureMailer *auth.CaptureMailer
	if cfg.DevMailCaptureEnabled {
		captureMailer = auth.NewCaptureMailer()
		authOpts = append(authOpts, auth.WithMailer(captureMailer))
		logger.Warn("DEV mail capture ENABLED — account-security tokens are retrievable via GET /api/v1/dev/email-token; NEVER enable this in production (DEV_MAIL_CAPTURE_ENABLED)")
	}
	authsvc := auth.NewService(db.Queries(), issuer, cfg.JWTRefreshTTL, authOpts...)
	opts = append(opts, httpapi.WithAuthService(authsvc, cfg.JWTAccessTTL))
	if captureMailer != nil {
		opts = append(opts, httpapi.WithDevMailCapture(captureMailer))
	}

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
	// When federation is on, fan a published video out to the channel's remote
	// followers. fedsvc is assigned below; the hook only runs post-startup so the
	// closure sees the built service (nil-guarded regardless).
	var fedsvc *federation.Service
	if cfg.FederationEnabled {
		vopts = append(vopts, video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
			if fedsvc == nil {
				return
			}
			if err := fedsvc.AnnounceVideo(ctx, videoID); err != nil {
				logger.Warn("federation announce failed", "video_id", videoID, "error", err)
			}
		}))
	}
	videosvc := video.NewService(db.Queries(), blobs, vopts...)
	opts = append(opts, httpapi.WithVideoService(videosvc), httpapi.WithMediaStorage(blobs))

	commentsvc := comment.NewService(db.Queries())
	opts = append(opts, httpapi.WithCommentService(commentsvc))

	ratingsvc := rating.NewService(db.Queries())
	opts = append(opts, httpapi.WithRatingService(ratingsvc))

	notifsvc := notification.NewService(db.Queries())
	opts = append(opts, httpapi.WithNotificationService(notifsvc))

	playlistsvc := playlist.NewService(db.Queries())
	opts = append(opts, httpapi.WithPlaylistService(playlistsvc))

	moderationsvc := moderation.NewService(db.Queries())
	opts = append(opts, httpapi.WithModerationService(moderationsvc))

	mutesvc := mute.NewService(db.Queries())
	opts = append(opts, httpapi.WithMuteService(mutesvc))

	blocksvc := block.NewService(db.Queries())
	opts = append(opts, httpapi.WithBlockService(blocksvc))

	watchwordsvc := watchword.NewService(db.Queries())
	opts = append(opts, httpapi.WithWatchWordService(watchwordsvc))

	adminsvc := admin.NewService(db.Queries())
	opts = append(opts, httpapi.WithAdminService(adminsvc))

	auditsvc := audit.NewService(db.Queries())
	opts = append(opts, httpapi.WithAuditLog(auditsvc))

	messagingsvc := messaging.NewService(db.Queries(), messaging.WithBlocker(blocksvc))
	opts = append(opts, httpapi.WithMessagingService(messagingsvc))

	livesvc := live.NewService(db.Queries())
	opts = append(opts, httpapi.WithLiveService(livesvc))

	// Federation (ActivityPub) — the service is always constructed, but its routes
	// mount only when FEDERATION_ENABLED (gated in httpapi.routes). Actor private
	// keys are envelope-encrypted with FEDERATION_KEY_KEK; without it (dev) they are
	// stored raw. See .ralph/specs/federation.md.
	fedOpts := []federation.Option{
		federation.WithBaseURL(cfg.PublicBaseURL),
		// Reuse the outbound-fetch dev knob: when set, remote-actor fetches may reach
		// loopback/private origins (dev/e2e only; never in production).
		federation.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
	}
	if cfg.FederationKeyKEK != "" {
		cipher, err := secretbox.NewCipherFromBase64(cfg.FederationKeyKEK)
		if err != nil {
			return err
		}
		fedOpts = append(fedOpts, federation.WithCipher(cipher))
	} else if cfg.FederationEnabled {
		logger.Warn("FEDERATION_KEY_KEK unset — actor private keys are stored UNENCRYPTED; set a KEK outside dev (FEDERATION_KEY_KEK)")
	}
	fedsvc = federation.NewService(db.Queries(), fedOpts...)
	opts = append(opts, httpapi.WithFederationService(fedsvc))

	// Drain the outbound federation delivery queue in the background (signed
	// Accept/activity delivery with retry + dead-letter). Only when enabled.
	if cfg.FederationEnabled {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runFederationDeliveryWorker(workerCtx, logger, fedsvc)
		logger.Info("federation delivery worker started")
	}

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

// runFederationDeliveryWorker drains the outbound federation delivery queue on a
// ticker until ctx is canceled. A single worker suffices (deliveries claim without
// row locking); it logs only claim-query errors — per-delivery failures are
// recorded in the queue (retry/backoff/dead-letter) rather than logged.
func runFederationDeliveryWorker(ctx context.Context, logger *slog.Logger, fedsvc *federation.Service) {
	const (
		interval = 10 * time.Second
		batch    = 20
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fedsvc.DrainDeliveries(ctx, batch); err != nil {
				logger.Warn("federation delivery drain failed", "error", err)
			}
		}
	}
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
