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
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/gommon/bytes"

	"github.com/vidra/vidra-core/internal/account"
	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/block"
	"github.com/vidra/vidra-core/internal/cache"
	"github.com/vidra/vidra-core/internal/captionjob"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/quota"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/transcode"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/version"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
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
	if cfg.MailEnabled {
		// Real outbound email over SMTP. NB: host/port/from are safe to log; the
		// SMTP credentials are NOT (observability sensitive-key rules).
		smtpMailer := mail.NewSMTP(mail.Config{
			Host:         cfg.SMTPHost,
			Port:         cfg.SMTPPort,
			Username:     cfg.SMTPUsername,
			Password:     cfg.SMTPPassword,
			From:         cfg.SMTPFrom,
			InstanceName: cfg.InstanceName,
		})
		authOpts = append(authOpts, auth.WithMailer(smtpMailer))
		logger.Info("smtp mailer enabled",
			"host", cfg.SMTPHost,
			"port", cfg.SMTPPort,
			"from", cfg.SMTPFrom,
		)
	}
	var captureMailer *auth.CaptureMailer
	if cfg.DevMailCaptureEnabled {
		// The dev capture seam wins over SMTP when both are enabled (WithMailer
		// options apply in order): tokens are captured, not delivered.
		captureMailer = auth.NewCaptureMailer()
		authOpts = append(authOpts, auth.WithMailer(captureMailer))
		logger.Warn("DEV mail capture ENABLED — account-security tokens are retrievable via GET /api/v1/dev/email-token; NEVER enable this in production (DEV_MAIL_CAPTURE_ENABLED)")
	}
	// TOTP two-factor auth (P4). Shared secrets are envelope-encrypted at rest
	// with MFA_KEY_KEK (falling back to FEDERATION_KEY_KEK); without a KEK (dev)
	// they are stored raw. NB: the KEK itself is never logged.
	var mfaCipher *secretbox.Cipher
	if kek := cfg.MFAKEK(); kek != "" {
		mfaCipher, err = secretbox.NewCipherFromBase64(kek)
		if err != nil {
			return fmt.Errorf("MFA KEK: %w", err)
		}
	} else {
		logger.Warn("MFA_KEY_KEK unset — TOTP secrets are stored UNENCRYPTED; set a KEK outside dev (MFA_KEY_KEK, or share FEDERATION_KEY_KEK)")
	}
	authOpts = append(authOpts, auth.WithMFA(db.Queries(), mfaCipher, cfg.TOTPIssuer))
	authsvc := auth.NewService(db.Queries(), issuer, cfg.JWTRefreshTTL, authOpts...)
	opts = append(opts, httpapi.WithAuthService(authsvc, cfg.JWTAccessTTL))
	if captureMailer != nil {
		opts = append(opts, httpapi.WithDevMailCapture(captureMailer))
	}

	// OIDC login providers (P4). Provider discovery is lazy (first use), so a
	// temporarily unreachable IdP never blocks boot. NB: provider name/issuer
	// are safe to log; the client secret is NOT (sensitive-key rules).
	if len(cfg.OAuthProviders) > 0 {
		providers := make([]auth.OAuthProvider, 0, len(cfg.OAuthProviders))
		for _, p := range cfg.OAuthProviders {
			providers = append(providers, auth.OAuthProvider{
				Name:         p.Name,
				IssuerURL:    p.IssuerURL,
				ClientID:     p.ClientID,
				ClientSecret: p.ClientSecret,
				Scopes:       p.Scopes,
			})
			logger.Info("oauth provider configured", "provider", p.Name, "issuer", p.IssuerURL)
		}
		opts = append(opts, httpapi.WithOAuthService(auth.NewOAuthService(db.Queries(), authsvc, providers)))
	}

	channelsvc := channel.NewService(db.Queries())
	opts = append(opts, httpapi.WithChannelService(channelsvc))

	// Simple crypto donation addresses (P14). The instance identifier is bound
	// into the verification challenge message so a signature can't be replayed
	// to another instance; prefer the canonical public origin, else the name.
	donationInstance := cfg.PublicBaseURL
	if donationInstance == "" {
		donationInstance = cfg.InstanceName
	}
	donationsvc := donation.NewService(db.Queries(), donationInstance)
	opts = append(opts, httpapi.WithDonationService(donationsvc))

	blobs, err := newStorageBackend(startCtx, cfg)
	if err != nil {
		return err
	}
	// NB: endpoint/bucket/region are safe to log; the S3 credentials are NOT
	// and must never appear here (observability spec sensitive-key rules).
	if cfg.StorageBackend == "s3" {
		logger.Info("media storage configured",
			"backend", cfg.StorageBackend,
			"endpoint", cfg.StorageS3Endpoint,
			"bucket", cfg.StorageS3Bucket,
			"region", cfg.StorageS3Region,
		)
	} else {
		logger.Info("media storage configured", "backend", cfg.StorageBackend)
	}

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
	if sb, ok := media.DetectStoryboarder(blobs); ok {
		vopts = append(vopts, video.WithStoryboarder(sb))
		logger.Info("storyboard generation enabled (ffmpeg + ffprobe found)")
	} else {
		logger.Warn("storyboard generation disabled (ffmpeg/ffprobe not on PATH); videos publish without a storyboard")
	}
	vopts = append(vopts, video.WithViewDeduper(cache.NewDeduper(rdb.Client)))
	if cfg.MalwareScanEnabled {
		vopts = append(vopts, video.WithScanner(media.NewClamAV(cfg.ClamAVAddr, blobs)))
		logger.Info("malware scanning enabled (clamd)", "addr", cfg.ClamAVAddr, "mode", cfg.MalwareScanMode)
	}
	// The scan fallback policy applies whenever a scanner is wired (default
	// fail-closed); harmless to set when scanning is off.
	vopts = append(vopts, video.WithScanMode(cfg.MalwareScanMode))
	if cfg.QuarantineNewUploads {
		// §11: non-privileged uploads park in 'quarantined' until a moderator
		// approves (publish hooks fire then) or rejects them.
		vopts = append(vopts, video.WithQuarantineNewUploads(true))
		logger.Info("upload quarantine enabled (QUARANTINE_NEW_UPLOADS)")
	}
	// HLS transcoding. The read side (playlist/rendition lookups + the /hls
	// serving routes) is always wired so previously produced playlists keep
	// serving even when the pipeline is later disabled; the enqueue hook and
	// worker run only when TRANSCODING_ENABLED and ffmpeg+ffprobe are present.
	var hlsTranscoder transcode.Transcoder
	if cfg.TranscodingEnabled {
		if tc, ok := media.DetectHLSTranscoder(blobs); ok {
			tc.SetVP9(cfg.TranscodingVP9Enabled)
			hlsTranscoder = tc
			logger.Info("hls transcoding enabled (ffmpeg + ffprobe found)", "vp9", cfg.TranscodingVP9Enabled)
		} else {
			logger.Warn("TRANSCODING_ENABLED=true but ffmpeg/ffprobe not on PATH; transcoding disabled")
		}
	}
	transcodesvc := transcode.NewService(db.Queries(), hlsTranscoder)
	opts = append(opts, httpapi.WithTranscodeService(transcodesvc))
	if hlsTranscoder != nil {
		vopts = append(vopts, video.WithTranscodeHook(func(ctx context.Context, videoID uuid.UUID, sourceKey string) {
			// Best-effort: an enqueue failure must never block the publish.
			if err := transcodesvc.Enqueue(ctx, videoID, sourceKey); err != nil {
				logger.Warn("transcode enqueue failed", "video_id", videoID, "error", err)
			}
		}))
	}
	// When federation is on, fan a published video out to the channel's remote
	// followers. fedsvc is assigned below; the hook only runs post-startup so the
	// closure sees the built service (nil-guarded regardless).
	var fedsvc *federation.Service
	if cfg.FederationEnabled {
		vopts = append(vopts,
			video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
				if fedsvc != nil {
					if err := fedsvc.AnnounceVideo(ctx, videoID); err != nil {
						logger.Warn("federation announce failed", "video_id", videoID, "error", err)
					}
				}
			}),
			video.WithUpdateHook(func(ctx context.Context, videoID uuid.UUID) {
				if fedsvc != nil {
					if err := fedsvc.UpdateVideo(ctx, videoID); err != nil {
						logger.Warn("federation update failed", "video_id", videoID, "error", err)
					}
				}
			}),
			video.WithDeleteHook(func(ctx context.Context, videoID, channelID uuid.UUID, wasPublic bool) {
				if fedsvc != nil {
					if err := fedsvc.DeleteVideo(ctx, videoID, channelID, wasPublic); err != nil {
						logger.Warn("federation delete failed", "video_id", videoID, "error", err)
					}
				}
			}),
		)
	}
	videosvc := video.NewService(db.Queries(), blobs, vopts...)
	opts = append(opts, httpapi.WithVideoService(videosvc), httpapi.WithMediaStorage(blobs))

	// When federation is on, fan a local comment on a local video out to the
	// channel's remote followers as Create/Update/Delete{Note} (remote-content
	// §6) — same deferred-fedsvc seam as the video hooks above.
	var commentOpts []comment.Option
	if cfg.FederationEnabled {
		commentOpts = append(commentOpts,
			comment.WithCreateHook(func(ctx context.Context, commentID uuid.UUID) {
				if fedsvc != nil {
					if err := fedsvc.AnnounceComment(ctx, commentID); err != nil {
						logger.Warn("federation comment announce failed", "comment_id", commentID, "error", err)
					}
				}
			}),
			comment.WithUpdateHook(func(ctx context.Context, commentID uuid.UUID) {
				if fedsvc != nil {
					if err := fedsvc.UpdateComment(ctx, commentID); err != nil {
						logger.Warn("federation comment update failed", "comment_id", commentID, "error", err)
					}
				}
			}),
			comment.WithDeleteHook(func(ctx context.Context, commentID, videoID, authorID uuid.UUID) {
				if fedsvc != nil {
					if err := fedsvc.DeleteComment(ctx, commentID, videoID, authorID); err != nil {
						logger.Warn("federation comment delete failed", "comment_id", commentID, "error", err)
					}
				}
			}),
		)
	}
	commentsvc := comment.NewService(db.Queries(), commentOpts...)
	opts = append(opts, httpapi.WithCommentService(commentsvc))

	ratingsvc := rating.NewService(db.Queries())
	opts = append(opts, httpapi.WithRatingService(ratingsvc))

	notifsvc := notification.NewService(db.Queries())
	opts = append(opts, httpapi.WithNotificationService(notifsvc))

	playlistsvc := playlist.NewService(db.Queries(), playlist.WithStorage(blobs))
	opts = append(opts, httpapi.WithPlaylistService(playlistsvc))

	// Media garbage collection: admin-triggered sweep + a daily scheduled sweep.
	mediagcsvc := mediagc.NewService(db.Queries(), blobs)
	opts = append(opts, httpapi.WithMediaGCService(mediagcsvc))

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

	// E2EE (P11.2): ciphertext-only encrypted messaging. The server is a dumb
	// key directory + envelope store — no cryptography here. Blocks apply
	// unchanged via the same Blocker as plaintext messaging.
	e2eesvc := e2ee.NewService(db.Queries(), e2ee.WithBlocker(blocksvc))
	opts = append(opts, httpapi.WithE2EEService(e2eesvc))

	// Live streaming. When a media server volume is configured (LIVE_HLS_ROOT),
	// the api serves live HLS by stream ID from it and — for replay-enabled
	// streams — republishes a finished session's recording as a normal VOD
	// through the shared video pipeline (best-effort, audited). Without the root,
	// replay stays dormant and live HLS serving 404s.
	liveOpts := []live.Option{live.WithLogger(logger)}
	if cfg.LiveHLSRoot != "" {
		liveOpts = append(liveOpts,
			live.WithReplayPipeline(videosvc),
			live.WithRecordingStore(live.NewDirRecordingStore(cfg.LiveHLSRoot)),
			live.WithAuditor(auditsvc),
		)
		logger.Info("live HLS serving + replay-to-VOD enabled", "hls_root", cfg.LiveHLSRoot)
	}
	livesvc := live.NewService(db.Queries(), liveOpts...)
	opts = append(opts, httpapi.WithLiveService(livesvc))

	imagesvc := profileimage.NewService(db.Queries(), blobs)
	opts = append(opts, httpapi.WithProfileImageService(imagesvc))

	// Per-user storage quotas: usage is aggregated live from video_files;
	// uploads/imports that would exceed the effective quota get 422.
	quotasvc := quota.NewService(db.Queries(), cfg.InstanceDefaultQuotaBytes)
	opts = append(opts, httpapi.WithQuotaService(quotasvc))
	if cfg.InstanceDefaultQuotaBytes > 0 {
		logger.Info("default per-user storage quota enabled", "bytes", cfg.InstanceDefaultQuotaBytes)
	}

	// Resumable/chunked upload sessions (P6.1). Chunk bytes go to the same blob
	// backend at uploads/<session>/<n>; completion assembles them through the
	// same AttachOriginal → Process pipeline as a direct upload, and a background
	// sweeper cleans up expired/cancelled sessions' chunks (failed-upload cleanup).
	uploadsvc := upload.NewService(db.Queries(), blobs)
	opts = append(opts, httpapi.WithUploadService(uploadsvc))

	// Asynchronous URL import (P2.2). POST /videos/:id/import now enqueues a job
	// and returns 202; a background worker performs the SSRF-guarded fetch and
	// runs it through the same pipeline, with the same UPLOAD_MAX_SIZE cap and
	// per-user quota enforcement the synchronous path had.
	importMaxBytes, _ := bytes.Parse(cfg.UploadMaxSize) // validated at startup
	importsvc := videoimport.NewService(db.Queries(), videosvc, importMaxBytes,
		videoimport.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
		videoimport.WithQuota(quotasvc),
		videoimport.WithLogger(logger),
	)
	opts = append(opts, httpapi.WithVideoImportService(importsvc))

	// Auto-caption / Whisper (P13). The endpoints are ALWAYS mounted (so the
	// documented contract is stable); the service's Enabled() flag gates the
	// request endpoint (503 when off) and the worker. When WHISPER_ENABLED, the
	// transcriber extracts audio via ffmpeg and POSTs it to WHISPER_ENDPOINT.
	var transcriber captionjob.Transcriber
	if cfg.WhisperEnabled {
		if _, lookErr := exec.LookPath("ffmpeg"); lookErr != nil {
			logger.Warn("WHISPER_ENABLED but ffmpeg is not on PATH — auto-caption jobs will fail until it is installed")
		}
		transcriber = media.NewWhisperClient(blobs, cfg.WhisperEndpoint)
	}
	captionjobsvc := captionjob.NewService(db.Queries(), videosvc, transcriber,
		captionjob.WithEnabled(cfg.WhisperEnabled),
		captionjob.WithDefaultLanguage(cfg.WhisperDefaultLanguage),
		captionjob.WithNotifier(notifsvc),
		captionjob.WithLogger(logger),
	)
	opts = append(opts, httpapi.WithCaptionJobService(captionjobsvc))

	// Account lifecycle (P4 export/import + §1 hard delete). Deleting an
	// account removes its videos through the video service, so the federation
	// Delete hooks registered above fire for previously-public videos.
	accountsvc := account.NewService(db.Queries(), blobs, videosvc,
		account.WithBaseURL(cfg.PublicBaseURL))
	opts = append(opts, httpapi.WithAccountService(accountsvc))

	// Federation (ActivityPub) — the service is always constructed, but its routes
	// mount only when FEDERATION_ENABLED (gated in httpapi.routes). Actor private
	// keys are envelope-encrypted with FEDERATION_KEY_KEK; without it (dev) they are
	// stored raw. See .ralph/specs/federation.md.
	fedOpts := []federation.Option{
		federation.WithBaseURL(cfg.PublicBaseURL),
		// Reuse the outbound-fetch dev knob: when set, remote-actor fetches may reach
		// loopback/private origins (dev/e2e only; never in production).
		federation.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
		// Best-effort remote thumbnail cache (remote-content §5) shares the media
		// blob backend. The inbound Create/Announce ingestion gate consults the
		// repository's accepted remote_channel_follows edges by default (§2) —
		// no extra wiring needed.
		federation.WithMediaStorage(blobs),
		// Inbound federated comments run the same watched-words flagging as
		// local ones (remote-content §6).
		federation.WithCommentFlagger(watchwordsvc),
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

	// Remote-video read side (metadata + cached thumbnail) and instance-level
	// moderation (per-user instance mutes + admin blocklist). REST surface, so
	// wired unconditionally — the tables are simply empty until federation
	// ingests content.
	remotevideosvc := remotevideo.NewService(db.Queries(), blobs)
	opts = append(opts, httpapi.WithRemoteVideoService(remotevideosvc))
	instancemodsvc := instancemod.NewService(db.Queries())
	opts = append(opts, httpapi.WithInstanceModerationService(instancemodsvc))

	// Drain the outbound federation delivery queue in the background (signed
	// Accept/activity delivery with retry + dead-letter). Only when enabled.
	if cfg.FederationEnabled {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runFederationDeliveryWorker(workerCtx, logger, fedsvc)
		logger.Info("federation delivery worker started")
	}

	// Drain the transcode job queue in the background (ffmpeg HLS ladder with
	// retry + dead-letter). Only when the transcoder is available.
	if hlsTranscoder != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runTranscodeWorker(workerCtx, logger, transcodesvc)
		logger.Info("transcode worker started")
	}

	// Drain the account-export job queue and sweep expired archives in the
	// background (always on: the due/expiry scans are cheap partial-index
	// lookups and exports must work on every instance).
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runAccountExportWorker(workerCtx, logger, accountsvc)
		logger.Info("account export worker started")
	}

	// Hard-delete expired disappearing E2EE messages in the background (always
	// on: the expiry scan is a cheap partial-index lookup; reads additionally
	// filter expired rows so expiry is correct between sweeps).
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runE2EESweepWorker(workerCtx, logger, e2eesvc)
		logger.Info("e2ee expiry sweep worker started")
	}

	// Publish scheduled videos as they come due (product-decisions §17). Always
	// on: the due scan is a cheap partial-index lookup, and the transition runs
	// the same publish hooks (federation announce, transcode enqueue) as a
	// direct publish.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runScheduledPublishWorker(workerCtx, logger, videosvc)
		logger.Info("scheduled publish worker started")
	}

	// Drain the URL-import queue in the background (SSRF-guarded fetch → the same
	// AttachOriginal → Process pipeline, with retry + dead-letter). Always on: the
	// due scan is a cheap partial-index lookup.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runVideoImportWorker(workerCtx, logger, importsvc)
		logger.Info("video import worker started")
	}

	// Drain the auto-caption (Whisper) queue in the background: extract audio →
	// transcribe → upsert the caption via the shared AddCaption path → notify the
	// owner, with retry + dead-letter. Only when auto-captioning is enabled.
	if captionjobsvc.Enabled() {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runCaptionJobWorker(workerCtx, logger, captionjobsvc)
		logger.Info("auto-caption worker started")
	}

	// Sweep expired/cancelled resumable-upload sessions (the failed-upload
	// cleanup): removes the chunk blobs then the row. Always on: the sweep scan
	// is a cheap indexed lookup.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runUploadSweepWorker(workerCtx, logger, uploadsvc)
		logger.Info("upload session sweep worker started")
	}

	// Media garbage collection: a daily sweep of orphaned storage blobs. Always
	// on (dry-run-free deletion) — the reference queries are cheap and the sweep
	// never touches unknown prefixes.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runMediaGCWorker(workerCtx, logger, mediagcsvc, auditsvc)
		logger.Info("media gc worker started")
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

