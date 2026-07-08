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
	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/block"
	"github.com/vidra/vidra-core/internal/cache"
	"github.com/vidra/vidra-core/internal/captionjob"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/channelsync"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/linkpreview"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/peertubeimport"
	"github.com/vidra/vidra-core/internal/playersettings"
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
	"github.com/vidra/vidra-core/internal/urlsafety"
	"github.com/vidra/vidra-core/internal/version"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/watchword"
	"github.com/vidra/vidra-core/internal/ytdlp"
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
	// Datastore + outbound-HTTP instrumentation (P17.3). Enabled only with OTel so
	// it is genuinely zero-cost off: pgx query spans, Redis command spans, and the
	// outbound client-span + W3C traceparent injection on EVERY SSRF-guarded client
	// (federation/import/whisper/atproto/link-preview) via one wrapper seam.
	var storeOpts []store.Option
	var cacheOpts []cache.Option
	if cfg.OTelEnabled {
		storeOpts = append(storeOpts, store.WithTracing())
		cacheOpts = append(cacheOpts, cache.WithTracing())
		urlsafety.SetTransportWrapper(observability.OTelHTTPTransport)
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

	db, err := store.New(startCtx, cfg.DatabaseURL, storeOpts...)
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("connected to postgres")

	rdb, err := cache.New(startCtx, cfg.RedisURL, cacheOpts...)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()
	logger.Info("connected to redis")

	// DB-backed instance-settings overlay (fix_plan P10): loads the mutable-key
	// overrides at boot and caches them. GET /instance and the
	// registration/upload/import/live/comment gates consult its effective values
	// (DB override, else the config default supplied here). A boot load failure is
	// fatal — the overlay must be authoritative before serving traffic.
	settingssvc := instancesettings.NewService(db.Queries(), instancesettings.Defaults{
		InstanceName:                cfg.InstanceName,
		InstanceDescription:         cfg.InstanceDescription,
		TermsURL:                    cfg.InstanceTermsURL,
		PrivacyURL:                  cfg.InstancePrivacyURL,
		ContactEmail:                cfg.InstanceContactEmail,
		RegistrationEnabled:         cfg.RegistrationEnabled,
		RegistrationRequireApproval: cfg.RegistrationRequireApproval,
		QuarantineNewUploads:        cfg.QuarantineNewUploads,
		UploadsEnabled:              cfg.UploadsEnabled,
		ImportsEnabled:              cfg.ImportsEnabled,
		LiveEnabled:                 cfg.LiveEnabled,
		CommentsEnabled:             cfg.CommentsEnabled,
	})
	if err := settingssvc.Load(startCtx); err != nil {
		return err
	}
	opts = append(opts, httpapi.WithSettingsService(settingssvc))

	if cfg.RateLimitEnabled {
		counter := ratelimit.NewRedisCounter(rdb.Client)
		limiter := ratelimit.NewLimiter(counter, cfg.RateLimitRequests, cfg.RateLimitWindow)
		authLimiter := ratelimit.NewLimiter(counter, cfg.AuthRateLimitRequests, cfg.RateLimitWindow)
		// Per-user DM attachment upload limiter (messaging-v2.md D6): the
		// compensating anti-abuse control now that DM attachments do not count
		// against the storage quota. Keyed by user id, its own (longer) window.
		attachLimiter := ratelimit.NewLimiter(counter, cfg.AttachmentUploadRateLimitRequests, cfg.AttachmentUploadRateLimitWindow)
		opts = append(opts,
			httpapi.WithRateLimiter(limiter),
			httpapi.WithAuthRateLimiter(authLimiter),
			httpapi.WithAttachmentRateLimiter(attachLimiter),
		)
		logger.Info("rate limiting enabled",
			"requests", cfg.RateLimitRequests,
			"auth_requests", cfg.AuthRateLimitRequests,
			"window", cfg.RateLimitWindow,
			"attachment_upload_requests", cfg.AttachmentUploadRateLimitRequests,
			"attachment_upload_window", cfg.AttachmentUploadRateLimitWindow,
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

	// Hybrid IPFS media mirror (fix_plan P19, .ralph/specs/ipfs-media.md). A MIRROR
	// SIDECAR — local/S3 stays authoritative — that add+pins ALREADY-PUBLIC media
	// to a Kubo node when IPFS_ENABLED. It is constructed unconditionally so every
	// producing service can hold the hook; when disabled every method is an inert
	// no-op (and the Kubo client is nil). The eligibility gate is the privacy fence
	// — nothing non-public is ever enqueued.
	var ipfsClient ipfs.Client
	var ipfsCluster ipfs.ClusterClient
	if cfg.IPFSEnabled {
		ipfsClient = ipfs.NewKuboClient(cfg.IPFSAPIURL, &http.Client{Timeout: cfg.IPFSAddTimeout})
		// Optional IPFS Cluster replication (STOR-05). Best-effort — the local node
		// pin is the authoritative mirror action; the cluster replicates it. The
		// token is a SECRET (Bearer), never logged.
		if cfg.IPFSClusterAPIURL != "" {
			ipfsCluster = ipfs.NewKuboClusterClient(cfg.IPFSClusterAPIURL, cfg.IPFSClusterToken, &http.Client{Timeout: cfg.IPFSAddTimeout})
		}
	}
	// Private mirroring tier (P19.P1): a SECOND, fully separate swarm.key'd node —
	// NEVER dual-homed with the public node (config validation rejects a shared URL).
	// The mirror worker routes each ledger row to exactly one network and can never
	// pin a private row through the public client (spec §1/§8). Present only when
	// IPFS_MIRROR_PRIVATE=true with a dedicated IPFS_PRIVATE_API_URL.
	var ipfsPrivateClient ipfs.Client
	var ipfsPrivateCluster ipfs.ClusterClient
	privateEnabled := cfg.IPFSMirrorPrivate && cfg.IPFSPrivateAPIURL != ""
	if privateEnabled {
		ipfsPrivateClient = ipfs.NewKuboClient(cfg.IPFSPrivateAPIURL, &http.Client{Timeout: cfg.IPFSPrivateAddTimeout})
		if cfg.IPFSPrivateClusterAPIURL != "" {
			ipfsPrivateCluster = ipfs.NewKuboClusterClient(cfg.IPFSPrivateClusterAPIURL, cfg.IPFSPrivateClusterToken, &http.Client{Timeout: cfg.IPFSPrivateAddTimeout})
		}
	}
	// One SQLLookups value serves both the per-entity Lookups (enqueue hooks) and
	// the bulk Catalog (the one-shot admin backfill, P19.6).
	ipfsLookups := ipfsmirror.NewSQLLookups(db.Queries())
	ipfsMirror := ipfsmirror.New(
		db.Queries(),
		ipfsLookups,
		blobs,
		ipfsClient,
		ipfsmirror.Config{
			Enabled:        cfg.IPFSEnabled,
			GatewayURL:     cfg.IPFSGatewayURL,
			ClusterEnabled: cfg.IPFSClusterAPIURL != "",
			AddTimeout:     cfg.IPFSAddTimeout,
			Concurrency:    cfg.IPFSPinConcurrency,
			Logger:         logger,
			Cluster:        ipfsCluster,
			Catalog:        ipfsLookups,
			// Private tier (P19.P1) — a dedicated private-swarm node client the worker
			// routes network='private' rows to (and ONLY those rows).
			PrivateEnabled:     privateEnabled,
			PrivateClient:      ipfsPrivateClient,
			PrivateCluster:     ipfsPrivateCluster,
			PrivateAddTimeout:  cfg.IPFSPrivateAddTimeout,
			PrivateConcurrency: cfg.IPFSPrivatePinConcurrency,
		},
	)
	opts = append(opts, httpapi.WithIPFSMirrorService(ipfsMirror))
	if cfg.IPFSEnabled {
		logger.Info("ipfs media mirror enabled", "gateway", cfg.IPFSGatewayURL, "cluster", cfg.IPFSClusterAPIURL != "")
	}
	if privateEnabled {
		// Never log the private API URL at info (it is infrastructure topology); the
		// presence + cluster flag is enough. The swarm.key/cluster token are secrets.
		logger.Info("ipfs private mirror enabled", "cluster", cfg.IPFSPrivateClusterAPIURL != "")
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
	// The durable audit trail (also mounted on the admin API below) lets the video
	// service record a content.upload.malware_rejected event when a scan keeps an
	// upload out of published (infection / unscannable under a non-publishing mode).
	auditsvc := audit.NewService(db.Queries())
	vopts = append(vopts, video.WithAuditor(auditsvc))
	if cfg.MalwareScanEnabled {
		vopts = append(vopts, video.WithScanner(media.NewClamAV(cfg.ClamAVAddr, blobs, cfg.ClamAVTimeout)))
		logger.Info("malware scanning enabled (clamd)", "addr", cfg.ClamAVAddr, "mode", cfg.MalwareScanMode, "timeout", cfg.ClamAVTimeout.String())
	}
	// The scan fallback policy applies whenever a scanner is wired (default
	// fail-closed); harmless to set when scanning is off.
	vopts = append(vopts, video.WithScanMode(cfg.MalwareScanMode))
	// §11 + P10 overlay: non-privileged uploads park in 'quarantined' until a
	// moderator approves (publish hooks fire then) or rejects them. The gate is
	// consulted per Process from the instance-settings overlay so an admin can
	// toggle it at runtime; with no DB override it returns cfg.QuarantineNewUploads.
	vopts = append(vopts, video.WithQuarantineGate(func() bool {
		return settingssvc.Bool(instancesettings.KeyQuarantineNewUploads)
	}))
	if cfg.QuarantineNewUploads {
		logger.Info("upload quarantine enabled by default (QUARANTINE_NEW_UPLOADS)")
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
	// IPFS mirror (P19.4): on transcode completion, add+pin the finalized VOD HLS
	// tree as one directory (car_root) row and refresh the VP9/WebM alternate — for
	// eligible (public+published) videos only; re-checked in the hook. Best-effort;
	// registered only when IPFS_ENABLED to avoid a no-op hook.
	var tcopts []transcode.Option
	if cfg.IPFSEnabled || privateEnabled {
		tcopts = append(tcopts, transcode.WithCompletionHook(func(ctx context.Context, videoID uuid.UUID) {
			if err := ipfsMirror.OnTranscodeComplete(ctx, videoID); err != nil {
				logger.Warn("ipfs mirror transcode-complete sync failed", "video_id", videoID, "error", err)
			}
		}))
	}
	transcodesvc := transcode.NewService(db.Queries(), hlsTranscoder, tcopts...)
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
	// When ATProto is on, enqueue an auto cross-post for a published PUBLIC video
	// whose owner has auto_post. Same deferred-service seam as fedsvc: atprotosvc
	// is assigned below and the closure is nil-guarded. Publish hooks are additive
	// (video.WithPublishHook appends), so federation + ATProto register
	// independently and both run.
	var atprotosvc *atproto.Service
	if cfg.ATProtoEnabled {
		vopts = append(vopts, video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
			if atprotosvc != nil {
				if err := atprotosvc.EnqueueForVideo(ctx, videoID); err != nil {
					logger.Warn("atproto auto-post enqueue failed", "video_id", videoID, "error", err)
				}
			}
		}))
	}
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
	// IPFS mirror hooks (fix_plan P19): on publish and on metadata update, re-evaluate
	// the video's image-derivative pins (thumbnail/storyboard/captions) against its
	// current privacy+state (public+published pins, anything else unpins); on delete,
	// unpin all of the video's ledger rows (reference-checked in the worker). Video
	// originals + VP9 (P19.3) and the HLS tree (P19.4) extend these same transitions.
	// Under P19.P2 a privacy flip TRANSITIONS the video's rows across swarms, so these
	// hooks run whenever EITHER tier is on (a private-only deployment mirrors non-public
	// media through the same SyncVideo path).
	if cfg.IPFSEnabled || privateEnabled {
		vopts = append(vopts,
			video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
				if err := ipfsMirror.SyncVideo(ctx, videoID); err != nil {
					logger.Warn("ipfs mirror publish sync failed", "video_id", videoID, "error", err)
				}
			}),
			video.WithUpdateHook(func(ctx context.Context, videoID uuid.UUID) {
				if err := ipfsMirror.SyncVideo(ctx, videoID); err != nil {
					logger.Warn("ipfs mirror update sync failed", "video_id", videoID, "error", err)
				}
			}),
			video.WithDeleteHook(func(ctx context.Context, videoID, channelID uuid.UUID, wasPublic bool) {
				if err := ipfsMirror.UnpinVideo(ctx, videoID); err != nil {
					logger.Warn("ipfs mirror delete unpin failed", "video_id", videoID, "error", err)
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

	playersettingssvc := playersettings.NewService(db.Queries())
	opts = append(opts, httpapi.WithPlayerSettingsService(playersettingssvc))

	playlistsvc := playlist.NewService(db.Queries(), playlist.WithStorage(blobs), playlist.WithMirror(ipfsMirror))
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

	opts = append(opts, httpapi.WithAuditLog(auditsvc))

	// DM completeness (product-decisions.md §14): attachments (scanned fail-closed
	// when clamd is configured) and SSRF-guarded, best-effort link previews.
	msgOpts := []messaging.Option{messaging.WithBlocker(blocksvc), messaging.WithLogger(logger)}
	var attachScanner messaging.Scanner
	if cfg.MalwareScanEnabled {
		attachScanner = media.NewClamAV(cfg.ClamAVAddr, blobs, cfg.ClamAVTimeout)
	}
	msgOpts = append(msgOpts, messaging.WithAttachments(blobs, attachScanner, messaging.MaxAttachmentBytes))
	previewGuard := urlsafety.Guard{AllowPrivate: cfg.ImportAllowPrivateURLs}
	msgOpts = append(msgOpts, messaging.WithPreviews(linkpreview.NewFetcher(previewGuard, linkpreview.DefaultMaxBytes)))
	messagingsvc := messaging.NewService(db.Queries(), msgOpts...)
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

	imagesvc := profileimage.NewService(db.Queries(), blobs, profileimage.WithMirror(ipfsMirror))
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
	uploadsvc := upload.NewService(db.Queries(), blobs,
		upload.WithMaxActiveSessions(cfg.UploadMaxActiveSessionsPerUser))
	opts = append(opts, httpapi.WithUploadService(uploadsvc))

	// Asynchronous URL import (P2.2). POST /videos/:id/import now enqueues a job
	// and returns 202; a background worker performs the SSRF-guarded fetch and
	// runs it through the same pipeline, with the same UPLOAD_MAX_SIZE cap and
	// per-user quota enforcement the synchronous path had.
	importMaxBytes, _ := bytes.Parse(cfg.UploadMaxSize) // validated at startup
	importOpts := []videoimport.Option{
		videoimport.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
		videoimport.WithQuota(quotasvc),
		videoimport.WithLogger(logger),
	}
	// yt-dlp platform-URL import (W2.C1, UPLOAD-09). OFF by default; admin opt-in.
	// The binary is pinned in the image (never self-updated at runtime); when the
	// flag is on but the binary is missing, imports still enqueue and fail SAFELY
	// per job (no crash). See .ralph/specs/backport-w2-upload-import.md §5. The
	// same sandboxed client backs both the import resolver and the channel-sync
	// lister below.
	var ytdlpClient *ytdlp.Client
	if cfg.YtdlpImportEnabled {
		if _, lookErr := exec.LookPath(cfg.YtdlpPath); lookErr != nil {
			logger.Warn("YTDLP_IMPORT_ENABLED but yt-dlp is not on PATH — platform imports will fail until it is installed", "path", cfg.YtdlpPath)
		}
		ytdlpClient = ytdlp.New(ytdlp.Config{
			Path:      cfg.YtdlpPath,
			Timeout:   cfg.YtdlpTimeout,
			Proxy:     cfg.YtdlpProxy,
			MaxHeight: cfg.YtdlpMaxHeight,
			MaxBytes:  importMaxBytes,
		})
		importOpts = append(importOpts, videoimport.WithYtdlp(ytdlpClient, ""))
		logger.Info("yt-dlp platform-URL import enabled", "max_height", cfg.YtdlpMaxHeight, "proxy_set", cfg.YtdlpProxy != "")
	}
	importsvc := videoimport.NewService(db.Queries(), videosvc, importMaxBytes, importOpts...)
	opts = append(opts, httpapi.WithVideoImportService(importsvc))

	// Channel auto-sync (W2.C4, UPLOAD-13). Effective only when CHANNEL_SYNC_ENABLED
	// AND the yt-dlp import resolver are both on (the sync path IS a yt-dlp import
	// path). The HTTP surface is ALWAYS mounted so the contract is stable; the
	// service's Enabled() flag 503s create/sync-now and short-circuits the worker
	// when off. Synced uploads land as PRIVATE drafts + `ytdlp` imports, so bytes
	// still flow through AttachOriginal → Process (scan hook) — no new write path.
	channelSyncEffective := cfg.ChannelSyncEnabled && cfg.YtdlpImportEnabled
	if cfg.ChannelSyncEnabled && !cfg.YtdlpImportEnabled {
		logger.Warn("CHANNEL_SYNC_ENABLED but YTDLP_IMPORT_ENABLED is off — channel auto-sync stays disabled (it requires the yt-dlp import resolver)")
	}
	channelSyncOpts := []channelsync.Option{
		channelsync.WithEnabled(channelSyncEffective),
		channelsync.WithAllowPrivateURLs(cfg.ImportAllowPrivateURLs),
		channelsync.WithMaxPerUser(cfg.ChannelSyncMaxPerUser),
		channelsync.WithBatch(cfg.ChannelSyncBatch),
		channelsync.WithInterval(cfg.ChannelSyncInterval),
		channelsync.WithLogger(logger),
	}
	if ytdlpClient != nil {
		channelSyncOpts = append(channelSyncOpts, channelsync.WithLister(ytdlpClient))
	}
	channelsyncsvc := channelsync.NewService(db.Queries(), videosvc, importsvc, channelSyncOpts...)
	opts = append(opts, httpapi.WithChannelSyncService(channelsyncsvc))

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
		account.WithBaseURL(cfg.PublicBaseURL),
		account.WithMirror(ipfsMirror)) // IPFS P19: unpin a deleted account's media
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

	// ATProto / Bluesky extension (P10.2, .ralph/specs/atproto.md) — v1 outbound
	// cross-posting only. Always constructed so the /me/atproto routes are a stable
	// contract; the Enabled() flag (ATPROTO_ENABLED) gates them at request time and
	// the worker only runs when enabled. Linked app passwords are sealed with the
	// ATProto KEK (ATPROTO_KEY_KEK, falling back to FEDERATION_KEY_KEK).
	atprotoOpts := []atproto.Option{
		atproto.WithEnabled(cfg.ATProtoEnabled),
		atproto.WithBaseURL(cfg.PublicBaseURL),
		atproto.WithThumbnails(videosvc),
		atproto.WithLogger(logger),
		// Reuse the outbound-fetch dev knob so backed e2e can reach a loopback PDS.
		atproto.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
	}
	if kek := cfg.ATProtoKEK(); kek != "" {
		atprotoCipher, err := secretbox.NewCipherFromBase64(kek)
		if err != nil {
			return err
		}
		atprotoOpts = append(atprotoOpts, atproto.WithCipher(atprotoCipher))
	} else if cfg.ATProtoEnabled {
		logger.Warn("ATPROTO_KEY_KEK/FEDERATION_KEY_KEK unset — Bluesky app passwords are stored UNENCRYPTED; set a KEK outside dev")
	}
	atprotosvc = atproto.NewService(db.Queries(), atprotoOpts...)
	opts = append(opts, httpapi.WithATProtoService(atprotosvc))

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

	// Drain the outbound ATProto auto-post queue in the background (fresh session
	// + app.bsky.feed.post with retry/backoff + dead-letter). Only when enabled.
	if cfg.ATProtoEnabled {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runATProtoPostWorker(workerCtx, logger, atprotosvc)
		logger.Info("atproto auto-post worker started")
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

	// Channel auto-sync worker (W2.C4): on a cadence, list each due sync's external
	// channel and enqueue `ytdlp` imports for unseen uploads. Only started when the
	// feature is effective (CHANNEL_SYNC_ENABLED + yt-dlp import); otherwise the
	// service is wired for its stable 503 contract but no worker runs.
	if channelsyncsvc.Enabled() {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runChannelSyncWorker(workerCtx, logger, channelsyncsvc)
		logger.Info("channel auto-sync worker started", "interval", cfg.ChannelSyncInterval.String())
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

	// Drain the IPFS mirror pin/unpin queue and periodically re-arm dead-letters
	// (fix_plan P19). Only when IPFS_ENABLED — the mirror is a sidecar, so this
	// never affects the authoritative write/serve paths.
	if ipfsMirror.Enabled() {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runIPFSMirrorWorker(workerCtx, logger, ipfsMirror, cfg.IPFSReconcileInterval)
		logger.Info("ipfs mirror worker started")
	}

	// Admin operations: the durable-queue depth snapshot + recent-failures
	// endpoint (P17.4) and the queue-depth Prometheus gauge both read from here.
	jobStatusSvc := jobstatus.NewService(db.Queries())
	opts = append(opts, httpapi.WithJobStatusService(jobStatusSvc))

	// PeerTube import / migration (fix_plan P18). The admin API is ALWAYS wired
	// (stable contract for the vidra-user import UI); the launch endpoint answers
	// 503 until a source is configured (PEERTUBE_IMPORT_ENABLED +
	// PEERTUBE_SOURCE_DATABASE_URL). The source connection is built from SERVER
	// CONFIG per run — never from a request; the browser never sends a DSN.
	defaultImportPolicy, _ := peertubeimport.ParseConflictPolicy(cfg.PeerTubeImportConflictPolicy)
	ptImportOpts := []peertubeimport.Option{
		peertubeimport.WithLogger(logger),
		peertubeimport.WithDefaultPolicy(defaultImportPolicy),
		peertubeimport.WithAudit(func(ctx context.Context, ev observability.AuditEvent) {
			_ = auditsvc.Record(ctx, audit.Event{Action: ev.Action, Result: ev.Result, ActorID: ev.ActorID, Reason: ev.Reason, RequestID: ev.RequestID})
		}),
	}
	if cfg.PeerTubeImportConfigured() {
		// Actor-key sealer reuses the federation KEK so imported account/channel
		// private keys are sealed exactly like the server seals them at rest.
		var ptSeal func(string) (string, error)
		if cfg.FederationKeyKEK != "" {
			ptCipher, err := secretbox.NewCipherFromBase64(cfg.FederationKeyKEK)
			if err != nil {
				return err
			}
			ptSeal = func(pem string) (string, error) { return ptCipher.Seal([]byte(pem)) }
		}
		srcStorageCfg := peertubeimport.SourceStorageConfig{
			Backend:          cfg.PeerTubeSourceStorageBackend,
			LocalRoot:        cfg.PeerTubeSourceStorageLocalRoot,
			S3Endpoint:       cfg.PeerTubeSourceS3Endpoint,
			S3Bucket:         cfg.PeerTubeSourceS3Bucket,
			S3AccessKey:      cfg.PeerTubeSourceS3AccessKey,
			S3SecretKey:      cfg.PeerTubeSourceS3SecretKey,
			S3Region:         cfg.PeerTubeSourceS3Region,
			S3UseSSL:         cfg.PeerTubeSourceS3UseSSL,
			S3ForcePathStyle: cfg.PeerTubeSourceS3ForcePathStyle,
		}
		ptImportOpts = append(ptImportOpts, peertubeimport.WithImporterFactory(
			func(ctx context.Context, policy peertubeimport.ConflictPolicy) (*peertubeimport.Importer, func(), error) {
				src, err := peertubeimport.OpenSource(ctx, cfg.PeerTubeSourceDatabaseURL)
				if err != nil {
					return nil, nil, err
				}
				srcMedia, err := peertubeimport.OpenSourceStorage(srcStorageCfg)
				if err != nil {
					src.Close()
					return nil, nil, err
				}
				imp := peertubeimport.NewImporter(db.Pool, src, peertubeimport.Options{
					Policy:    policy,
					SrcMedia:  srcMedia,
					DestMedia: blobs,
					SealKey:   ptSeal,
					Logger:    logger,
				})
				return imp, func() { src.Close() }, nil
			}))
		logger.Info("peertube import configured", "source_storage", cfg.PeerTubeSourceStorageBackend)
	}
	ptImportSvc := peertubeimport.NewService(db.Queries(), ptImportOpts...)
	opts = append(opts, httpapi.WithPeerTubeImportService(ptImportSvc))
	if ptImportSvc.Configured() {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runPeerTubeImportWorker(workerCtx, logger, ptImportSvc)
		logger.Info("peertube import worker started")
	}

	// Prometheus RED metrics (P17.3), gated behind METRICS_ENABLED. The queue-depth
	// gauge pulls from the jobs snapshot at scrape time. Off by default → zero cost.
	if cfg.MetricsEnabled {
		metrics := observability.NewMetrics()
		metrics.RegisterQueueDepthSource(func(ctx context.Context) ([]observability.QueueDepth, error) {
			depths, err := jobStatusSvc.Depths(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]observability.QueueDepth, len(depths))
			for i, d := range depths {
				out[i] = observability.QueueDepth{Queue: d.Queue, State: d.State, Count: d.Count}
			}
			return out, nil
		})
		opts = append(opts, httpapi.WithMetrics(metrics))
		logger.Info("prometheus metrics enabled", "route", "/metrics")
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

// runATProtoPostWorker drains the durable ATProto auto-post queue on a ticker
// until ctx is canceled (mirrors runFederationDeliveryWorker). A single worker
// with a small batch keeps at most a couple of PDS round-trips in flight per
// tick; it logs only claim-query errors — per-post failures are recorded in the
// queue (retry/backoff/dead-letter) rather than logged.
func runATProtoPostWorker(ctx context.Context, logger *slog.Logger, svc *atproto.Service) {
	const (
		interval = 15 * time.Second
		batch    = 10
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := svc.DrainPosts(ctx, batch); err != nil {
				logger.Warn("atproto auto-post drain failed", "error", err)
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

// runChannelSyncWorker lists due channel syncs on a cadence and enqueues `ytdlp`
// imports for their unseen external uploads (W2.C4). The poll tick is short so a
// POST .../sync-now trigger is picked up promptly; per-sync scheduling
// (CHANNEL_SYNC_INTERVAL) lives in the service, which reschedules each row after
// a run. Per-sync failures are recorded on the row (state=failed, safe
// last_error); only the claim-query error is logged.
func runChannelSyncWorker(ctx context.Context, logger *slog.Logger, svc *channelsync.Service) {
	const (
		interval = time.Minute
		// perTick bounds how many due syncs one tick claims (settled decision:
		// 15 per tick).
		perTick = 15
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := svc.DrainDue(ctx, perTick)
			if err != nil {
				logger.Warn("channel sync drain failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("channel sync completed passes", "count", n)
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

// runIPFSMirrorWorker drains the IPFS mirror pin/unpin queue on a short ticker
// (add+pin pending rows, reference-checked unpin of unpinning rows) and re-arms
// dead-lettered rows on the reconcile interval, until ctx is canceled. Per-row
// failures are persisted in the ledger (rescheduled/dead-lettered), never
// surfaced — the mirror is non-authoritative, so an outage never blocks anything.
func runIPFSMirrorWorker(ctx context.Context, logger *slog.Logger, svc *ipfsmirror.Service, reconcileInterval time.Duration) {
	const (
		drainInterval = 10 * time.Second
		batch         = 8
	)
	drain := time.NewTicker(drainInterval)
	defer drain.Stop()
	if reconcileInterval <= 0 {
		reconcileInterval = 5 * time.Minute
	}
	reconcile := time.NewTicker(reconcileInterval)
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-drain.C:
			total := 0
			for {
				n, err := svc.DrainDue(ctx, batch)
				if err != nil {
					logger.Warn("ipfs mirror drain failed", "error", err)
					break
				}
				total += n
				if n == 0 {
					break
				}
			}
			if total > 0 {
				logger.Info("ipfs mirror drained pins", "count", total)
			}
			// Expand any durable per-user re-eval jobs (unlisted-toggle fan-out moved
			// off the request path, P19 round-2 audit) with per-video error isolation.
			for {
				n, err := svc.DrainDueUserReevals(ctx, batch)
				if err != nil {
					logger.Warn("ipfs mirror user-reeval drain failed", "error", err)
					break
				}
				if n == 0 {
					break
				}
			}
		case <-reconcile.C:
			if _, err := svc.Reconcile(ctx); err != nil {
				logger.Warn("ipfs mirror reconcile failed", "error", err)
			}
			// Eligibility backstop: re-arm any pinned-but-now-ineligible row (missed
			// toggle, crashed re-eval, or a future rule change) toward removal.
			if _, err := svc.SweepIneligible(ctx); err != nil {
				logger.Warn("ipfs mirror eligibility sweep failed", "error", err)
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

// runPeerTubeImportWorker claims and executes due PeerTube import runs (fix_plan
// P18). Only one run is ever active (the single-active DB constraint), so it
// drains at most one per tick; per-run outcomes are persisted to the run row.
func runPeerTubeImportWorker(ctx context.Context, logger *slog.Logger, svc *peertubeimport.Service) {
	const interval = 15 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := svc.DrainDueRuns(ctx, 1); err != nil {
				logger.Warn("peertube import drain failed", "error", err)
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
