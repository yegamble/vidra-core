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
	"strconv"
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
	"github.com/vidra/vidra-core/internal/diskspace"
	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/instancedocs"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/jobrecovery"
	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/leaderlock"
	"github.com/vidra/vidra-core/internal/linkpreview"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/mediahash"
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
	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/storagemigration"
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

// jobRecoverySweepInterval is how often stranded jobs are swept back into their
// queues. The queues lease for 30 minutes, so a worker that dies is recovered
// somewhere between one and two sweeps later — soon enough that a crash is not
// felt as a permanently stuck video, and rare enough that six UPDATEs every few
// minutes is not worth thinking about.
const jobRecoverySweepInterval = 2 * time.Minute

func main() {
	// Bootstrap logger for pre-config diagnostics; replaced by the configured
	// logger (LOG_LEVEL/LOG_FORMAT) once config is loaded in run().
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Operator subcommands (see migrate.go, verify_blobs.go). Dispatched before
	// run() so the schema and consistency tooling ships in the same image as the
	// server without paying for the server's config/dependency startup. Bare
	// `api` (no argv) is the server, as it has always been.
	//
	// Anything else is REFUSED rather than ignored: `docker compose run --rm
	// migrate <args>` replaces the service's command outright, so a mistyped
	// subcommand on the migrate one-shot would otherwise boot a whole HTTP server
	// in a container an operator believes is applying migrations.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			if err := runMigrate(os.Args[2:]); err != nil {
				slog.Error("migrate failed", "error", err)
				os.Exit(1)
			}
			return
		case "verify-blobs":
			// Exits with its own code: 0 consistent, 3 inconsistent, 1 could not
			// check. A three-outcome command cannot report through main's
			// error path, which only knows "worked" and "did not".
			os.Exit(runVerifyBlobs(os.Args[2:], os.Stdout, os.Stderr))
		default:
			slog.Error("unknown subcommand", "argv", os.Args[1],
				"usage", "api ["+migrateUsage+"|"+verifyBlobsUsage+"] (no arguments runs the server)")
			os.Exit(1)
		}
	}

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
	// Prometheus metrics registry (P17.3). Built early when enabled so the search
	// outbox enqueuer/drainer can meter drops/dead-letters; the scrape sources
	// that need late-built services are registered lower down.
	var metrics *observability.Metrics
	if cfg.MetricsEnabled {
		metrics = observability.NewMetrics()
	}
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
	// GET /schemaz reads the migration ledger over this same pool, so the version
	// probe costs a pooled query rather than a connection.
	opts = append(opts, httpapi.WithSchemaLedger(db.Pool))

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
	// Parse the boot-validated UPLOAD_MAX_SIZE once (config.go already rejected an
	// invalid value) — it seeds both the settings default and the import cap below.
	uploadMaxBytesDefault, _ := bytes.Parse(cfg.UploadMaxSize)
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

		DefaultUserQuotaBytes:          cfg.InstanceDefaultQuotaBytes,
		UploadMaxSizeBytes:             uploadMaxBytesDefault,
		UploadMaxActiveSessionsPerUser: int64(cfg.UploadMaxActiveSessionsPerUser),
		ImportMaxHeight:                int64(cfg.YtdlpMaxHeight),

		// Shipped-feature toggle batch (config-parity W8): defaults come from
		// the existing env knobs; the boot capabilities themselves (yt-dlp
		// wiring, WHISPER_ENDPOINT) stay env-only and are ANDed in at each seam.
		ChannelSyncEnabled:    cfg.ChannelSyncEnabled,
		ChannelSyncMaxPerUser: int64(cfg.ChannelSyncMaxPerUser),
		TranscriptionEnabled:  cfg.WhisperEnabled,

		// VOD transcoding master toggle (config-parity W10): the runtime
		// setting defaults to the boot env; the ffmpeg/ffprobe boot capability
		// is ANDed in at the enqueue/pickup seams.
		TranscodingEnabled: cfg.TranscodingEnabled,
	})
	if err := settingssvc.Load(startCtx); err != nil {
		return err
	}
	opts = append(opts, httpapi.WithSettingsService(settingssvc))

	// Instance documents (config-parity W1): the admin-authored homepage +
	// custom CSS/JS store. Loaded at boot into an in-memory cache (same posture
	// as the settings overlay) so the public delivery routes and the GET
	// /instance customization/homepage hashes never round-trip to the database.
	instancedocssvc := instancedocs.NewService(db.Queries())
	if err := instancedocssvc.Load(startCtx); err != nil {
		return err
	}
	opts = append(opts, httpapi.WithInstanceDocumentsService(instancedocssvc))

	// vidra-search integration (search-service W4). Wired only when
	// SEARCH_SERVICE_URL is set; otherwise every search surface degrades to local
	// behaviour and no client/enqueuer/worker is constructed. The enqueuer is
	// built here (before the video service) so the publish/update/delete hooks
	// below can capture it.
	var searchEnqueuer *searchevents.Enqueuer
	var searchClient *searchclient.Client
	var searchDrainer *searchevents.Drainer
	if cfg.SearchServiceEnabled() {
		var enqOpts []searchevents.Option
		var drainOpts []searchevents.DrainOption
		if metrics != nil {
			enqOpts = append(enqOpts, searchevents.WithMetrics(metrics))
			drainOpts = append(drainOpts, searchevents.WithDrainMetrics(metrics))
		}
		searchEnqueuer = searchevents.NewEnqueuer(db.Queries(), logger, enqOpts...)
		searchClient = searchclient.New(cfg.SearchServiceURL, cfg.SearchInternalSecret,
			searchclient.WithTimeouts(
				time.Duration(cfg.SearchSuggestTimeoutMS)*time.Millisecond,
				time.Duration(cfg.SearchQueryTimeoutMS)*time.Millisecond,
				time.Duration(cfg.SearchRecsTimeoutMS)*time.Millisecond,
			),
			// Active health detection (W9): the background prober GETs /healthz on
			// this cadence so a down service is skipped with zero per-request latency.
			searchclient.WithHealthInterval(cfg.SearchHealthInterval),
			searchclient.WithLogger(logger),
		)
		// vidra_search_service_healthy gauge (W9): reflect the prober's /healthz
		// verdict at scrape time (no writer coupling), alongside the other metrics.
		if metrics != nil {
			metrics.RegisterSearchServiceHealthSource(func() float64 {
				if searchClient.ServiceProbeHealthy() {
					return 1
				}
				return 0
			})
		}
		searchDrainer = searchevents.NewDrainer(db.Queries(), searchClient, logger, drainOpts...)
		// video.watch_progress throttle: one event per (user,video) per 30s window.
		searchThrottle := cache.NewDeduper(rdb.Client)
		opts = append(opts,
			httpapi.WithSearchClient(searchClient),
			httpapi.WithSearchEvents(searchEnqueuer),
			httpapi.WithSearchWatchThrottle(func(ctx context.Context, key string) bool {
				first, terr := searchThrottle.First(ctx, key, 30*time.Second)
				return terr == nil && first
			}),
		)
		logger.Info("vidra-search integration enabled", "url", cfg.SearchServiceURL)
	}

	if cfg.RateLimitEnabled {
		counter := ratelimit.NewRedisCounter(rdb.Client)
		limiter := ratelimit.NewLimiter(counter, cfg.RateLimitRequests, cfg.RateLimitWindow)
		authLimiter := ratelimit.NewLimiter(counter, cfg.AuthRateLimitRequests, cfg.RateLimitWindow)
		// Media GETs (thumbnails, HLS playlists/segments, storyboards, downloads,
		// avatars) get their own, much larger budget over the same window: one
		// home page is a single feed call plus ~20 thumbnails, and playback adds
		// segment reads on top, so charging them to the API budget 429s an
		// ordinary viewer within a minute.
		mediaLimiter := ratelimit.NewLimiter(counter, cfg.MediaRateLimitRequests, cfg.RateLimitWindow)
		// Per-user DM attachment upload limiter (messaging-v2.md D6): the
		// compensating anti-abuse control now that DM attachments do not count
		// against the storage quota. Keyed by user id, its own (longer) window.
		attachLimiter := ratelimit.NewLimiter(counter, cfg.AttachmentUploadRateLimitRequests, cfg.AttachmentUploadRateLimitWindow)
		opts = append(opts,
			httpapi.WithRateLimiter(limiter),
			httpapi.WithAuthRateLimiter(authLimiter),
			httpapi.WithMediaRateLimiter(mediaLimiter),
			httpapi.WithAttachmentRateLimiter(attachLimiter),
		)
		logger.Info("rate limiting enabled",
			"requests", cfg.RateLimitRequests,
			"auth_requests", cfg.AuthRateLimitRequests,
			"media_requests", cfg.MediaRateLimitRequests,
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
	var smtpMailer *mail.SMTP
	if cfg.MailEnabled {
		// Real outbound email over SMTP. NB: host/port/from are safe to log; the
		// SMTP credentials are NOT (observability sensitive-key rules).
		smtpMailer = mail.NewSMTP(mail.Config{
			Host:         cfg.SMTPHost,
			Port:         cfg.SMTPPort,
			Username:     cfg.SMTPUsername,
			Password:     cfg.SMTPPassword,
			From:         cfg.SMTPFrom,
			InstanceName: cfg.InstanceName,
		},
			// Email customization (config-parity W6): the subject prefix (with
			// {instance_name} substituted from the EFFECTIVE instance name) and
			// body signature ride the single Send seam, resolved per send from
			// the settings overlay. Empty values are no-ops; with MAIL_ENABLED
			// off this mailer never exists, so the settings are inert.
			mail.WithSubjectPrefixFunc(func() string {
				return settingssvc.String(instancesettings.KeyEmailSubjectPrefix)
			}),
			mail.WithBodySignatureFunc(func() string {
				return settingssvc.String(instancesettings.KeyEmailBodySignature)
			}),
			mail.WithInstanceNameFunc(func() string {
				return settingssvc.String(instancesettings.KeyInstanceName)
			}),
		)
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
	// Public contact form (spec instance-platform-info): POST /instance/contact
	// delivers through the same effective mailer the auth flows use (dev capture
	// wins over SMTP). Without any mail path the option stays unset and the form
	// reports unavailable (409 / effective contact_form_enabled=false). The
	// endpoint always carries its own hard budget — 1 request per IP per hour —
	// on the shared Redis counter when rate limiting is on (multi-node
	// correctness), else an in-process counter, so it is never unthrottled.
	switch {
	case captureMailer != nil:
		opts = append(opts, httpapi.WithContactMailer(captureMailer))
	case smtpMailer != nil:
		opts = append(opts, httpapi.WithContactMailer(smtpMailer))
	}
	var contactCounter ratelimit.Counter = ratelimit.NewMemoryCounter()
	if cfg.RateLimitEnabled {
		contactCounter = ratelimit.NewRedisCounter(rdb.Client)
	}
	opts = append(opts, httpapi.WithContactRateLimiter(ratelimit.NewLimiter(contactCounter, 1, time.Hour)))
	// The admin mail probe (POST /admin/mail/test) gets its own budget on the
	// same counter: 3 per ADMIN per hour. It always mails the instance's own
	// contact address, so this is not an anti-relay control — it stops a stuck
	// browser tab from hammering the relay until the domain is throttled, which
	// is a failure that arrives days later as "password resets stopped working".
	opts = append(opts, httpapi.WithMailTestRateLimiter(ratelimit.NewLimiter(contactCounter, 3, time.Hour)))
	// Remote-URI search resolution (config-parity W13) is an outbound-fetch
	// surface, so it carries its own per-caller budget — 10 resolutions per
	// minute — on the shared Redis counter when rate limiting is on (multi-node
	// correctness), else an in-process counter (httpapi installs one itself
	// when this option is absent; wiring it here keeps the counter choice
	// consistent with the contact form).
	var searchResolveCounter ratelimit.Counter = ratelimit.NewMemoryCounter()
	if cfg.RateLimitEnabled {
		searchResolveCounter = ratelimit.NewRedisCounter(rdb.Client)
	}
	opts = append(opts, httpapi.WithSearchResolveRateLimiter(ratelimit.NewLimiter(searchResolveCounter, 10, time.Minute)))
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
	// Sign-up & new users (config-parity W7): seed the per-user watch-history
	// preference from the live setting, and wire the EFFECTIVE registration
	// email-verification gate — the runtime toggle AND an outbound mail path
	// (dev capture or SMTP; a runtime toggle can never conjure a mailer the
	// deployment lacks).
	mailWired := smtpMailer != nil || captureMailer != nil
	authOpts = append(authOpts,
		auth.WithNewUserHistoryEnabledFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyNewUserHistoryEnabled)
		}),
		auth.WithEmailVerificationGateFunc(func() bool {
			return mailWired && settingssvc.Bool(instancesettings.KeyRegistrationRequireEmailVerification)
		}),
	)
	// OWNER_CLAIM_TOKEN (dev/test-only; config refuses it in production) pins the
	// owner-claim mint to a deterministic value so harnesses can claim the owner
	// without scraping the boot log.
	if cfg.OwnerClaimToken != "" {
		authOpts = append(authOpts, auth.WithFixedOwnerClaimToken(cfg.OwnerClaimToken))
	}
	authsvc := auth.NewService(db.Queries(), issuer, cfg.JWTRefreshTTL, authOpts...)
	opts = append(opts, httpapi.WithAuthService(authsvc, cfg.JWTAccessTTL))

	// First-run owner bootstrap (0104): the admin account is claimed with a
	// one-time setup token — never by winning the registration race. A fresh
	// token is minted on every boot while a claim is outstanding (only its
	// hash is stored, so the raw value is unrecoverable and each re-mint
	// invalidates the previous log line) — even once users exist, because an
	// unclaimed token in an old boot log must never stay a live admin
	// credential. Instances with users and no unclaimed token are implicitly
	// claimed and skip this entirely. The token appears in the log MESSAGE by
	// deliberate exception to the never-log-secrets rule: like WordPress/
	// Jupyter bootstrap secrets, the operator console is its one delivery
	// channel, and it grants nothing once claimed or re-minted.
	setupToken, minted, hadUsers, err := authsvc.EnsureOwnerClaimToken(startCtx)
	if err != nil {
		return fmt.Errorf("owner-claim bootstrap: %w", err)
	}
	if minted {
		if cfg.OwnerClaimToken != "" {
			logger.Warn("OWNER_CLAIM_TOKEN override active: the owner-claim token is the FIXED value from the environment, not a random mint (dev/test only — production refuses this variable)")
		}
		if hadUsers {
			logger.Warn("OWNER STILL UNCLAIMED: this instance has users but the owner (admin) account was never claimed — the previous setup token has been invalidated and a fresh one minted; claim it now: " + setupToken)
		} else {
			logger.Warn("FIRST-RUN SETUP REQUIRED: no accounts exist yet — claim the owner (admin) account with the one-time setup token (sign-ups stay closed with 403 owner_claim_required until claimed): " + setupToken)
		}
		logger.Warn("claim the owner account with: curl -X POST <public-base-url>/api/v1/setup/claim-owner -H 'Content-Type: application/json' -d '{\"token\":\"<setup token above>\",\"username\":\"...\",\"email\":\"...\",\"password\":\"...\"}' — restarting mints a fresh token and invalidates this one")
	}
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

	// The per-user channel cap (max_channels_per_user, config-parity W8) is
	// resolved per create from the settings overlay; 0 (the default) = unlimited.
	channelsvc := channel.NewService(db.Queries(),
		channel.WithMaxPerUserFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyMaxChannelsPerUser)
		}))
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

	blobs, createdBucket, err := newStorageBackend(startCtx, cfg)
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

	// Bucket ownership for media GC (phase-2 storage, item 1). Resolved here
	// because it needs both halves of the question — the identity is in the
	// database and the marker is in the store — and once, at boot, because a
	// per-sweep round trip would answer the same question daily and still not
	// notice the case that matters (someone else's bucket) any sooner.
	//
	// A missing identity row is not fatal: an install whose migrations have not
	// been run yet still has to boot. It costs destructive GC until they have.
	instanceIdentity := ""
	if id, iderr := db.Queries().GetInstanceIdentity(startCtx); iderr != nil {
		logger.Warn("instance identity unavailable; media gc cannot establish bucket ownership", "error", iderr)
	} else {
		instanceIdentity = id.String()
	}
	bucketOwnership := resolveBucketOwnership(startCtx, logger, blobs, createdBucket, instanceIdentity)

	// Storage migration target (phase-2 storage, items 4-5): the SECOND backend a
	// migration campaign copies into. nil when STORAGE_MIGRATION_TARGET_* is
	// unset, which is the ordinary configuration.
	//
	// NB: same rule as the primary — endpoint/bucket are safe to log, the
	// credentials are NOT. storage.Describe returns exactly the safe half.
	migrationTarget, err := newMigrationTargetBackend(startCtx, cfg, blobs)
	if err != nil {
		return err
	}
	if migrationTarget != nil {
		logger.Info("storage migration target configured",
			"backend", cfg.StorageMigrationTargetBackend,
			"target", storage.Describe(migrationTarget),
			"serving_from", storage.Describe(blobs),
			"grace_hours", cfg.StorageMigrationGraceHours)
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

	// Per-user storage quotas: usage is aggregated live from video_files;
	// uploads/imports that would exceed the effective quota get 422.
	// Constructed BEFORE the video service so AttachOriginal can feed the
	// rolling daily-upload ledger (config-parity W7). The daily quota reads
	// default_user_daily_quota_bytes live (0 = unlimited).
	quotasvc := quota.NewService(db.Queries(), cfg.InstanceDefaultQuotaBytes,
		quota.WithDefaultBytesFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyDefaultUserQuotaBytes)
		}),
		quota.WithDailyBytesFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyDefaultUserDailyQuotaBytes)
		}))
	opts = append(opts, httpapi.WithQuotaService(quotasvc))
	if cfg.InstanceDefaultQuotaBytes > 0 {
		logger.Info("default per-user storage quota enabled", "bytes", cfg.InstanceDefaultQuotaBytes)
	}

	// Wire the FFprobe media prober when ffprobe is on PATH; otherwise uploads
	// finalise by publishing the original unprobed (no metadata) so a host
	// without ffmpeg still works.
	var vopts []video.Option
	// Daily-upload accounting (W7): every stored original is recorded against
	// its owner's rolling 24h window at the AttachOriginal choke point.
	vopts = append(vopts, video.WithUploadUsageRecorder(func(ctx context.Context, ownerID uuid.UUID, bytes int64) error {
		if err := quotasvc.RecordUpload(ctx, ownerID, bytes); err != nil {
			logger.Warn("daily upload usage record failed", "user_id", ownerID, "error", err)
		}
		return nil
	}))
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
	// Storyboard generation follows the storyboards_enabled overlay at runtime
	// (config-parity W8, default on). Stored storyboards keep serving when the
	// gate is off — it covers generation only.
	vopts = append(vopts, video.WithStoryboardGate(func() bool {
		return settingssvc.Bool(instancesettings.KeyStoryboardsEnabled)
	}))
	// Instance publish defaults (config-parity W9): fields a creator leaves
	// unset on a new video seed from the defaults.publish settings, read live
	// per CreateDraft so admin changes apply without a restart. Covers every
	// draft producer (HTTP create, channel-sync, live replays).
	vopts = append(vopts, video.WithPublishDefaultsFunc(func() video.PublishDefaults {
		def := video.PublishDefaults{
			Privacy:         settingssvc.String(instancesettings.KeyDefaultVideoPrivacy),
			CommentsPolicy:  settingssvc.String(instancesettings.KeyDefaultCommentPolicy),
			DownloadEnabled: settingssvc.Bool(instancesettings.KeyDefaultDownloadEnabled),
		}
		if licence := settingssvc.Int(instancesettings.KeyDefaultVideoLicence); licence != 0 {
			def.License = strconv.FormatInt(licence, 10)
		}
		return def
	}))
	// Upload container policy (config-parity W10): the extended extension set
	// follows upload_additional_extensions_enabled at runtime (default on —
	// the shipped allow-list already included it). Consulted per AttachOriginal;
	// the HTTP layer applies the same gate up front at session create.
	vopts = append(vopts, video.WithAdditionalExtGate(func() bool {
		return settingssvc.Bool(instancesettings.KeyUploadAdditionalExtensionsEnabled)
	}))
	if cfg.QuarantineNewUploads {
		logger.Info("upload quarantine enabled by default (QUARANTINE_NEW_UPLOADS)")
	}
	// HLS transcoding. The read side (playlist/rendition lookups + the /hls
	// serving routes) is always wired so previously produced playlists keep
	// serving even when the pipeline is later disabled. The transcoder, enqueue
	// hook, and worker are wired whenever ffmpeg+ffprobe are present (the BOOT
	// capability); the runtime transcoding_enabled setting — defaulting to
	// TRANSCODING_ENABLED — gates job enqueue and worker pickup per call
	// (config-parity W10), never construction, so an admin can flip the
	// pipeline on/off without a restart (the boot-baked-worker gotcha).
	var hlsTranscoder transcode.Transcoder
	if tc, ok := media.DetectHLSTranscoder(blobs); ok {
		tc.SetVP9(cfg.TranscodingVP9Enabled)
		tc.SetStreamOutput(cfg.TranscodingStreamOutput)
		// Packaging format for NEW transcodes (phase-3 item 3). Config has already
		// validated the name; a failure here would mean the two lists disagree,
		// which is a bug rather than an operator mistake — refuse to boot on it
		// rather than silently packaging a whole deployment the other way.
		if err := tc.SetPackager(cfg.Packager()); err != nil {
			return fmt.Errorf("TRANSCODING_PACKAGER: %w", err)
		}
		// Encode knobs (ladder/FPS/threads/original-resolution) resolve from the
		// settings overlay once per job, so changes apply without a restart.
		tc.SetEncodeSettingsFunc(func() media.HLSEncodeSettings {
			return media.HLSEncodeSettings{
				Resolutions:        instancesettings.ParseRungHeights(settingssvc.Strings(instancesettings.KeyTranscodingResolutions)),
				MaxFPS:             int(settingssvc.Int(instancesettings.KeyTranscodingMaxFPS)),
				Threads:            int(settingssvc.Int(instancesettings.KeyTranscodingThreads)),
				OriginalResolution: settingssvc.Bool(instancesettings.KeyTranscodingOriginalResolution),
			}
		})
		hlsTranscoder = tc
		logger.Info("hls transcoding pipeline wired (ffmpeg + ffprobe found; runtime gate transcoding_enabled)",
			"enabled_default", cfg.TranscodingEnabled, "vp9", cfg.TranscodingVP9Enabled,
			"stream_output", cfg.TranscodingStreamOutput, "packager", cfg.Packager())
	} else if cfg.TranscodingEnabled {
		logger.Warn("TRANSCODING_ENABLED=true but ffmpeg/ffprobe not on PATH; transcoding disabled")
	}
	// transcodesvc + videosvc are assigned below; the completion/failure hooks and
	// the video-service transcode seams only run post-startup (worker goroutines or
	// request handlers), so these deferred-assignment closures see the built
	// services. Nil-guarded regardless (mirrors the fedsvc/atprotosvc seam above).
	var (
		transcodesvc *transcode.Service
		videosvc     *video.Service
	)
	// releaseHold releases a publish-after-transcode hold once no live job
	// remains (0098), shared by the completion and terminal-failure hooks. The
	// no-live-job gate uses LiveJob — the raw check that SURFACES the lookup
	// error — not HasLiveJob, whose fail-busy posture would let a transient DB
	// error at last-job completion suppress the release until the stuck sweeper.
	// "Undetermined" attempts the release instead: the state-guarded CAS inside
	// ReleaseTranscodeHold makes an over-eager attempt harmless.
	releaseHold := func(ctx context.Context, videoID uuid.UUID) {
		if videosvc == nil || transcodesvc == nil {
			return
		}
		if live, err := transcodesvc.LiveJob(ctx, videoID); err == nil && live {
			return // more jobs still running for this video; the last one releases
		}
		if err := videosvc.ReleaseTranscodeHold(ctx, videoID); err != nil {
			logger.Warn("release transcode hold failed", "video_id", videoID, "error", err)
		}
	}
	// mirrorSync is the IPFS mirror's HLS-tree pin on transcode completion
	// (P19.4). Gated on the IPFS tiers so it stays a no-op otherwise.
	mirrorSync := func(ctx context.Context, videoID uuid.UUID) {
		if !cfg.IPFSEnabled && !privateEnabled {
			return
		}
		if err := ipfsMirror.OnTranscodeComplete(ctx, videoID); err != nil {
			logger.Warn("ipfs mirror transcode-complete sync failed", "video_id", videoID, "error", err)
		}
	}
	// Completion hook, fired per successfully completed job: release the hold
	// FIRST, then sync the mirror — see composeTranscodeCompletion for why the
	// order is load-bearing. Best-effort; a hook failure never fails the job.
	var tcopts []transcode.Option
	tcopts = append(tcopts, transcode.WithCompletionHook(composeTranscodeCompletion(releaseHold, mirrorSync)))
	// Terminal-failure hook (per dead-lettered job): release a publish-after-
	// transcode hold so a video whose transcode permanently failed still publishes
	// from its (playable) original rather than staying hidden forever (0098).
	tcopts = append(tcopts, transcode.WithFailureHook(releaseHold))
	// Runtime gates (config-parity W10): transcoding_enabled is consulted at
	// enqueue AND pickup; transcoding_concurrency per drain tick.
	tcopts = append(tcopts,
		transcode.WithEnabledFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyTranscodingEnabled)
		}),
		transcode.WithConcurrencyFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyTranscodingConcurrency)
		}),
	)
	// Scratch-space admission control: the worker measures the filesystem it
	// actually writes to (TMPDIR, which the prod compose points at the
	// transcode_tmp volume) and claims nothing below the floor. Without this a
	// busy instance fills the disk, and on a single-disk host that stops Postgres
	// accepting writes rather than merely failing a transcode.
	scratchRoot := os.TempDir()
	tcopts = append(tcopts, transcode.WithScratchGuard(
		func() (uint64, error) {
			u, err := diskspace.Measure(scratchRoot)
			return u.FreeBytes, err
		},
		uint64(cfg.TranscodingMinFreeScratchMB)<<20,
	))
	transcodesvc = transcode.NewService(db.Queries(), hlsTranscoder, tcopts...)
	opts = append(opts, httpapi.WithTranscodeService(transcodesvc))
	// Publish-after-transcode seams (0098): whether a transcode will actually run
	// (so a video is held only when the hold can be released) and whether a live
	// job exists (the update-toggle edge case). Both consult transcodesvc, which
	// reports Capable()=false when no ffmpeg-backed transcoder is wired.
	vopts = append(vopts,
		video.WithTranscodeReadinessFunc(func() bool {
			return transcodesvc.Capable() && transcodesvc.Enabled()
		}),
		video.WithLiveTranscodeChecker(func(ctx context.Context, videoID uuid.UUID) bool {
			return transcodesvc.HasLiveJob(ctx, videoID)
		}),
	)
	if hlsTranscoder != nil {
		vopts = append(vopts, video.WithTranscodeHook(func(ctx context.Context, videoID uuid.UUID, sourceKey string) {
			// Best-effort: an enqueue failure must never block the publish.
			// While transcoding_enabled is off, Enqueue is a silent no-op and
			// the video stays playable via the retained original.
			if err := transcodesvc.Enqueue(ctx, videoID, sourceKey); err != nil {
				logger.Warn("transcode enqueue failed", "video_id", videoID, "error", err)
			}
		}))
	}
	// Tell the channel's LOCAL followers that a new public video went live —
	// the "new video from a channel you follow" notification, one set-based
	// fan-out per publish. Registered unconditionally (there is no feature flag
	// on notifications) and best-effort: a fan-out failure is logged and never
	// touches the publish. notifsvc is built further down, so this takes the
	// same deferred-service seam as the federation/ATProto hooks below.
	var notifsvc *notification.Service
	vopts = append(vopts, video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
		if notifsvc == nil {
			return
		}
		notified, err := notifsvc.NotifyNewVideo(ctx, videoID)
		if err != nil {
			logger.Warn("new-video follower notification failed", "video_id", videoID, "error", err)
			return
		}
		if notified > 0 {
			logger.Info("notified followers of new video", "video_id", videoID, "followers", notified)
		}
	}))
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
	// Search indexing hooks (search-service W4): publish + metadata/privacy edit
	// re-index the full doc (search recomputes eligibility); delete suppresses it.
	// Best-effort — a search hiccup never fails the video mutation.
	if searchEnqueuer != nil {
		vopts = append(vopts,
			video.WithPublishHook(func(ctx context.Context, videoID uuid.UUID) {
				searchEnqueuer.EnqueueVideoUpsert(ctx, videoID)
			}),
			video.WithUpdateHook(func(ctx context.Context, videoID uuid.UUID) {
				searchEnqueuer.EnqueueVideoUpsert(ctx, videoID)
			}),
			video.WithDeleteHook(func(ctx context.Context, videoID, channelID uuid.UUID, wasPublic bool) {
				searchEnqueuer.EnqueueVideoSuppress(ctx, videoID, searchevents.SuppressDeleted)
			}),
		)
	}
	videosvc = video.NewService(db.Queries(), blobs, vopts...)

	// DUAL-READ during a storage migration. The HTTP layer's media handle — and
	// ONLY it — gets a fallback view: a request for an object that has not been
	// copied into the store this instance now serves from is answered from the
	// other one instead of 404ing.
	//
	// The split is deliberate and load-bearing. mediagc, the IPFS mirror, the
	// doctor, ffprobe/thumbnailing and every other consumer keep the RAW primary,
	// because storage.Fallback implements Backend and no optional capability (see
	// its doc comment): a garbage collector that enumerated a merged view of two
	// stores would delete from a store the merge did not cover. Serving is the one
	// job where "wherever the bytes are" is the right answer.
	mediaForServing := storage.Backend(blobs)
	if migrationTarget != nil {
		mediaForServing = storage.NewFallback(blobs, migrationTarget, logger)
	}
	opts = append(opts, httpapi.WithVideoService(videosvc), httpapi.WithMediaStorage(mediaForServing))

	// DIRECT DELIVERY (phase-2 storage item 6): the presigner for signed-URL
	// redirects is the RAW primary backend, and only when no migration is in
	// flight. Both halves matter:
	//
	//   - raw, because storage.Fallback deliberately implements no optional
	//     capability — a URL signed against the primary for an object that is
	//     still only in the secondary is a 404 the API can no longer rescue,
	//     since the viewer is no longer talking to the API;
	//   - only when migrationTarget is nil, because "dual-read is active" is
	//     exactly the state in which any given object may be in the other store.
	//
	// A local-filesystem install simply is not a Presigner and lands here as a
	// no-op. Wiring this does NOT enable presigned delivery — the
	// delivery_presign_enabled admin setting does, and it defaults off.
	if migrationTarget == nil {
		if presigner, ok := storage.Backend(blobs).(storage.Presigner); ok {
			opts = append(opts, httpapi.WithDeliveryPresigner(presigner))
			logger.Info("direct object delivery available",
				"backend", storage.Describe(blobs),
				"note", "enable with the delivery_presign_enabled instance setting")
		}
	} else {
		logger.Info("direct object delivery disabled while a storage migration target is configured")
	}

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

	notifsvc = notification.NewService(db.Queries())
	opts = append(opts, httpapi.WithNotificationService(notifsvc))

	playersettingssvc := playersettings.NewService(db.Queries(),
		playersettings.WithVideoCardPreviewsDefaultEnabledFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyVideoCardPreviewsDefaultEnabled)
		}))
	opts = append(opts, httpapi.WithPlayerSettingsService(playersettingssvc))

	playlistsvc := playlist.NewService(db.Queries(), playlist.WithStorage(blobs), playlist.WithMirror(ipfsMirror))
	opts = append(opts, httpapi.WithPlaylistService(playlistsvc))

	// Media garbage collection: admin-triggered sweep + a daily scheduled sweep.
	// The endpoint is mounted whatever MEDIA_GC_ENABLED says — the flag governs
	// the unattended daily delete, not an admin asking for a sweep by hand — and
	// the ownership state resolved above governs whether either may delete.
	mediagcsvc := mediagc.NewService(db.Queries(), blobs,
		mediagc.WithMaxOrphanPercent(cfg.MediaGCMaxOrphanPercent),
		mediagc.WithBucketOwnership(bucketOwnership),
		mediagc.WithInstanceIdentity(instanceIdentity),
		// Storage-migration interlock. Wired unconditionally — a campaign can
		// outlive the configuration that started it, and the sweep that must not
		// delete during one is exactly the sweep on the instance that has since
		// been reconfigured.
		mediagc.WithActiveMigrationCheck(db.Queries().HasActiveStorageMigration))
	opts = append(opts, httpapi.WithMediaGCService(mediagcsvc))

	// Content-hash backfill (phase-2 storage, work item 2): reads back the
	// stored objects whose video_files row predates hash-on-Put and records what
	// they actually contain. No API surface — it exists only to give the
	// integrity-verified storage migration a baseline for the WHOLE library.
	mediahashsvc := mediahash.NewService(db.Queries(), blobs, logger)

	// Storage migration (phase-2 storage, items 4-5). Constructed unconditionally,
	// with a possibly-nil target: Start answers 503 without one, but the read and
	// CANCEL surfaces have to exist even on an instance whose target has since been
	// unconfigured — otherwise a half-finished campaign could neither advance nor
	// be stopped, and the media-GC interlock would hold destructive sweeps off
	// forever with no way out.
	storagemigrationsvc := storagemigration.NewService(db.Queries(), blobs, migrationTarget, storagemigration.Config{
		Grace:  time.Duration(cfg.StorageMigrationGraceHours) * time.Hour,
		Logger: logger,
	})
	opts = append(opts, httpapi.WithStorageMigrationService(storagemigrationsvc))

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
	liveOpts := []live.Option{
		live.WithLogger(logger),
		// Live enforcement knobs (config-parity W11) read the DB-backed overlay
		// at enforcement time (provider-func seams), so an admin change applies
		// without a restart: the instance replay master gate, the simultaneous-
		// live caps enforced at the RTMP publish callback, and the max-duration
		// limit the watchdog sweeps against.
		live.WithAllowReplayFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyLiveAllowReplay)
		}),
		live.WithMaxInstanceLivesFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyLiveMaxInstanceLives)
		}),
		live.WithMaxUserLivesFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyLiveMaxUserLives)
		}),
		live.WithMaxDurationSecsFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyLiveMaxDurationSecs)
		}),
		// The auditor is wired unconditionally: replay outcomes only fire with a
		// recording store, but the duration watchdog audits force-closes on
		// every deployment.
		live.WithAuditor(auditsvc),
	}
	if cfg.LiveHLSRoot != "" {
		liveOpts = append(liveOpts,
			live.WithReplayPipeline(videosvc),
			live.WithRecordingStore(live.NewDirRecordingStore(cfg.LiveHLSRoot)),
		)
		logger.Info("live HLS serving + replay-to-VOD enabled", "hls_root", cfg.LiveHLSRoot)
	}
	livesvc := live.NewService(db.Queries(), liveOpts...)
	opts = append(opts, httpapi.WithLiveService(livesvc))

	imagesvc := profileimage.NewService(db.Queries(), blobs,
		profileimage.WithMirror(ipfsMirror),
		// Instance branding assets (config-parity W1): the singleton instance
		// avatar/banner/logo slots ride the same pipeline over their own thin
		// table; metadata is cached at boot for the GET /instance branding block.
		profileimage.WithInstanceImages(db.Queries()))
	if err := imagesvc.LoadInstanceImages(startCtx); err != nil {
		return err
	}
	opts = append(opts, httpapi.WithProfileImageService(imagesvc))

	// Resumable/chunked upload sessions (P6.1). Chunk bytes go to the same blob
	// backend at uploads/<session>/<n>; completion assembles them through the
	// same AttachOriginal → Process pipeline as a direct upload, and a background
	// sweeper cleans up expired/cancelled sessions' chunks (failed-upload cleanup).
	uploadsvc := upload.NewService(db.Queries(), blobs,
		upload.WithMaxActiveSessions(cfg.UploadMaxActiveSessionsPerUser),
		upload.WithMaxActiveSessionsFunc(func() int {
			return int(settingssvc.Int(instancesettings.KeyUploadMaxActiveSessionsPerUser))
		}))
	opts = append(opts, httpapi.WithUploadService(uploadsvc))

	// Asynchronous URL import (P2.2). POST /videos/:id/import now enqueues a job
	// and returns 202; a background worker performs the SSRF-guarded fetch and
	// runs it through the same pipeline, with the same UPLOAD_MAX_SIZE cap and
	// per-user quota enforcement the synchronous path had.
	importMaxBytes := uploadMaxBytesDefault // parsed once above (boot-validated)
	importOpts := []videoimport.Option{
		videoimport.WithAllowPrivateFetch(cfg.ImportAllowPrivateURLs),
		videoimport.WithQuota(quotasvc),
		videoimport.WithLogger(logger),
		// Runtime worker parallelism (import_jobs_concurrency, config-parity
		// W10), resolved per drain tick so changes apply without a restart.
		videoimport.WithConcurrencyFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyImportJobsConcurrency)
		}),
		// The per-file cap follows the upload-size overlay at runtime (resolved per
		// job); the constructor value stays the boot default.
		videoimport.WithMaxBytesFunc(func() int64 {
			return settingssvc.Int(instancesettings.KeyUploadMaxSizeBytes)
		}),
		// The platform (yt-dlp) resolver follows the import_http_enabled overlay
		// (config-parity W8): the gate can pause the path at runtime but never
		// enable it on a deployment that did not wire the extractor below.
		videoimport.WithYtdlpGate(func() bool {
			return settingssvc.Bool(instancesettings.KeyImportHTTPEnabled)
		}),
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
			// Resolution/size caps follow the overlay at argv-build time (per job).
			MaxHeightFn: func() int { return int(settingssvc.Int(instancesettings.KeyImportMaxHeight)) },
			MaxBytesFn:  func() int64 { return settingssvc.Int(instancesettings.KeyUploadMaxSizeBytes) },
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
		// Runtime overlay (config-parity W8): the effective gate is the boot
		// capability (the yt-dlp resolver) AND the channel_sync_enabled setting
		// AND the import_http_enabled setting (the sync path IS a yt-dlp import
		// path). Resolved per call/tick so an admin can pause and resume syncs
		// without a restart; the worker below stays constructed either way.
		channelsync.WithEnabledFunc(func() bool {
			return cfg.YtdlpImportEnabled &&
				settingssvc.Bool(instancesettings.KeyChannelSyncEnabled) &&
				settingssvc.Bool(instancesettings.KeyImportHTTPEnabled)
		}),
		channelsync.WithAllowPrivateURLs(cfg.ImportAllowPrivateURLs),
		channelsync.WithMaxPerUser(cfg.ChannelSyncMaxPerUser),
		// The per-user cap follows the channel_sync_max_per_user overlay
		// (0 = unlimited, default CHANNEL_SYNC_MAX_PER_USER).
		channelsync.WithMaxPerUserFunc(func() int {
			return int(settingssvc.Int(instancesettings.KeyChannelSyncMaxPerUser))
		}),
		channelsync.WithBatch(cfg.ChannelSyncBatch),
		channelsync.WithInterval(cfg.ChannelSyncInterval),
		channelsync.WithCooldown(cfg.ChannelSyncCooldown),
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
		// Runtime overlay (config-parity W8): effective = transcription_enabled
		// setting AND the Whisper boot capability (WHISPER_ENABLED/ENDPOINT stay
		// env-only — mirroring the contact_form_enabled env-dependency pattern).
		captionjob.WithEnabledFunc(func() bool {
			return cfg.WhisperEnabled && settingssvc.Bool(instancesettings.KeyTranscriptionEnabled)
		}),
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
		account.WithMirror(ipfsMirror), // IPFS P19: unpin a deleted account's media
		// Export retention follows the user_export_expiration_hours overlay
		// (config-parity W8; default 168h, 0 = archives never expire), resolved
		// when each export finishes.
		account.WithExportTTLFunc(func() time.Duration {
			return time.Duration(settingssvc.Int(instancesettings.KeyUserExportExpirationHours)) * time.Hour
		}))
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
		// Federation policy gates (config-parity W12): runtime provider funcs
		// over the settings overlay, read per inbound activity so an admin
		// change applies without a restart. All gate the ActivityPub inbox
		// ONLY — the ATProto integration is outbound cross-posting with no
		// inbound path.
		federation.WithAcceptRemoteCommentsFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyFederationAcceptRemoteComments)
		}),
		federation.WithAllowChannelFollowersFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyFederationAllowChannelFollowers)
		}),
		federation.WithFollowerApprovalFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyFederationFollowerApproval)
		}),
		federation.WithAutoFollowBackFunc(func() bool {
			return settingssvc.Bool(instancesettings.KeyFederationAutoFollowBack)
		}),
		federation.WithLogger(logger),
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

	// ATProto IDENTITY LOGIN (sign in with a Bluesky / any-PDS handle) — distinct
	// from the cross-posting extension above and independent of it. Constructed
	// ALWAYS so the /auth/atproto/* routes are a stable contract; ATPROTO_LOGIN_ENABLED
	// gates start/callback at request time (503 when off). It keeps NO PDS tokens,
	// so it needs no KEK. Reuses the outbound-fetch dev knob (ImportAllowPrivateURLs)
	// so backed e2e can reach a loopback PDS/auth server; production stays https+public.
	atprotoLoginClient := atproto.NewOAuthClient(
		atproto.WithOAuthClientAllowPrivate(cfg.ImportAllowPrivateURLs),
	)
	atprotoLoginSvc := auth.NewATProtoOAuthService(db.Queries(), authsvc, atprotoLoginClient,
		auth.WithATProtoEnabled(cfg.ATProtoLoginEnabled),
		auth.WithATProtoPublicBaseURL(cfg.PublicBaseURL),
	)
	opts = append(opts, httpapi.WithATProtoLoginService(atprotoLoginSvc))

	// Remote-video read side (metadata + cached thumbnail) and instance-level
	// moderation (per-user instance mutes + admin blocklist). REST surface, so
	// wired unconditionally — the tables are simply empty until federation
	// ingests content.
	remotevideosvc := remotevideo.NewService(db.Queries(), blobs)
	opts = append(opts, httpapi.WithRemoteVideoService(remotevideosvc))
	instancemodsvc := instancemod.NewService(db.Queries())
	opts = append(opts, httpapi.WithInstanceModerationService(instancemodsvc))

	// LEADER ELECTION for the singleton background sweeps. The workers that CLAIM
	// from a durable queue run on every instance -- that is what the leases are
	// for, and more instances mean more throughput. The workers that SWEEP must
	// not: they each walk a table or a bucket and act on whatever they find, and
	// media garbage collection is destructive. One PostgreSQL advisory lock, held
	// on a dedicated connection, elects exactly one instance to run them; it is
	// released automatically if that instance dies.
	cronLeader := leaderlock.New(db.Pool, leaderlock.SingletonCronClass, leaderlock.SingletonCronsKey, "singleton-crons", logger)
	{
		leaderCtx, leaderCancel := context.WithCancel(context.Background())
		defer leaderCancel()
		go cronLeader.Run(leaderCtx)
	}

	// LEASE-EXPIRY JOB RECOVERY. Every durable queue claims work by flipping a
	// row to 'running' and pushing its due time forward by a lease that the
	// owning worker renews while it works. A worker that dies stops renewing, so
	// its rows come back on their own; nothing has to know which instances are
	// alive. That replaced a boot-time blanket requeue of every 'running' row,
	// which was safe only while this process was the deployment's only worker.
	//
	// Recovery therefore runs on a TICKER, not once at start-up: a worker that
	// dies an hour into the day is recovered within a lease rather than at the
	// next deploy. The first sweep still happens immediately, because a crash
	// right before this boot is the most likely reason there is anything to
	// recover.
	{
		sweepCtx, sweepCancel := context.WithCancel(context.Background())
		defer sweepCancel()
		sweep := func() {
			ctx, cancel := context.WithTimeout(sweepCtx, 30*time.Second)
			defer cancel()
			for _, r := range jobrecovery.Sweep(ctx, db.Queries()) {
				switch {
				case r.Err != nil:
					// Never fatal: a queue that cannot be swept is a degraded
					// instance, not a reason to refuse to serve.
					logger.Error("job recovery sweep failed", "queue", r.Queue, "error", r.Err)
				case r.Requeued > 0:
					logger.Warn("requeued jobs whose worker stopped renewing their lease",
						"queue", r.Queue, "requeued", r.Requeued)
				}
			}
		}
		sweep()
		go func() {
			t := time.NewTicker(jobRecoverySweepInterval)
			defer t.Stop()
			for {
				select {
				case <-sweepCtx.Done():
					return
				case <-t.C:
					sweep()
				}
			}
		}()
	}

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
		go runE2EESweepWorker(workerCtx, logger, e2eesvc, cronLeader)
		logger.Info("e2ee expiry sweep worker started")
	}

	// Publish scheduled videos as they come due (product-decisions §17). Always
	// on: the due scan is a cheap partial-index lookup, and the transition runs
	// the same publish hooks (federation announce, transcode enqueue) as a
	// direct publish.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runScheduledPublishWorker(workerCtx, logger, videosvc, cronLeader)
		logger.Info("scheduled publish worker started")
	}

	// Release publish-after-transcode holds stuck past the timeout (0098). Always
	// on: the stuck scan is a cheap partial-index lookup. This is the safety net
	// for crashed workers / lost jobs — the primary release paths are the
	// transcode completion and terminal-failure hooks.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runTranscodeHoldSweepWorker(workerCtx, logger, videosvc, cfg.TranscodeHoldTimeout, cronLeader)
		logger.Info("transcode hold sweep worker started", "timeout", cfg.TranscodeHoldTimeout)
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

	// Live max-duration watchdog (config-parity W11): force-close live sessions
	// that exceed live_max_duration_secs. Always on — the scan is a cheap lookup
	// over the handful of state='live' rows and an immediate no-op while the
	// limit is 0/unset, so the knob applies without a restart.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runLiveDurationWatchdog(workerCtx, logger, livesvc, cronLeader)
		logger.Info("live duration watchdog started")
	}

	// Sweep expired/cancelled resumable-upload sessions (the failed-upload
	// cleanup): removes the chunk blobs then the row. Always on: the sweep scan
	// is a cheap indexed lookup.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runUploadSweepWorker(workerCtx, logger, uploadsvc, cronLeader)
		logger.Info("upload session sweep worker started")
	}

	// Media garbage collection: a daily sweep of orphaned storage blobs. The
	// unattended sweep DELETES, so it is the one worker with an off switch —
	// boot-baked, because a runtime toggle would put an irreversible operation
	// behind a settings mistake.
	if cfg.MediaGCEnabled {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runMediaGCWorker(workerCtx, logger, mediagcsvc, auditsvc, cronLeader)
		logger.Info("media gc worker started",
			"max_orphan_percent", cfg.MediaGCMaxOrphanPercent,
			"bucket_ownership", string(bucketOwnership))
	} else {
		logger.Info("media gc worker disabled (MEDIA_GC_ENABLED=false); orphaned objects accumulate until an admin sweeps by hand")
	}

	// Content-hash backfill. Always on and with no flag: it only ever READS
	// objects and writes one text column, it drains to a no-op query once the
	// library is hashed, and every later phase-2 step (verified migration,
	// post-restore consistency) is unusable without it.
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runMediaHashBackfillWorker(workerCtx, logger, mediahashsvc, cronLeader)
		logger.Info("media hash backfill worker started")
	}

	// Storage migration (phase-2 storage, items 4-5). TWO workers, split the way
	// the worker-role doctrine requires:
	//
	//   the COPY worker is UNLEADERED — it claims object rows with FOR UPDATE
	//   SKIP LOCKED and renews a lease while it streams, so running it on every
	//   instance is safe and makes the move finish sooner;
	//
	//   the SWEEP worker is LEADER-GATED — enumerating a store, deciding a
	//   campaign is synced, and deleting the source are all whole-store decisions,
	//   and two instances making them at once would fight.
	//
	// Only started when a target is configured: with none there is nothing to copy
	// to, and the admin surface (which can still cancel a leftover campaign) is
	// mounted regardless.
	if storagemigrationsvc.Enabled() {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runStorageMigrationCopyWorker(workerCtx, logger, storagemigrationsvc)
		go runStorageMigrationSweepWorker(workerCtx, logger, storagemigrationsvc, cronLeader)
		logger.Info("storage migration workers started",
			"grace_hours", cfg.StorageMigrationGraceHours)
	}

	// Search outbox drain + reconcile sweep (search-service W4). Only when the
	// search service is wired. The outbox worker delivers enqueued events to
	// vidra-search on a 5s ticker; the reconcile worker performs a full
	// index-repair sweep at startup and every SEARCH_RECONCILE_INTERVAL.
	if searchDrainer != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runSearchOutboxWorker(workerCtx, logger, searchDrainer)
		go runSearchReconcileWorker(workerCtx, logger, searchEnqueuer, cfg.SearchReconcileInterval, cronLeader)
		// Active health prober (W9): drives Client.Healthy() so the routing policy
		// fails over to backup the moment /healthz goes down. Context-cancelled on
		// shutdown; never blocks it.
		go searchClient.RunHealthProbe(workerCtx)
		// Seed the effective config once at startup so a freshly-started search
		// service is configured even if no admin change follows.
		searchEnqueuer.EnqueueConfigUpdated(context.Background(), searchConfigFromSettings(settingssvc))
		logger.Info("search outbox + reconcile workers started", "reconcile_interval", cfg.SearchReconcileInterval.String(), "health_interval", cfg.SearchHealthInterval.String())
	}

	// Drain the IPFS mirror pin/unpin queue and periodically re-arm dead-letters
	// (fix_plan P19). Only when IPFS_ENABLED — the mirror is a sidecar, so this
	// never affects the authoritative write/serve paths.
	if ipfsMirror.Enabled() {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runIPFSMirrorWorker(workerCtx, logger, ipfsMirror, cfg.IPFSReconcileInterval, cronLeader)
		logger.Info("ipfs mirror worker started")
	}

	// Admin operations: the durable-queue depth snapshot + recent-failures
	// endpoint (P17.4) and the queue-depth Prometheus gauge both read from here.
	jobStatusSvc := jobstatus.NewService(db.Queries())
	opts = append(opts, httpapi.WithJobStatusService(jobStatusSvc))
	{
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		go runOperationalJobRetentionWorker(workerCtx, logger, jobStatusSvc, cronLeader)
		logger.Info("operational job retention worker started")
	}

	// PeerTube import / migration (fix_plan P18). The admin API is ALWAYS wired
	// (stable contract for the vidra-user import UI); the launch endpoint answers
	// 503 until a source is configured (PEERTUBE_IMPORT_ENABLED +
	// PEERTUBE_SOURCE_DATABASE_URL). The source connection is built from SERVER
	// CONFIG per run — never from a request; the browser never sends a DSN.
	defaultImportPolicy, _ := peertubeimport.ParseConflictPolicy(cfg.PeerTubeImportConflictPolicy)
	defaultImportMediaMode, _ := peertubeimport.ParseMediaMode(cfg.PeerTubeImportMediaMode)
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
				var srcMedia storage.Backend
				if defaultImportMediaMode == peertubeimport.MediaModeCopy {
					srcMedia, err = peertubeimport.OpenSourceStorage(srcStorageCfg)
					if err != nil {
						src.Close()
						return nil, nil, err
					}
				}
				imp := peertubeimport.NewImporter(db.Pool, src, peertubeimport.Options{
					Policy:    policy,
					MediaMode: defaultImportMediaMode,
					SrcMedia:  srcMedia,
					DestMedia: blobs,
					SealKey:   ptSeal,
					Logger:    logger,
				})
				return imp, func() { src.Close() }, nil
			}))
		logger.Info("peertube import configured", "source_storage", cfg.PeerTubeSourceStorageBackend, "media_mode", defaultImportMediaMode.String())
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
	if metrics != nil {
		metrics.RegisterQueueDepthSource(func(ctx context.Context) ([]observability.QueueDepth, error) {
			depths, err := jobStatusSvc.Depths(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]observability.QueueDepth, 0, len(depths)+3)
			for _, d := range depths {
				out = append(out, observability.QueueDepth{Queue: d.Queue, State: d.State, Count: d.Count})
			}
			// Search outbox depth by state (search-service W4).
			if cfg.SearchServiceEnabled() {
				if rows, derr := db.Queries().SearchOutboxDepth(ctx); derr == nil {
					for _, r := range rows {
						out = append(out, observability.QueueDepth{Queue: "search_outbox", State: r.State, Count: r.Depth})
					}
				}
			}
			return out, nil
		})
		metrics.RegisterJobQueueHealthSource(func(ctx context.Context) ([]observability.JobQueueHealth, error) {
			health, err := jobStatusSvc.QueueHealth(ctx, 5*time.Minute)
			if err != nil {
				return nil, err
			}
			out := make([]observability.JobQueueHealth, len(health))
			for i, row := range health {
				out[i] = observability.JobQueueHealth{
					Queue: row.Queue, OldestQueuedAgeSeconds: row.OldestQueuedAgeSeconds,
					StaleRunning: row.StaleRunning,
				}
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

// runSearchOutboxWorker drains the search event outbox on a 5s ticker until ctx
// is canceled (search-service W4). A single worker claims a batch (≤200) per pass
// and delivers it to vidra-search; per-event failures are persisted in the queue
// (retry/backoff/dead-letter), so only the claim-query error is logged.
func runSearchOutboxWorker(ctx context.Context, logger *slog.Logger, drainer *searchevents.Drainer) {
	const (
		interval = 5 * time.Second
		batch    = 200
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := drainer.Drain(ctx, batch); err != nil {
				logger.Warn("search outbox drain failed", "error", err)
			}
		}
	}
}

// runSearchReconcileWorker performs a full index-reconciliation sweep at startup
// and then every interval (search-service W4). The sweep pages every eligible
// public+published local video into reconcile.begin/page/end events so the search
// service can repair the index against any dropped incremental event.
func runSearchReconcileWorker(ctx context.Context, logger *slog.Logger, enq *searchevents.Enqueuer, interval time.Duration, leader *leaderlock.Elector) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if err := enq.RunReconcile(ctx, searchevents.DefaultReconcilePageSize); err != nil {
		logger.Warn("search reconcile sweep failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			if err := enq.RunReconcile(ctx, searchevents.DefaultReconcilePageSize); err != nil {
				logger.Warn("search reconcile sweep failed", "error", err)
			}
		}
	}
}