// runTranscodeWorker drains the durable transcode job queue on a ticker until
// ctx is canceled (mirrors runFederationDeliveryWorker). A single worker with a
// small batch keeps at most a couple of ffmpeg runs in flight per tick; it logs
// only claim-query errors — per-job failures are recorded in the queue
// (retry/backoff/dead-letter) rather than logged.
func runTranscodeWorker(ctx context.Context, logger *slog.Logger, svc *transcode.Service) {
	const (
		interval = 10 * time.Second
		batch    = 2
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Drain the whole due backlog, batch by batch, so a burst of
			// uploads doesn't wait a tick per pair of jobs. DrainJobs only
			// counts completions, so a persistently failing job ends the
			// inner loop and backoff retries it on a later tick.
			total := 0
			for {
				n, err := svc.DrainJobs(ctx, batch)
				if err != nil {
					logger.Warn("transcode drain failed", "error", err)
					break
				}
				total += n
				if n == 0 {
					break
				}
			}
			if total > 0 {
				logger.Info("transcode drain completed jobs", "count", total)
			}
		}
	}
}

// runAccountExportWorker drains the durable account-export queue and sweeps
// expired archives on a ticker until ctx is canceled (mirrors
// runTranscodeWorker). Per-job failures are recorded in the queue
// (retry/backoff/dead-letter); only the claim/sweep query errors are logged.
func runAccountExportWorker(ctx context.Context, logger *slog.Logger, svc *account.Service) {
	const (
		interval   = 10 * time.Second
		batch      = 2
		sweepBatch = 50
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := svc.DrainExports(ctx, batch); err != nil {
				logger.Warn("account export drain failed", "error", err)
			} else if n > 0 {
				logger.Info("account export drain completed jobs", "count", n)
			}
			if n, err := svc.SweepExpiredExports(ctx, sweepBatch); err != nil {
				logger.Warn("account export sweep failed", "error", err)
			} else if n > 0 {
				logger.Info("account export sweep removed expired archives", "count", n)
			}
		}
	}
}

// runE2EESweepWorker hard-deletes expired disappearing E2EE messages on a
// ticker until ctx is canceled (mirrors runTranscodeWorker). Only the sweep
// query error is logged — never message contents (which are ciphertext anyway).
func runE2EESweepWorker(ctx context.Context, logger *slog.Logger, svc *e2ee.Service) {
	const (
		interval   = 10 * time.Second
		sweepBatch = 200
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := svc.SweepExpired(ctx, sweepBatch); err != nil {
				logger.Warn("e2ee expiry sweep failed", "error", err)
			} else if n > 0 {
				logger.Info("e2ee expiry sweep removed expired messages", "count", n)
			}
		}
	}
}

// runScheduledPublishWorker transitions scheduled videos to published as their
// publish_at comes due, on a ticker until ctx is canceled (mirrors
// runTranscodeWorker). PublishDue runs each due video through the same publish
// transition Process uses, so the federation-announce and transcode-enqueue
// hooks fire exactly as they would on a direct publish. Per-video failures stay
// 'scheduled' and are retried next tick; only the claim-query error is logged.
func runScheduledPublishWorker(ctx context.Context, logger *slog.Logger, svc *video.Service) {
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
			n, err := svc.PublishDue(ctx, batch)
			if err != nil {
				logger.Warn("scheduled publish sweep failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("scheduled publish sweep published videos", "count", n)
			}
		}
	}
}