// searchConfigFromSettings builds the effective search-config subset pushed to
// vidra-search on startup (search-service W4).
func searchConfigFromSettings(s *instancesettings.Service) searchevents.SearchConfig {
	return searchevents.SearchConfig{
		SearchMode:                         s.String(instancesettings.KeySearchMode),
		SuggestionsEnabled:                 s.Bool(instancesettings.KeySearchSuggestionsEnabled),
		PersonalizedSearchEnabled:          s.Bool(instancesettings.KeyPersonalizedSearchEnabled),
		PersonalizedRecommendationsEnabled: s.Bool(instancesettings.KeyPersonalizedRecommendationsEnabled),
		SearchHistoryEnabled:               s.Bool(instancesettings.KeySearchHistoryEnabled),
		SearchEventRetentionDays:           s.Int(instancesettings.KeySearchEventRetentionDays),
		MinimumQueryUserCount:              s.Int(instancesettings.KeySearchMinQueryUserCount),
		HideSensitiveDefault:               s.String(instancesettings.KeySensitiveContentPolicy) == instancesettings.SensitiveContentPolicyHide,
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
// ctx is canceled (mirrors runFederationDeliveryWorker). A single worker
// goroutine claims a batch per pass and runs it on the service's bounded pool
// (transcoding_concurrency, config-parity W10); the batch size follows the
// effective concurrency per pass — minimum 2, the historical batch — so a
// runtime change applies without a restart. It logs only claim-query errors —
// per-job failures are recorded in the queue (retry/backoff/dead-letter)
// rather than logged.
func runTranscodeWorker(ctx context.Context, logger *slog.Logger, svc *transcode.Service) {
	const interval = 10 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Drain the whole due backlog, batch by batch, so a burst of
			// uploads doesn't wait a tick per batch of jobs. DrainJobs only
			// counts completions, so a persistently failing job ends the
			// inner loop and backoff retries it on a later tick.
			total := 0
			for {
				n, err := svc.DrainJobs(ctx, drainBatch(svc.Concurrency()))
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

// drainBatch sizes a queue worker's per-pass claim from its effective
// concurrency (config-parity W10): at least the historical batch of 2 so the
// backlog drains as before, and at least the pool width so a raised
// concurrency is actually saturated.
func drainBatch(concurrency int) int {
	if concurrency < 2 {
		return 2
	}
	return concurrency
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
func runE2EESweepWorker(ctx context.Context, logger *slog.Logger, svc *e2ee.Service, leader *leaderlock.Elector) {
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
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			if n, err := svc.SweepExpired(ctx, sweepBatch); err != nil {
				logger.Warn("e2ee expiry sweep failed", "error", err)
			} else if n > 0 {
				logger.Info("e2ee expiry sweep removed expired messages", "count", n)
			}
		}
	}
}

// runLiveDurationWatchdog force-closes live sessions that exceed the effective
// live_max_duration_secs on a ticker until ctx is canceled (mirrors
// runE2EESweepWorker; config-parity W11). The limit is read per sweep through
// the service's provider seam, so admin changes apply without a restart; a
// 0/unset limit makes each sweep a free no-op. Note the enforcement reality
// documented on SweepOverdueLive: with no nginx-rtmp control endpoint in the
// deployed media config this is a server-side close (HLS serving + listings
// stop immediately); the publisher's ingest socket lingers until it disconnects,
// which then drives the normal stop/replay path.
func runLiveDurationWatchdog(ctx context.Context, logger *slog.Logger, svc *live.Service, leader *leaderlock.Elector) {
	const interval = 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			if n, err := svc.SweepOverdueLive(ctx); err != nil {
				logger.Warn("live duration watchdog sweep failed", "error", err)
			} else if n > 0 {
				logger.Info("live duration watchdog force-closed over-limit sessions", "count", n)
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
func runScheduledPublishWorker(ctx context.Context, logger *slog.Logger, svc *video.Service, leader *leaderlock.Elector) {
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
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
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

// composeTranscodeCompletion builds the transcode completion hook: the
// publish-after-transcode hold release runs BEFORE the IPFS mirror sync, and
// that order is load-bearing. The mirror routes eligibility on COMMITTED state
// (internal/ipfsmirror Route: any non-published video → NetworkNone), so
// syncing while the video is still parked in 'transcoding' would skip the
// HLS-tree pin — permanently, because the later publish-hook SyncVideo only
// re-pins single-file refs (videoFileMirrorClass deliberately excludes 'hls').
// Releasing first is safe for the normal (non-held) path: the release is a
// state-guarded CAS no-op for any video not in the 'transcoding' state.
func composeTranscodeCompletion(releaseHold, mirrorSync func(context.Context, uuid.UUID)) func(context.Context, uuid.UUID) {
	return func(ctx context.Context, videoID uuid.UUID) {
		if releaseHold != nil {
			releaseHold(ctx, videoID)
		}
		if mirrorSync != nil {
			mirrorSync(ctx, videoID)
		}
	}
}

// runTranscodeHoldSweepWorker releases publish-after-transcode videos stuck in
// the 'transcoding' hold past the timeout (0098): a crashed worker or a lost job
// would otherwise hide a video forever, so the sweeper publishes it from its
// (playable) original. Modeled on runScheduledPublishWorker; a rare backstop, so
// a released batch is logged at warn level.
func runTranscodeHoldSweepWorker(ctx context.Context, logger *slog.Logger, svc *video.Service, timeout time.Duration, leader *leaderlock.Elector) {
	const (
		interval = 5 * time.Minute
		batch    = 20
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			n, err := svc.ReleaseStuckTranscodeHolds(ctx, timeout, batch)
			if err != nil {
				logger.Warn("transcode hold sweep failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Warn("transcode hold sweep released stuck videos", "count", n)
			}
		}
	}
}

// runVideoImportWorker drains the durable URL-import queue on a ticker until
// ctx is canceled (mirrors runTranscodeWorker). The claimed batch runs on the
// service's bounded pool (import_jobs_concurrency, config-parity W10), the
// claim size following the effective concurrency per pass. Per-job failures
// are recorded in the queue (retry/backoff/dead-letter); only the claim-query
// error is logged.
func runVideoImportWorker(ctx context.Context, logger *slog.Logger, svc *videoimport.Service) {
	const interval = 10 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			total := 0
			for {
				n, err := svc.DrainJobs(ctx, drainBatch(svc.Concurrency()))
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
func runUploadSweepWorker(ctx context.Context, logger *slog.Logger, svc *upload.Service, leader *leaderlock.Elector) {
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
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
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
func runIPFSMirrorWorker(ctx context.Context, logger *slog.Logger, svc *ipfsmirror.Service, reconcileInterval time.Duration, leader *leaderlock.Elector) {
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
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
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

// runStorageMigrationCopyWorker drains the storage-migration object queue on a
// short ticker until ctx is canceled.
//
// DELIBERATELY UNLEADERED. The queue claims with FOR UPDATE SKIP LOCKED and each
// claimed object's lease is renewed while it copies, so concurrent instances take
// disjoint objects and a dead worker's object comes back on its own. More
// instances therefore mean a faster move, which is the whole point of putting a
// library migration on the durable-queue convention.
//
// The batch is small because each item is a whole media object: four concurrent
// streams per tick keeps the copy from monopolising the same network the instance
// is serving playback over.
func runStorageMigrationCopyWorker(ctx context.Context, logger *slog.Logger, svc *storagemigration.Service) {
	const (
		interval = 10 * time.Second
		batch    = 4
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
				n, err := svc.CopyOnce(ctx, batch)
				if err != nil {
					logger.Warn("storage migration copy pass failed", "error", err)
					break
				}
				total += n
				if n == 0 {
					break
				}
			}
			if total > 0 {
				logger.Info("storage migration copied objects", "count", total)
			}
		}
	}
}

// runStorageMigrationSweepWorker advances the campaign state machine on a ticker
// until ctx is canceled: enumerate, reconcile, notice cutover, and — after the
// grace period — delete the source copies.
//
// LEADER-GATED. Every one of those steps acts on whatever it finds across a whole
// store, and the last one is irreversible.
func runStorageMigrationSweepWorker(ctx context.Context, logger *slog.Logger, svc *storagemigration.Service, leader *leaderlock.Elector) {
	const interval = time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			if err := svc.SweepOnce(ctx); err != nil {
				logger.Warn("storage migration sweep failed", "error", err)
			}
		}
	}
}

// runMediaGCWorker runs the media garbage collector once a day (deleting
// orphaned storage blobs) until ctx is canceled. Each run is audited with its
// counts under an explicit system actor. A listing-unsupported backend disables the sweep
// after one warning.
//
// The FIRST sweep of a process lifetime is always a dry run, and it happens five
// minutes after boot rather than a day later. That is the rail an operator can
// actually use: a misconfiguration that would make this worker delete a library
// is visible in the log and the audit trail within minutes of the deploy that
// introduced it, instead of overnight and after the fact. Whichever timer fires
// first spends that dry run; deletion starts from the sweep after it.
func runMediaGCWorker(ctx context.Context, logger *slog.Logger, svc *mediagc.Service, auditsvc *audit.Service, leader *leaderlock.Elector) {
	const interval = 24 * time.Hour
	// Long enough that the boot storm (migrations, the first transcodes, a cold
	// object store) is over, short enough to still be the same deploy.
	const firstSweepDelay = 5 * time.Minute

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	initial := time.NewTimer(firstSweepDelay)
	defer initial.Stop()

	swept := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
		case <-ticker.C:
		}
		// Singleton sweep: exactly one instance runs it. A follower skips the
		// tick rather than shutting down, because leadership can move here at
		// any time (see internal/leaderlock). A skipped tick is not a sweep, so
		// it does not spend the dry run either.
		if !leader.IsLeader() {
			continue
		}
		dryRun := !swept
		res, err := svc.Sweep(ctx, dryRun)
		if err != nil {
			if errors.Is(err, mediagc.ErrListingUnsupported) {
				logger.Warn("media gc disabled: storage backend does not support listing")
				return
			}
			logger.Warn("media gc sweep failed", "error", err)
			continue
		}
		swept = true
		logger.Info("media gc sweep completed",
			"mode", res.Mode, "scanned", res.Scanned, "orphans", len(res.Orphans),
			"orphan_percent", res.OrphanPercent, "deleted", res.Deleted,
			"breaker_tripped", res.BreakerTripped, "bucket_ownership", res.BucketOwnership,
			"forced_dry_run", res.ForcedDryRun)
		if res.BreakerTripped {
			logger.Error("media gc deleted nothing: the orphan share of the store is over MEDIA_GC_MAX_ORPHAN_PERCENT, which is what a wrong reference set looks like",
				"orphan_percent", res.OrphanPercent, "scanned", res.Scanned, "orphans", len(res.Orphans))
		}
		if auditsvc != nil {
			_ = auditsvc.Record(ctx, audit.Event{
				Action: observability.ActionMediaGC,
				Result: observability.ResultSuccess,
				Actor:  audit.ActorSnapshot{Kind: "system"},
				Reason: res.Summary(),
			})
		}
	}
}

// runMediaHashBackfillWorker computes the content hash of stored media files
// whose video_files row does not carry one yet, a small batch per tick, until
// ctx is canceled (phase-2 storage, work item 2).
//
// It has no enable flag on purpose. The sweep reads objects and writes one text
// column — it can neither delete nor rewrite media — and it drains: once every
// row is hashed the tick costs one indexed query that returns nothing, which is
// the steady state for the entire life of the install. The alternative, an
// operator who forgets to turn it on, shows up much later as a storage migration
// that cannot verify what it copied.
//
// Objects the store does not have are recorded with the missing sentinel rather
// than retried forever; anything else that fails is simply left for the next
// tick.
func runMediaHashBackfillWorker(ctx context.Context, logger *slog.Logger, svc *mediahash.Service, leader *leaderlock.Elector) {
	const (
		interval = time.Minute
		batch    = 25
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Completion is logged on the edge, not every tick: a drained backfill
	// otherwise writes one line a minute forever.
	drained := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			res, err := svc.BackfillOnce(ctx, batch)
			if err != nil {
				logger.Warn("media hash backfill failed", "error", err)
				continue
			}
			if res.Scanned == 0 {
				if !drained {
					drained = true
					logger.Info("media hash backfill complete: every stored media file carries a content hash or the missing sentinel")
				}
				continue
			}
			drained = false
			logger.Info("media hash backfill progressed",
				"scanned", res.Scanned, "hashed", res.Hashed, "missing", res.Missing, "failed", res.Failed)
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

// runOperationalJobRetentionWorker prunes only bounded batches. Events are
// removed after 30 days only when their parent is terminal; terminal runs and
// pipelines are retained for 90 days. Active execution history is never pruned.
func runOperationalJobRetentionWorker(ctx context.Context, logger *slog.Logger, svc *jobstatus.Service, leader *leaderlock.Elector) {
	const interval = 24 * time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if !leader.IsLeader() {
				continue
			}
			events, runs, pipelines, err := svc.Prune(ctx, now.UTC())
			if err != nil {
				logger.Warn("operational job retention failed", "error", err)
				continue
			}
			if events+runs+pipelines > 0 {
				logger.Info("operational job retention pruned rows",
					"events", events, "runs", runs, "pipelines", pipelines)
			}
		}
	}
}

// storageSpec is one store's worth of configuration, so the primary media
// backend and the storage-migration target are built by the SAME code. They have
// to be: a target built by a near-copy of this function is exactly how a
// migration ends up writing into a store with different semantics than the one
// it is reading from.
type storageSpec struct {
	backend   string
	localRoot string
	s3        storage.S3Config
}

func primaryStorageSpec(cfg *config.Config) storageSpec {
	return storageSpec{
		backend:   cfg.StorageBackend,
		localRoot: cfg.StorageLocalRoot,
		s3: storage.S3Config{
			Endpoint:       cfg.StorageS3Endpoint,
			Bucket:         cfg.StorageS3Bucket,
			AccessKey:      cfg.StorageS3AccessKey,
			SecretKey:      cfg.StorageS3SecretKey,
			Region:         cfg.StorageS3Region,
			UseSSL:         cfg.StorageS3UseSSL,
			ForcePathStyle: cfg.StorageS3ForcePathStyle,
		},
	}
}

func migrationTargetStorageSpec(cfg *config.Config) storageSpec {
	return storageSpec{
		backend:   cfg.StorageMigrationTargetBackend,
		localRoot: cfg.StorageMigrationTargetLocalRoot,
		s3: storage.S3Config{
			Endpoint:       cfg.StorageMigrationTargetS3Endpoint,
			Bucket:         cfg.StorageMigrationTargetS3Bucket,
			AccessKey:      cfg.StorageMigrationTargetS3AccessKey,
			SecretKey:      cfg.StorageMigrationTargetS3SecretKey,
			Region:         cfg.StorageMigrationTargetS3Region,
			UseSSL:         cfg.StorageMigrationTargetS3UseSSL,
			ForcePathStyle: cfg.StorageMigrationTargetS3ForcePathStyle,
		},
	}
}

// buildStorageBackend builds one blob backend from a spec. Config validation
// already restricts the backend name to the supported set, so the default branch
// is a defensive guard. ctx bounds the s3 startup probe (EnsureBucket) so an
// unreachable store fails fast like a missing DB.
//
// createdBucket reports that this boot MADE the bucket, which resolveBucketOwnership
// needs: a bucket that did not exist a moment ago cannot hold anyone else's
// media, and is therefore the one case where this install may claim ownership of
// an object store without an operator saying so.
func buildStorageBackend(ctx context.Context, spec storageSpec) (blobs storage.Backend, createdBucket bool, err error) {
	switch spec.backend {
	case "local":
		local, lerr := storage.NewLocal(spec.localRoot)
		if lerr != nil {
			return nil, false, lerr
		}
		return local, false, nil
	case "s3":
		s3b, serr := storage.NewS3(spec.s3)
		if serr != nil {
			return nil, false, serr
		}
		created, berr := s3b.EnsureBucket(ctx)
		if berr != nil {
			return nil, false, berr
		}
		return s3b, created, nil
	default:
		return nil, false, fmt.Errorf("unsupported storage backend %q", spec.backend)
	}
}

// newStorageBackend builds the AUTHORITATIVE media blob backend selected by
// config.
func newStorageBackend(ctx context.Context, cfg *config.Config) (storage.Backend, bool, error) {
	return buildStorageBackend(ctx, primaryStorageSpec(cfg))
}

// newMigrationTargetBackend builds the SECOND backend a storage migration copies
// into (STORAGE_MIGRATION_TARGET_*), or (nil, nil) when the feature is off.
//
// It refuses a target that is the same store as the primary. Config validation
// already compares the configured values; this compares the IDENTITIES the built
// handles actually report, which is the version that cannot be fooled by two
// different spellings of one bucket.
func newMigrationTargetBackend(ctx context.Context, cfg *config.Config, primary storage.Backend) (storage.Backend, error) {
	if !cfg.StorageMigrationConfigured() {
		return nil, nil
	}
	target, _, err := buildStorageBackend(ctx, migrationTargetStorageSpec(cfg))
	if err != nil {
		return nil, fmt.Errorf("storage migration target: %w", err)
	}
	src, dst := storage.Describe(primary), storage.Describe(target)
	if src == "" || dst == "" {
		return nil, fmt.Errorf("storage migration target: a configured storage backend does not report its identity")
	}
	if src == dst {
		return nil, fmt.Errorf("storage migration target: STORAGE_MIGRATION_TARGET_* names the same store as STORAGE_* (%s)", src)
	}
	return target, nil
}

// resolveBucketOwnership answers, once per boot, whether the object store media
// garbage collection is about to delete from belongs to THIS install.
//
// The evidence is a marker object holding the instance identity
// (storage.OwnerMarkerKey). Four outcomes, and only the first two permit a
// destructive sweep:
//
//	marker == our identity                     → owned
//	no marker, and the bucket is ours to claim → written, owned
//	no marker, and the bucket has objects in it→ unowned  (operator must adopt)
//	marker == a different identity             → conflict (another install owns it)
//
// "Ours to claim" is the bucket this boot just created, or one that holds
// nothing at all. Anything else is somebody's data, and the only thing that can
// establish whose is an operator — hence the adopt-bucket endpoint.
//
// Every failure resolves to unowned rather than owned: the whole point is that
// an unanswerable question must not end in a delete. Local disk is exempt by
// design (see mediagc.OwnershipNotApplicable) and never reaches this function.
func resolveBucketOwnership(ctx context.Context, logger *slog.Logger, blobs storage.Backend, createdBucket bool, identity string) mediagc.BucketOwnership {
	s3b, isObjectStore := blobs.(*storage.S3)
	if !isObjectStore {
		return mediagc.OwnershipNotApplicable
	}
	if identity == "" {
		logger.Warn("media gc: no instance identity, so bucket ownership cannot be established — destructive sweeps are disabled")
		return mediagc.OwnershipUnowned
	}
	marker, found, err := storage.ReadOwnerMarker(ctx, blobs)
	switch {
	case err != nil:
		logger.Warn("media gc: the bucket ownership marker could not be read — destructive sweeps are disabled", "error", err)
		return mediagc.OwnershipUnowned
	case found && marker == identity:
		return mediagc.OwnershipOwned
	case found:
		// Deliberately NOT logging the other identity as a plain value an
		// operator might mistake for theirs — the state is the actionable part.
		logger.Error("media gc: this bucket carries ANOTHER Vidra install's ownership marker; destructive sweeps are disabled",
			"marker_key", storage.OwnerMarkerKey)
		return mediagc.OwnershipConflict
	}

	// No marker. Claim it only when there is provably nobody else's data here.
	claimable := createdBucket
	if !claimable {
		empty, eerr := s3b.IsEmpty(ctx)
		if eerr != nil {
			logger.Warn("media gc: the bucket could not be checked for existing objects — destructive sweeps are disabled", "error", eerr)
			return mediagc.OwnershipUnowned
		}
		claimable = empty
	}
	if !claimable {
		logger.Warn("media gc: this bucket holds objects but carries no ownership marker, so it is not established that it belongs to this instance; destructive sweeps are disabled until an admin adopts it",
			"marker_key", storage.OwnerMarkerKey, "adopt", "POST /api/v1/admin/media/gc/adopt-bucket")
		return mediagc.OwnershipUnowned
	}
	if werr := storage.WriteOwnerMarker(ctx, blobs, identity); werr != nil {
		logger.Warn("media gc: the bucket ownership marker could not be written — destructive sweeps are disabled", "error", werr)
		return mediagc.OwnershipUnowned
	}
	logger.Info("media gc: claimed the object store with an ownership marker", "marker_key", storage.OwnerMarkerKey)
	return mediagc.OwnershipOwned
}