// runVideoImportWorker drains the durable URL-import queue on a ticker until
// ctx is canceled (mirrors runTranscodeWorker). Per-job failures are recorded
// in the queue (retry/backoff/dead-letter); only the claim-query error is logged.
func runVideoImportWorker(ctx context.Context, logger *slog.Logger, svc *videoimport.Service) {
	const (
		interval = 10 * time.Second
		batch    = 2
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total := 0
			for {
				n, err := svc.DrainJobs(ctx, batch)
				if err != nil {
					logger.Warn("video import drain failed", "error", err)
					break
				}
				total += n
				if n == 0 {
					break
				}
			}
			if total > 0 {
				logger.Info("video import drain completed jobs", "count", total)
			}
		}
	}
}

// runCaptionJobWorker drains the durable auto-caption (Whisper) queue on a ticker
// until ctx is canceled (mirrors runTranscodeWorker). A small batch keeps at most
// a couple of ffmpeg-extract + transcription round-trips in flight per tick;
// per-job failures are recorded in the queue (retry/backoff/dead-letter) rather
// than logged.
func runCaptionJobWorker(ctx context.Context, logger *slog.Logger, svc *captionjob.Service) {
	const (
		interval = 10 * time.Second
		batch    = 2
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total := 0
			for {
				n, err := svc.DrainJobs(ctx, batch)
				if err != nil {
					logger.Warn("auto-caption drain failed", "error", err)
					break
				}
				total += n
				if n == 0 {
					break
				}
			}
			if total > 0 {
				logger.Info("auto-caption drain completed jobs", "count", total)
			}
		}
	}
}

// runUploadSweepWorker deletes expired/cancelled resumable-upload sessions and
// their chunk blobs on a ticker until ctx is canceled (mirrors the account
// export sweep). Only the sweep-query error is logged.
func runUploadSweepWorker(ctx context.Context, logger *slog.Logger, svc *upload.Service) {
	const (
		interval   = time.Minute
		sweepBatch = 50
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := svc.Sweep(ctx, sweepBatch); err != nil {
				logger.Warn("upload session sweep failed", "error", err)
			} else if n > 0 {
				logger.Info("upload session sweep removed sessions", "count", n)
			}
		}
	}
}

// runMediaGCWorker runs the media garbage collector once a day (deleting
// orphaned storage blobs) until ctx is canceled. Each run is audited with its
// counts (actor "" = system). A listing-unsupported backend disables the sweep
// after one warning.
func runMediaGCWorker(ctx context.Context, logger *slog.Logger, svc *mediagc.Service, auditsvc *audit.Service) {
	const interval = 24 * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, err := svc.Sweep(ctx, false)
			if err != nil {
				if errors.Is(err, mediagc.ErrListingUnsupported) {
					logger.Warn("media gc disabled: storage backend does not support listing")
					return
				}
				logger.Warn("media gc sweep failed", "error", err)
				continue
			}
			logger.Info("media gc sweep completed", "scanned", res.Scanned, "orphans", len(res.Orphans), "deleted", res.Deleted)
			if auditsvc != nil {
				_ = auditsvc.Record(ctx, audit.Event{
					Action: observability.ActionMediaGC,
					Result: observability.ResultSuccess,
					Reason: fmt.Sprintf("mode=delete scanned=%d orphans=%d deleted=%d", res.Scanned, len(res.Orphans), res.Deleted),
				})
			}
		}
	}
}

// newStorageBackend builds the media blob backend selected by config. Config
// validation already restricts StorageBackend to the supported set, so the
// default branch is a defensive guard. ctx bounds the s3 startup probe
// (EnsureBucket) so an unreachable store fails fast like a missing DB.
func newStorageBackend(ctx context.Context, cfg *config.Config) (storage.Backend, error) {
	switch cfg.StorageBackend {
	case "local":
		return storage.NewLocal(cfg.StorageLocalRoot)
	case "s3":
		s3b, err := storage.NewS3(storage.S3Config{
			Endpoint:       cfg.StorageS3Endpoint,
			Bucket:         cfg.StorageS3Bucket,
			AccessKey:      cfg.StorageS3AccessKey,
			SecretKey:      cfg.StorageS3SecretKey,
			Region:         cfg.StorageS3Region,
			UseSSL:         cfg.StorageS3UseSSL,
			ForcePathStyle: cfg.StorageS3ForcePathStyle,
		})
		if err != nil {
			return nil, err
		}
		if err := s3b.EnsureBucket(ctx); err != nil {
			return nil, err
		}
		return s3b, nil
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}
