// Package httpapi wires the Echo HTTP server: routing, middleware, and
// request/response handling. Handlers are intentionally thin; application
// logic lives in service packages.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/account"
	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/block"
	"github.com/vidra/vidra-core/internal/captionjob"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/channelsync"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/playback"
	"github.com/vidra/vidra-core/internal/playersettings"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/quota"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/transcode"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/watchword"
)

// Pinger is satisfied by dependencies that can report liveness (store, cache).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server holds the Echo instance and its dependencies.
type Server struct {
	echo      *echo.Echo
	cfg       *config.Config
	db        Pinger
	rdb       Pinger
	startedAt time.Time
	logger    *slog.Logger
	limiter   *ratelimit.Limiter
	authLimit *ratelimit.Limiter
	// unlockLimit throttles POST /videos/{id}/unlock (a password-guessing
	// surface). Derived in New(): the configured auth limiter when present
	// (WithAuthRateLimiter), else a built-in, always-on in-memory default so the
	// endpoint is never left unthrottled — even on a single-node deployment
	// running without Redis. Always non-nil after New().
	unlockLimit     *ratelimit.Limiter
	attachmentLimit *ratelimit.Limiter
	// contactLimit throttles the public contact form (POST /instance/contact) —
	// its own fixed-window budget, 1 request/IP/hour in production (wired in
	// cmd/api via WithContactRateLimiter). Nil (e.g. unit tests) mounts the
	// route unthrottled beyond the general limiter.
	contactLimit *ratelimit.Limiter
	// contactMailer delivers contact-form messages to the operator (spec
	// instance-platform-info). Nil when the deployment has no outbound mail
	// path — the contact form then reports unavailable (409 / effective
	// contact_form_enabled=false).
	contactMailer     auth.Mailer
	authsvc           *auth.Service
	authTTL           time.Duration
	accountsvc        *account.Service
	oauthsvc          *auth.OAuthService
	channelsvc        *channel.Service
	donationsvc       *donation.Service
	videosvc          *video.Service
	commentsvc        *comment.Service
	ratingsvc         *rating.Service
	notifsvc          *notification.Service
	playersettingssvc *playersettings.Service
	playlistsvc       *playlist.Service
	moderationsvc     *moderation.Service
	mutesvc           *mute.Service
	blocksvc          *block.Service
	watchwordsvc      *watchword.Service
	adminsvc          *admin.Service
	auditLog          *audit.Service
	messagingsvc      *messaging.Service
	e2eesvc           *e2ee.Service
	livesvc           *live.Service
	imagesvc          *profileimage.Service
	quotasvc          *quota.Service
	transcodesvc      *transcode.Service
	uploadsvc         *upload.Service
	importsvc         *videoimport.Service
	channelsyncsvc    *channelsync.Service
	captionjobsvc     *captionjob.Service
	fedsvc            *federation.Service
	atprotosvc        *atproto.Service
	remotevideosvc    *remotevideo.Service
	instancemodsvc    *instancemod.Service
	settingssvc       *instancesettings.Service
	mediagcsvc        *mediagc.Service
	jobStatusSvc      jobStatusProvider
	peertubeimportsvc peerTubeImportProvider
	ipfsmirrorsvc     ipfsMirrorProvider
	metrics           *observability.Metrics
	media             storage.Backend
	// playbackSigner mints/verifies the short-lived, video-scoped playback tokens
	// that unlock password-protected videos (CORE-17 / W1.C2). Derived in New()
	// from the JWT secret via domain separation, so it is always present.
	playbackSigner *playback.Signer
	// devMailCapture, when set (DEV_MAIL_CAPTURE_ENABLED only), exposes captured
	// account-security tokens via GET /api/v1/dev/email-token. Nil in production.
	devMailCapture *auth.CaptureMailer
}

// uploadRoutePath is the Echo route template for the original-file upload. It is
// exempted from the default body limit (which gets its own larger one).
const uploadRoutePath = "/api/v1/videos/:id/file"

// uploadChunkRoutePath is the Echo route template for a resumable-upload chunk
// PUT. Like uploadRoutePath it is exempted from the default JSON body limit —
// the upload service bounds each chunk at the fixed chunk size itself.
const uploadChunkRoutePath = "/api/v1/uploads/:upload_id/chunks/:n"

// attachmentUploadRoutePath is the Echo route template for a DM attachment
// upload. Exempted from the default JSON body limit; the messaging service caps
// each attachment at 100 MiB itself (messaging-v2.md D6).
const attachmentUploadRoutePath = "/api/v1/conversations/:id/attachments"

// Option customises the Server during construction.
type Option func(*Server)

// WithRateLimiter mounts fixed-window rate limiting (per client IP) on the API
// surface. When nil or unset, no rate limiting is applied — handy for unit tests.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(s *Server) { s.limiter = l }
}

// WithAuthService mounts the auth endpoints (register/login). ttl is the access
// token lifetime, reported to clients as expires_in. When unset, the auth routes
// are not registered.
func WithAuthService(svc *auth.Service, ttl time.Duration) Option {
	return func(s *Server) {
		s.authsvc = svc
		s.authTTL = ttl
	}
}

// WithAccountService mounts the account lifecycle endpoints: the §1 hard
// delete (self-service DELETE /auth/me + the admin variant), the P4 account
// export (request/status/download), and the import foundation. The self-delete
// re-confirms the password through the auth service, so these routes register
// only when both are wired.
func WithAccountService(svc *account.Service) Option {
	return func(s *Server) { s.accountsvc = svc }
}

// WithOAuthService mounts the OIDC login endpoints (begin/callback per
// provider) and the linked-identity management routes. The flow routes need
// the auth service too (it issues the resulting session); when either is
// unset, none are registered. With zero configured providers the routes exist
// but every provider name 404s.
func WithOAuthService(svc *auth.OAuthService) Option {
	return func(s *Server) { s.oauthsvc = svc }
}

// WithAuthRateLimiter mounts a stricter, dedicated limiter on the sensitive auth
// endpoints (login, register, password-reset/verify confirmations). It is keyed
// independently of the general API limiter and emits an audit event on denial.
// When nil/unset, those routes fall back to the general limiter only.
func WithAuthRateLimiter(l *ratelimit.Limiter) Option {
	return func(s *Server) { s.authLimit = l }
}

// WithAttachmentRateLimiter mounts a per-USER limiter on the DM attachment upload
// route (messaging-v2.md D6). DM attachments do not count against the storage
// quota, so this per-user upload budget is the compensating anti-abuse control.
// It is keyed by authenticated user id (not IP) and layered over the general
// per-IP limiter. When nil/unset, the upload route is not extra-throttled.
func WithAttachmentRateLimiter(l *ratelimit.Limiter) Option {
	return func(s *Server) { s.attachmentLimit = l }
}

// WithContactRateLimiter mounts a dedicated per-IP limiter on the public
// contact form (POST /instance/contact): 1 request per IP per hour in
// production wiring. Keyed independently of the general/auth limiters. When
// nil/unset, the route falls back to the general limiter only.
func WithContactRateLimiter(l *ratelimit.Limiter) Option {
	return func(s *Server) { s.contactLimit = l }
}

// WithContactMailer wires the outbound mail path the public contact form
// delivers through (the same effective mailer the auth flows use: dev capture
// wins over SMTP). When unset, the contact form is reported unavailable
// (effective contact_form_enabled=false; POST answers 409).
func WithContactMailer(m auth.Mailer) Option {
	return func(s *Server) { s.contactMailer = m }
}

// WithChannelService mounts the channel endpoints (create/list-own/get-by-handle).
// When unset, the channel routes are not registered.
func WithChannelService(svc *channel.Service) Option {
	return func(s *Server) { s.channelsvc = svc }
}

// WithDonationService mounts the simple, NON-CUSTODIAL crypto donation-address
// endpoints (P14): the caller's CRUD under /me/donation-addresses, the
// signed-challenge verification flow, and the public per-user/per-channel
// reads. The public channel read resolves a handle, so it registers only when
// the channel service is also present. When unset, none are registered.
func WithDonationService(svc *donation.Service) Option {
	return func(s *Server) { s.donationsvc = svc }
}

// WithVideoService mounts the video endpoints (create draft, get by id). Video
// creation also needs the channel service (for ownership); when either is unset
// the video routes are not registered.
func WithVideoService(svc *video.Service) Option {
	return func(s *Server) { s.videosvc = svc }
}

// WithCommentService mounts the comment endpoints. Comments are scoped to videos,
// so the comment routes register only when the video service is also present.
func WithCommentService(svc *comment.Service) Option {
	return func(s *Server) { s.commentsvc = svc }
}

// WithRatingService mounts the video rating endpoints. Ratings are scoped to
// videos, so the routes register only when the video service is also present.
func WithRatingService(svc *rating.Service) Option {
	return func(s *Server) { s.ratingsvc = svc }
}

// WithNotificationService mounts the notification endpoints and enables the
// follow/comment notification side effects. When unset, the routes are not
// registered and no notifications are created.
func WithNotificationService(svc *notification.Service) Option {
	return func(s *Server) { s.notifsvc = svc }
}

// WithPlayerSettingsService mounts the per-user player-settings endpoints
// (GET/PUT /me/player-settings, PLAY-07). When unset, the routes are not
// registered.
func WithPlayerSettingsService(svc *playersettings.Service) Option {
	return func(s *Server) { s.playersettingssvc = svc }
}

// WithPlaylistService mounts the playlist endpoints (create/list/get/update/
// delete + add/remove item). Adding an item validates the video via the video
// service, so playlist routes register only when both are present.
func WithPlaylistService(svc *playlist.Service) Option {
	return func(s *Server) { s.playlistsvc = svc }
}

// WithModerationService mounts the abuse-report endpoints: reporting a video or
// comment, and the admin/moderator queue (list + resolve). Reporting a video
// needs the video service (for the public-video guard), so the report routes
// register only when both are present.
func WithModerationService(svc *moderation.Service) Option {
	return func(s *Server) { s.moderationsvc = svc }
}

// WithMuteService mounts the account-mute endpoints (mute / unmute / list the
// accounts the caller has muted). When unset, the routes are not registered.
func WithMuteService(svc *mute.Service) Option {
	return func(s *Server) { s.mutesvc = svc }
}

// WithBlockService mounts the account-block endpoints (block / unblock / list the
// accounts the caller has blocked). When unset, the routes are not registered.
// The messaging service is given this same service as its Blocker in cmd/api so
// a block gates direct messaging in both directions.
func WithBlockService(svc *block.Service) Option {
	return func(s *Server) { s.blocksvc = svc }
}

// WithWatchWordService mounts the moderation watched-words endpoints (add / list /
// delete instance-wide watched terms). When unset, the routes are not registered.
func WithWatchWordService(svc *watchword.Service) Option {
	return func(s *Server) { s.watchwordsvc = svc }
}

// WithAdminService mounts the admin user-management endpoints (list/search users,
// edit role + active flag). When unset, the routes are not registered.
func WithAdminService(svc *admin.Service) Option {
	return func(s *Server) { s.adminsvc = svc }
}

// WithMessagingService mounts the direct-messaging endpoints (start/list
// conversations, send/list messages). When unset, the routes are not registered.
func WithMessagingService(svc *messaging.Service) Option {
	return func(s *Server) { s.messagingsvc = svc }
}

// WithE2EEService mounts the E2EE endpoints (device registry, one-time-key
// upload/count/claim) and enables the encrypted branch of the conversation
// endpoints (start {encrypted:true}, envelope send/read). When unset, none of
// that surface exists and plaintext messaging behaves exactly as before.
func WithE2EEService(svc *e2ee.Service) Option {
	return func(s *Server) { s.e2eesvc = svc }
}

// WithLiveService mounts the live-stream endpoints (create/list/get/delete +
// stream-key regeneration). When unset, the routes are not registered.
func WithLiveService(svc *live.Service) Option {
	return func(s *Server) { s.livesvc = svc }
}

// WithProfileImageService mounts the avatar/banner endpoints: upload/delete for
// the caller's own account, owner-only upload/delete per channel, and the
// public serving routes. The channel routes additionally need the channel
// service (for handle resolution + ownership); when unset, none are registered.
func WithProfileImageService(svc *profileimage.Service) Option {
	return func(s *Server) { s.imagesvc = svc }
}

// WithQuotaService wires per-user storage quotas: GET /api/v1/me/quota (the
// caller's usage + effective cap) and enforcement on the original-file upload
// and URL import (422 quota_exceeded when the incoming file would not fit).
// When unset, no quota route is registered and uploads are never quota-checked.
func WithQuotaService(svc *quota.Service) Option {
	return func(s *Server) { s.quotasvc = svc }
}

// WithTranscodeService wires the HLS transcoding read side: the video detail's
// hls_url/renditions fields and the /videos/{id}/hls/* serving routes (which
// register only when the video service is also present). The write side (the
// enqueue hook + worker) is wired in cmd/api, gated by TRANSCODING_ENABLED.
func WithTranscodeService(svc *transcode.Service) Option {
	return func(s *Server) { s.transcodesvc = svc }
}

// WithMediaGCService mounts the admin media garbage-collection endpoint
// (POST /admin/media/gc). The scheduled daily sweep is wired separately in
// cmd/api.
func WithMediaGCService(svc *mediagc.Service) Option {
	return func(s *Server) { s.mediagcsvc = svc }
}

// WithJobStatusService mounts the admin operations jobs endpoint
// (GET /admin/jobs) — the per-queue depth snapshot + recent-failures list that
// backs the admin jobs page (P17.4). When unset, the route is not registered.
func WithJobStatusService(svc jobStatusProvider) Option {
	return func(s *Server) { s.jobStatusSvc = svc }
}

// WithPeerTubeImportService mounts the admin PeerTube-import endpoints (launch a
// dry-run/run, list runs, poll one run). The routes are ALWAYS registered when
// the service is wired (stable contract for the vidra-user admin "Import from
// PeerTube" UI); the launch endpoint answers 503 when no source is configured on
// the server. The source connection is server-config-only — the browser never
// sends a DSN or credential. When unset, the routes are not registered.
func WithPeerTubeImportService(svc peerTubeImportProvider) Option {
	return func(s *Server) { s.peertubeimportsvc = svc }
}

// WithIPFSMirrorService wires the hybrid IPFS media-mirror service (fix_plan
// P19). It backs the real GET /api/v1/ipfs/status payload and the unlisted-toggle
// re-evaluation. The routes are always mounted (stable contract); when this is
// unset but IPFS_ENABLED, status answers an honest 501. When unset, the re-eval
// hook is skipped.
func WithIPFSMirrorService(svc ipfsMirrorProvider) Option {
	return func(s *Server) { s.ipfsmirrorsvc = svc }
}

// WithMetrics attaches the Prometheus RED-metrics registry. When set AND
// cfg.MetricsEnabled is true, the request choke point records per-request
// counters/histograms and GET /metrics serves the scrape. Mirrors the otelecho
// gating: off by default, zero cost. When nil, no metrics are recorded and the
// scrape route is absent.
func WithMetrics(m *observability.Metrics) Option {
	return func(s *Server) { s.metrics = m }
}

// WithUploadService mounts the resumable/chunked upload endpoints (open session,
// PUT chunk, GET progress, complete, cancel). Completion runs the assembled file
// through the same video pipeline as a direct upload, so the routes register
// only when the video service is also present. When unset, the routes are absent
// (direct multipart upload still works).
func WithUploadService(svc *upload.Service) Option {
	return func(s *Server) { s.uploadsvc = svc }
}

// WithVideoImportService mounts the asynchronous URL-import endpoints (enqueue +
// status). The enqueue authorises the video via the video service, so the routes
// register only when the video service is also present. When unset, the import
// routes are not registered.
func WithVideoImportService(svc *videoimport.Service) Option {
	return func(s *Server) { s.importsvc = svc }
}

// WithChannelSyncService mounts the channel auto-sync endpoints (W2.C4): create /
// list / delete / sync-now. The service's Enabled() flag (CHANNEL_SYNC_ENABLED +
// yt-dlp import) gates the create/sync-now handlers (503 when off); the routes
// themselves are always registered when the service is present so the contract is
// stable. When unset, the routes are not registered.
func WithChannelSyncService(svc *channelsync.Service) Option {
	return func(s *Server) { s.channelsyncsvc = svc }
}

// WithCaptionJobService mounts the auto-caption (Whisper) endpoints (request +
// status). The routes are always registered when the service is present — even
// when auto-captioning is disabled, in which case the request endpoint answers
// 503 — so the documented contract surface stays stable. The enqueue authorises
// the video via the video service.
func WithCaptionJobService(svc *captionjob.Service) Option {
	return func(s *Server) { s.captionjobsvc = svc }
}

// WithFederationService wires the ActivityPub federation service. The AP root
// routes (NodeInfo/WebFinger/actors/inboxes/collections) are mounted only when
// this is set AND cfg.FederationEnabled is true — so they are absent by default
// and never appear in the REST OpenAPI contract. The REST remote-follow routes
// (/api/v1/me/remote-follows, remote-content §3) mount whenever the service is
// wired and ARE part of the OpenAPI contract. See .ralph/specs/federation.md.
func WithFederationService(svc *federation.Service) Option {
	return func(s *Server) { s.fedsvc = svc }
}

// WithATProtoService mounts the ATProto / Bluesky link/status/unlink endpoints
// (/api/v1/me/atproto, P10.2 — a Vidra extension). The routes ARE part of the
// REST OpenAPI contract and mount whenever the service is wired; the service's
// own Enabled() flag (ATPROTO_ENABLED) gates them at request time (503 when off),
// so the documented surface is stable regardless of the toggle. See
// .ralph/specs/atproto.md.
func WithATProtoService(svc *atproto.Service) Option {
	return func(s *Server) { s.atprotosvc = svc }
}

// WithRemoteVideoService mounts the remote-video read endpoints (metadata +
// cached thumbnail of federated videos ingested by the inbox). These are REST
// contract surface (documented in api/openapi.yaml), unlike the AP root routes.
// When unset, the routes are not registered.
func WithRemoteVideoService(svc *remotevideo.Service) Option {
	return func(s *Server) { s.remotevideosvc = svc }
}

// WithInstanceModerationService mounts instance-level moderation: the caller's
// per-user instance mutes and the admin instance blocklist. When unset, the
// routes are not registered.
func WithInstanceModerationService(svc *instancemod.Service) Option {
	return func(s *Server) { s.instancemodsvc = svc }
}

// WithSettingsService wires the DB-backed instance-settings overlay (fix_plan
// P10). When set, GET /api/v1/instance and the registration/upload/import/
// live-create/comment-create gates consult the effective value (DB override, else
// config default) instead of the static config, and the admin GET/PATCH
// /api/v1/admin/instance-settings routes are registered. When unset, those gates
// fall back to the static config and the admin routes are not mounted.
func WithSettingsService(svc *instancesettings.Service) Option {
	return func(s *Server) { s.settingssvc = svc }
}

// WithAuditLog wires the durable audit-log service. When set, s.audit persists
// each security-audit event (best-effort) in addition to logging it, and the
// GET /api/v1/admin/audit-log endpoint is registered. When unset, audit events
// are slog-only and the endpoint is not mounted.
func WithAuditLog(svc *audit.Service) Option {
	return func(s *Server) { s.auditLog = svc }
}

// WithMediaStorage gives the server the blob backend used to stream stored media
// (the original-file endpoint). It should be the same backend the video service
// writes uploads to. When unset, the streaming route serves 503.
func WithMediaStorage(b storage.Backend) Option {
	return func(s *Server) { s.media = b }
}

// WithLogger overrides the structured logger (default: slog.Default()). Used to
// route request/error/audit logs to a specific destination — and by tests to
// capture audit events.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithDevMailCapture wires the DEVELOPMENT-ONLY mail-capture reader. When set,
// GET /api/v1/dev/email-token exposes the most recent captured account-security
// token for an email so end-to-end tests can complete the reset/verify flows.
// The route is registered ONLY when this option is provided (i.e. when
// DEV_MAIL_CAPTURE_ENABLED is on), so production never carries it. It is
// intentionally absent from api/openapi.yaml — a test seam, not a public
// contract surface — so the OpenAPI drift test does not mount it.
func WithDevMailCapture(c *auth.CaptureMailer) Option {
	return func(s *Server) { s.devMailCapture = c }
}

// New constructs the HTTP server with middleware and routes registered. db and
// rdb may be nil (e.g. in unit tests); readiness reports them as unconfigured.
// It uses the process-wide slog default logger for request and error logging.
func New(cfg *config.Config, db, rdb Pinger, opts ...Option) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{echo: e, cfg: cfg, db: db, rdb: rdb, startedAt: time.Now(), logger: slog.Default()}
	// Playback-token signer for password-protected videos (CORE-17). Its key is
	// derived from the JWT secret via domain separation, so a playback token is
	// cryptographically independent of an account access token.
	s.playbackSigner = playback.NewSigner([]byte(cfg.JWTSecret))
	for _, opt := range opts {
		opt(s)
	}
	// The unlock endpoint is a password-guessing surface, so it is ALWAYS
	// rate-limited: a configured auth limiter (shared with login) takes
	// precedence, otherwise a built-in in-memory default engages so the endpoint
	// is never unthrottled on a deployment without Redis (W1 audit).
	if s.authLimit != nil {
		s.unlockLimit = s.authLimit
	} else {
		s.unlockLimit = ratelimit.NewLimiter(ratelimit.NewMemoryCounter(), defaultUnlockRateLimit, defaultUnlockRateWindow)
	}
	e.HTTPErrorHandler = s.httpErrorHandler

	e.Use(middleware.Recover())
	// Security response headers on every response (incl. recovered 5xx). HSTS is
	// added only in production (meaningless/unwanted on plain-HTTP localhost).
	e.Use(secureHeaders(cfg.Environment == "production"))
	e.Use(middleware.RequestID())
	// correlationID runs after RequestID (to mint from it) and before the request
	// logger (so `correlation_id` is present on the emitted line).
	e.Use(correlationID())
	// OpenTelemetry HTTP instrumentation (only when enabled — zero cost off). It
	// runs before the request logger so the active span's trace_id/span_id are in
	// context when the line is emitted, and it accepts inbound W3C traceparent.
	if cfg.OTelEnabled {
		e.Use(otelecho.Middleware(cfg.OTelServiceName))
	}
	e.Use(s.requestLogger())
	// Cookie-mode auth sessions need credentialed CORS so the browser attaches
	// the vidra_refresh cookie cross-origin. Allow-Credentials is only ever
	// granted to the explicit allow-list — never combined with a wildcard
	// origin (echoing "*" with credentials is unsafe; the browser rejects it).
	corsAllowCredentials := true
	for _, o := range cfg.CORSAllowedOrigins {
		if o == "*" {
			corsAllowCredentials = false
		}
	}
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     cfg.CORSAllowedOrigins,
		AllowMethods:     []string{echo.GET, echo.POST, echo.PUT, echo.PATCH, echo.DELETE, echo.OPTIONS},
		AllowCredentials: corsAllowCredentials,
	}))
	e.Use(requestDeadline(cfg.HTTPRequestTimeout))
	// The default body limit keeps the JSON API small. The original-file upload
	// route is exempted here and gets its own (larger) UploadMaxSize limit at
	// registration, so media uploads have headroom without widening the rest.
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		Skipper: func(c echo.Context) bool {
			r := c.Request()
			if r.Method == http.MethodPost && c.Path() == uploadRoutePath {
				return true
			}
			// DM attachment uploads carry a bounded (100 MiB) media body.
			if r.Method == http.MethodPost && c.Path() == attachmentUploadRoutePath {
				return true
			}
			// Resumable chunk PUTs carry raw media bytes (up to the chunk size);
			// the upload service bounds each chunk itself.
			return r.Method == http.MethodPut && c.Path() == uploadChunkRoutePath
		},
		Limit: cfg.HTTPBodyLimit,
	}))

	s.routes()
	return s
}

// requestLogger emits one structured slog line per request via Echo's
// RequestLogger middleware. Level escalates with status class so 5xx responses
// surface as errors. Request bodies and headers are never logged.
func (s *Server) requestLogger() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:    true,
		LogMethod:    true,
		LogURI:       true,
		LogLatency:   true,
		LogRequestID: true,
		LogError:     true,
		HandleError:  true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case v.Status >= 500:
				level = slog.LevelError
			case v.Status >= 400:
				level = slog.LevelWarn
			}
			attrs := []any{
				"method", v.Method,
				// Redact any ?pt= playback token (CORE-17) so the secret unlock token
				// never reaches the request log.
				"uri", redactQueryToken(v.URI),
				"status", v.Status,
				"latency_ms", v.Latency.Milliseconds(),
				"request_id", v.RequestID,
			}
			if cid := correlationIDFromContext(c.Request().Context()); cid != "" {
				attrs = append(attrs, "correlation_id", cid)
			}
			// When a trace is active (OTel enabled), correlate the log to it.
			if sc := trace.SpanContextFromContext(c.Request().Context()); sc.HasTraceID() {
				attrs = append(attrs, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
			}
			if v.Error != nil {
				attrs = append(attrs, "error", v.Error)
			}
			s.logger.Log(c.Request().Context(), level, "request", attrs...)
			// RED metrics ride this same choke point (gated by METRICS_ENABLED, like
			// otelecho is gated by OTEL_ENABLED): Echo's RequestLogger runs the error
			// handler first (HandleError:true), so v.Status is the FINAL status here.
			// c.Path() is the bounded route TEMPLATE — never the raw URL — so label
			// cardinality stays small.
			if s.metrics != nil {
				s.metrics.ObserveRequest(v.Method, c.Path(), v.Status, v.Latency)
			}
			return nil
		},
	})
}

// requestDeadline attaches a timeout to each request's context so handlers and
// the DB/Redis/outbound calls they make observe a deadline and abort cleanly.
// It does not forcibly interrupt a handler that ignores its context — the
// server's WriteTimeout is the hard backstop for that. Handlers that honour the
// context should return ctx.Err() (or a wrapped error), which the central error
// handler renders as a 503 envelope.
func requestDeadline(d time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx, cancel := context.WithTimeout(c.Request().Context(), d)
			defer cancel()
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

func (s *Server) routes() {
	s.echo.GET("/healthz", s.handleLive)
	s.echo.GET("/readyz", s.handleReady)
	s.echo.GET("/version", s.handleVersion)

	// Prometheus RED-metrics scrape (request rate/errors/duration + queue-depth
	// gauge), gated behind METRICS_ENABLED (s.metrics is nil otherwise). This is
	// an OPERATIONS surface intentionally EXCLUDED from the OpenAPI REST contract —
	// same treatment as the federation/AP root routes and the dev-only routes.
	// It lives at the root (not under /api/v1), so it is unthrottled like the
	// health probes; operators should network-scope it, not expose it publicly.
	// Documented in README.md and .ralph/specs/observability.md.
	if s.metrics != nil {
		s.echo.GET("/metrics", echo.WrapHandler(s.metrics.Handler()))
	}

	// Fediverse discovery (NodeInfo) lives at the root, outside /api/v1, and is a
	// federation contract — not part of the REST OpenAPI. Mounted only when
	// federation is enabled and wired, so the REST drift guard never sees it and
	// it 404s by default. See .ralph/specs/federation.md §4-5.
	if s.cfg.FederationEnabled && s.fedsvc != nil {
		s.echo.GET("/.well-known/nodeinfo", s.handleNodeInfoDiscovery)
		s.echo.GET("/nodeinfo/2.1", s.handleNodeInfo21)
		s.echo.GET("/.well-known/webfinger", s.handleWebFinger)
		s.echo.GET("/accounts/:handle", s.handleAccountActor)
		s.echo.GET("/video-channels/:handle", s.handleChannelActor)
		// Inbound activities. The shared inbox plus per-actor inboxes advertised in
		// the actor documents; all share one signature-verifying handler.
		s.echo.POST("/inbox", s.handleInbox)
		s.echo.POST("/accounts/:handle/inbox", s.handleInbox)
		s.echo.POST("/video-channels/:handle/inbox", s.handleInbox)
		// Actor collections advertised in the actor documents (read-only). Followers
		// and following are summaries; the channel outbox is paged (?page=N).
		for _, kind := range []string{"followers", "following"} {
			s.echo.GET("/video-channels/:handle/"+kind, s.channelCollection(kind))
			s.echo.GET("/accounts/:handle/"+kind, s.accountCollection(kind))
		}
		s.echo.GET("/video-channels/:handle/outbox", s.channelOutbox)
		s.echo.GET("/accounts/:handle/outbox", s.accountCollection("outbox"))
	}

	api := s.echo.Group("/api/v1")
	// Rate limiting guards the API surface only; liveness/readiness/version are
	// exempt so orchestrator probes are never throttled.
	if s.limiter != nil {
		api.Use(s.rateLimit(s.limiter))
	}
	api.GET("/nodeinfo", s.handleNodeInfo)
	api.GET("/instance", s.handleInstance)
	// Public platform information (spec instance-platform-info): the about-page
	// markdown document and the operator contact form. The contact form gets its
	// own hard per-IP budget when wired (1/hour in production).
	api.GET("/instance/about", s.handleInstanceAbout)
	var contactMW []echo.MiddlewareFunc
	if s.contactLimit != nil {
		contactMW = append(contactMW, s.contactRateLimit(s.contactLimit))
	}
	api.POST("/instance/contact", s.handleInstanceContact, contactMW...)

	if s.authsvc != nil {
		authGroup := api.Group("/auth")
		// A stricter limiter throttles credential stuffing / token guessing on the
		// sensitive endpoints, layered over the general per-IP limiter. Optional:
		// when unset (e.g. unit tests), these behave like any other route.
		var authMW []echo.MiddlewareFunc
		if s.authLimit != nil {
			authMW = append(authMW, s.authRateLimit(s.authLimit))
		}
		authGroup.POST("/register", s.handleRegister, authMW...)
		authGroup.POST("/login", s.handleLogin, authMW...)
		authGroup.POST("/refresh", s.handleRefresh)
		authGroup.POST("/logout", s.handleLogout)
		authGroup.POST("/password-reset", s.handleRequestPasswordReset, authMW...)
		authGroup.POST("/password-reset/confirm", s.handleConfirmPasswordReset, authMW...)
		authGroup.POST("/verify-email", s.handleRequestEmailVerification, s.requireAuth)
		authGroup.POST("/verify-email/confirm", s.handleConfirmEmailVerification, authMW...)
		authGroup.GET("/me", s.handleMe, s.requireAuth)
		authGroup.PATCH("/me", s.handleUpdateMe, s.requireAuth)
		authGroup.POST("/me/deactivate", s.handleDeactivateAccount, s.requireAuth)
		authGroup.POST("/logout-all", s.handleLogoutAll, s.requireAuth)

		// TOTP two-factor authentication (P4). Enrollment/status/disable act on
		// the authenticated account; the challenge is the second half of an MFA
		// login — public, and behind the strict auth limiter like login itself
		// (it is a code-guessing surface).
		authGroup.GET("/mfa", s.handleGetMFAStatus, s.requireAuth)
		authGroup.POST("/mfa/totp", s.handleBeginTOTPEnrollment, s.requireAuth)
		authGroup.POST("/mfa/totp/verify", s.handleVerifyTOTPEnrollment, s.requireAuth)
		authGroup.DELETE("/mfa/totp", s.handleDisableTOTP, s.requireAuth)
		authGroup.POST("/mfa/challenge", s.handleMFAChallenge, authMW...)

		// OIDC login/link (P4/P15). Browser-navigation flow: begin 302s to the
		// provider, the callback issues a cookie-mode session. The auth limiter
		// throttles both (they are unauthenticated credential endpoints).
		if s.oauthsvc != nil {
			authGroup.GET("/oauth/:provider", s.handleOAuthBegin, authMW...)
			authGroup.GET("/oauth/:provider/callback", s.handleOAuthCallback, authMW...)
			api.GET("/me/oauth-identities", s.handleListOAuthIdentities, s.requireAuth)
			api.DELETE("/me/oauth-identities/:provider", s.handleUnlinkOAuthIdentity, s.requireAuth)
		}

		// Registration approval queue (admin-only). Present whenever auth is wired;
		// only meaningful when REGISTRATION_REQUIRE_APPROVAL is on.
		api.GET("/admin/registration-requests", s.handleListRegistrationRequests, s.requireAuth, s.requireRole("admin"))
		api.POST("/admin/registration-requests/:id/approve", s.handleApproveRegistration, s.requireAuth, s.requireRole("admin"))
		api.POST("/admin/registration-requests/:id/reject", s.handleRejectRegistration, s.requireAuth, s.requireRole("admin"))
	}

	// Account lifecycle (P4 export/import + §1 hard delete). The self-delete
	// re-confirms the password via the auth service; the admin variant is
	// admin-only and self-guarded. Export is a durable job: request → status →
	// download (archives expire after 7 days).
	if s.accountsvc != nil && s.authsvc != nil {
		api.DELETE("/auth/me", s.handleDeleteAccount, s.requireAuth)
		api.DELETE("/admin/users/:id", s.handleAdminDeleteUser, s.requireAuth, s.requireRole("admin"))
		api.POST("/me/export", s.handleRequestAccountExport, s.requireAuth)
		api.GET("/me/export", s.handleGetAccountExport, s.requireAuth)
		api.GET("/me/export/download", s.handleDownloadAccountExport, s.requireAuth)
		api.POST("/me/import", s.handleImportAccount, s.requireAuth)
	}

	// DEV-ONLY: expose captured account-security tokens so e2e tests can complete
	// the reset/verify flows. Registered only when DEV_MAIL_CAPTURE_ENABLED wired
	// the capture mailer (never in production). Intentionally not in openapi.yaml.
	if s.devMailCapture != nil {
		api.GET("/dev/email-token", s.handleDevEmailToken)
	}

	if s.channelsvc != nil {
		api.POST("/channels", s.handleCreateChannel, s.requireAuth)
		api.GET("/channels/:handle", s.handleGetChannel)
		api.PATCH("/channels/:handle", s.handleUpdateChannel, s.requireAuth)
		api.DELETE("/channels/:handle", s.handleDeleteChannel, s.requireAuth)
		api.POST("/channels/:handle/follow", s.handleFollowChannel, s.requireAuth)
		api.DELETE("/channels/:handle/follow", s.handleUnfollowChannel, s.requireAuth)
		api.GET("/me/channels", s.handleListMyChannels, s.requireAuth)
		api.GET("/me/subscriptions", s.handleListFollowedChannels, s.requireAuth)
	}

	// Simple crypto donation addresses (P14): the owner manages their own
	// (account-level or channel-scoped) addresses and optionally proves control
	// via a signed challenge; anyone may read a user's or channel's addresses.
	// NON-CUSTODIAL and display-only — Vidra never holds funds or processes
	// payments. The public channel read needs the channel service (handle → id).
	if s.donationsvc != nil {
		api.POST("/me/donation-addresses", s.handleAddDonationAddress, s.requireAuth)
		api.GET("/me/donation-addresses", s.handleListMyDonationAddresses, s.requireAuth)
		api.DELETE("/me/donation-addresses/:id", s.handleDeleteDonationAddress, s.requireAuth)
		api.POST("/me/donation-addresses/:id/challenge", s.handleChallengeDonationAddress, s.requireAuth)
		api.POST("/me/donation-addresses/:id/verify", s.handleVerifyDonationAddress, s.requireAuth)
		api.GET("/users/:id/donation-addresses", s.handleListUserDonationAddresses)
		if s.channelsvc != nil {
			api.GET("/channels/:handle/donation-addresses", s.handleListChannelDonationAddresses)
		}
	}

	// Avatars/banners: the caller manages their own account images; a channel
	// owner manages the channel's (non-owner → 404); serving is public with the
	// content type derived at upload time. Uploads are bounded by the global
	// HTTP body limit (same cap as the custom video thumbnail).
	if s.imagesvc != nil {
		for _, kind := range []string{profileimage.KindAvatar, profileimage.KindBanner} {
			api.POST("/me/"+kind, s.handleSetMyImage(kind), s.requireAuth)
			api.DELETE("/me/"+kind, s.handleDeleteMyImage(kind), s.requireAuth)
			api.GET("/users/:id/"+kind, s.handleGetUserImage(kind))
			if s.channelsvc != nil {
				api.POST("/channels/:handle/"+kind, s.handleSetChannelImage(kind), s.requireAuth)
				api.DELETE("/channels/:handle/"+kind, s.handleDeleteChannelImage(kind), s.requireAuth)
				api.GET("/channels/:handle/"+kind, s.handleGetChannelImage(kind))
			}
		}
	}

	// Video creation needs both the video and channel services (channel for
	// ownership); the public get applies optional auth so owners can see their
	// own private drafts.
	if s.videosvc != nil && s.channelsvc != nil {
		api.POST("/channels/:handle/videos", s.handleCreateVideo, s.requireAuth)
		api.GET("/channels/:handle/videos", s.handleListChannelVideos, s.optionalAuth)
		api.GET("/videos", s.handleListPublicVideos, s.optionalAuth)
		api.GET("/me/subscriptions/videos", s.handleListSubscriptionVideos, s.requireAuth)
		api.GET("/me/saved", s.handleListSavedVideos, s.requireAuth)
		api.POST("/videos/:id/save", s.handleSaveVideo, s.requireAuth)
		api.DELETE("/videos/:id/save", s.handleUnsaveVideo, s.requireAuth)
		api.GET("/me/history", s.handleListHistory, s.requireAuth)
		api.DELETE("/me/history", s.handleClearHistory, s.requireAuth)
		api.DELETE("/me/history/:id", s.handleDeleteHistoryEntry, s.requireAuth)
		api.GET("/videos/:id/watch-progress", s.handleGetWatchProgress, s.requireAuth)
		api.PUT("/videos/:id/watch-progress", s.handleRecordWatchProgress, s.requireAuth)
		api.GET("/videos/config", s.handleVideoConfig)
		api.GET("/videos/search", s.handleSearchVideos, s.optionalAuth)
		api.GET("/videos/:id", s.handleGetVideo, s.optionalAuth)
		api.GET("/videos/:id/original", s.handleStreamVideoOriginal, s.optionalAuth)
		api.GET("/videos/:id/download", s.handleGetVideoDownloads, s.optionalAuth)

		// HLS playback (transcoded ladder): the master playlist plus per-rendition
		// variant playlists/segments, same visibility as /original. Registered only
		// when the transcode read side is wired.
		if s.transcodesvc != nil {
			api.GET("/videos/:id/hls/master.m3u8", s.handleGetHLSMaster, s.optionalAuth)
			api.GET("/videos/:id/hls/:rendition/:file", s.handleGetHLSFile, s.optionalAuth)
		}
		api.GET("/videos/:id/thumbnail", s.handleGetVideoThumbnail, s.optionalAuth)
		api.POST("/videos/:id/thumbnail", s.handleSetVideoThumbnail, s.requireAuth)
		// Seek-preview storyboard (sprite sheet + WebVTT map), same visibility as
		// the detail endpoint. detail exposes has_storyboard.
		api.GET("/videos/:id/storyboard.jpg", s.handleGetVideoStoryboardImage, s.optionalAuth)
		api.GET("/videos/:id/storyboard.vtt", s.handleGetVideoStoryboardVTT, s.optionalAuth)
		// Seek-bar chapters (CORE-15): public read with the detail's visibility;
		// owner-only whole-set replace. detail exposes has_chapters.
		api.GET("/videos/:id/chapters", s.handleGetVideoChapters, s.optionalAuth)
		api.PUT("/videos/:id/chapters", s.handleSetVideoChapters, s.requireAuth)

		// Video passwords + unlock (CORE-17 / W1.C2). The unlock endpoint is a
		// password-guessing surface, so it is ALWAYS rate-limited (optional auth: an
		// anonymous viewer may unlock): the configured auth limiter when present
		// (shared with login), else the built-in in-memory default derived in New().
		// s.unlockLimit is always non-nil. The owner CRUD endpoints never return a
		// plaintext or hash.
		unlockMW := []echo.MiddlewareFunc{s.optionalAuth, s.authRateLimit(s.unlockLimit)}
		api.POST("/videos/:id/unlock", s.handleUnlockVideo, unlockMW...)
		api.GET("/videos/:id/passwords", s.handleListVideoPasswords, s.requireAuth)
		api.POST("/videos/:id/passwords", s.handleAddVideoPassword, s.requireAuth)
		api.PUT("/videos/:id/passwords", s.handleReplaceVideoPasswords, s.requireAuth)
		api.DELETE("/videos/:id/passwords/:passwordId", s.handleDeleteVideoPassword, s.requireAuth)

		// Embed privacy (CORE-17): read is optional-auth with the detail's BASE
		// visibility (no password gate, so the embed page can read the policy
		// pre-unlock); write is owner-only.
		api.GET("/videos/:id/embed-privacy", s.handleGetVideoEmbedPrivacy, s.optionalAuth)
		api.PUT("/videos/:id/embed-privacy", s.handleSetVideoEmbedPrivacy, s.requireAuth)
		// Progressive VP9/WebM alternate (TRANSCODING_VP9_ENABLED); 404 when absent.
		api.GET("/videos/:id/webm", s.handleStreamVideoWebM, s.optionalAuth)
		api.POST("/videos/:id/view", s.handleRecordVideoView, s.optionalAuth)
		// Creator statistics (owner-only; non-owners get 404).
		api.GET("/videos/:id/stats", s.handleGetVideoStats, s.requireAuth)
		api.GET("/channels/:handle/stats", s.handleGetChannelStats, s.requireAuth)
		api.PATCH("/videos/:id", s.handleUpdateVideo, s.requireAuth)
		api.DELETE("/videos/:id", s.handleDeleteVideo, s.requireAuth)
		api.POST("/videos/:id/file", s.handleUploadVideoFile, s.requireAuth, middleware.BodyLimit(s.cfg.UploadMaxSize))

		// Resumable/chunked upload (P6.1): open a session, PUT fixed-size chunks
		// (each bounded by the upload service, hence exempt from the JSON body
		// limit), GET progress, complete (→ the same AttachOriginal → Process
		// pipeline as a direct upload), or cancel.
		if s.uploadsvc != nil {
			api.POST("/videos/:id/upload-session", s.handleCreateUploadSession, s.requireAuth)
			api.GET("/me/uploads", s.handleListMyUploads, s.requireAuth)
			api.PUT("/uploads/:upload_id/chunks/:n", s.handlePutUploadChunk, s.requireAuth)
			api.GET("/uploads/:upload_id", s.handleGetUploadSession, s.requireAuth)
			api.POST("/uploads/:upload_id/complete", s.handleCompleteUploadSession, s.requireAuth)
			api.DELETE("/uploads/:upload_id", s.handleCancelUploadSession, s.requireAuth)
		}

		// Asynchronous URL import (P2.2): enqueue → 202, then poll the job status.
		if s.importsvc != nil {
			api.POST("/videos/:id/import", s.handleImportVideoFile, s.requireAuth)
			api.GET("/videos/:id/import", s.handleGetVideoImport, s.requireAuth)
		}

		// Auto-caption / Whisper (P13): request an auto-generated caption track
		// (202, or 503 when auto-captioning is disabled) and poll its status.
		if s.captionjobsvc != nil {
			api.POST("/videos/:id/captions/auto", s.handleRequestAutoCaption, s.requireAuth)
			api.GET("/videos/:id/captions/auto", s.handleGetAutoCaption, s.requireAuth)
		}

		// Captions: the owner uploads/removes WebVTT tracks (any state); anyone
		// lists/downloads them on a public, published video.
		api.GET("/videos/:id/captions", s.handleListCaptions, s.optionalAuth)
		api.GET("/videos/:id/captions/:lang", s.handleDownloadCaption, s.optionalAuth)
		api.POST("/videos/:id/captions", s.handleUploadCaption, s.requireAuth)
		api.DELETE("/videos/:id/captions/:lang", s.handleDeleteCaption, s.requireAuth)

		// Comments are scoped to a (public, published) video.
		if s.commentsvc != nil {
			api.GET("/videos/:id/comments", s.handleListComments, s.optionalAuth)
			api.POST("/videos/:id/comments", s.handleCreateComment, s.requireAuth)
			api.PATCH("/comments/:id", s.handleUpdateComment, s.requireAuth)
			api.DELETE("/comments/:id", s.handleDeleteComment, s.requireAuth)
			api.GET("/admin/comments", s.handleListAdminComments, s.requireAuth, s.requireRole("admin", "moderator"))
		}

		// Ratings (like/dislike) are scoped to a (public, published) video.
		if s.ratingsvc != nil {
			api.GET("/videos/:id/rating", s.handleGetVideoRating, s.optionalAuth)
			api.PUT("/videos/:id/rating", s.handlePutVideoRating, s.requireAuth)
			api.DELETE("/videos/:id/rating", s.handleDeleteVideoRating, s.requireAuth)
		}
	}

	// Channel auto-sync (W2.C4, UPLOAD-13): manage the bindings that mirror an
	// external channel's uploads into a local channel via `ytdlp` imports. Owner-
	// scoped and self-contained (no dependency on the video service), so it mounts
	// independently; the create/sync-now handlers 503 when the feature is disabled.
	if s.channelsyncsvc != nil {
		api.POST("/channel-syncs", s.handleCreateChannelSync, s.requireAuth)
		api.GET("/channel-syncs", s.handleListChannelSyncs, s.requireAuth)
		api.DELETE("/channel-syncs/:id", s.handleDeleteChannelSync, s.requireAuth)
		api.POST("/channel-syncs/:id/sync-now", s.handleSyncChannelNow, s.requireAuth)
	}

	// Storage quota: the caller's own usage + effective cap. The same service
	// gates the upload/import handlers above (422 quota_exceeded).
	if s.quotasvc != nil {
		api.GET("/me/quota", s.handleGetMyQuota, s.requireAuth)
	}

	// Notifications are the caller's own inbox; independent of the other feature
	// services (their rows are written as a side effect of the follow/comment flows).
	if s.notifsvc != nil {
		api.GET("/me/notifications", s.handleListNotifications, s.requireAuth)
		api.GET("/me/notifications/unread-count", s.handleUnreadNotificationCount, s.requireAuth)
		api.POST("/me/notifications/read-all", s.handleMarkAllNotificationsRead, s.requireAuth)
		api.POST("/me/notifications/:id/read", s.handleMarkNotificationRead, s.requireAuth)
		// Per-type delivery preferences (all types default enabled; the Notify*
		// side effects consult them at create time).
		api.GET("/me/notification-prefs", s.handleGetNotificationPrefs, s.requireAuth)
		api.PATCH("/me/notification-prefs", s.handleUpdateNotificationPrefs, s.requireAuth)
	}

	// Per-user player settings (PLAY-07): the signed-in user's playback defaults
	// (speed, autoplay-next, quality, captions/theater). GET always returns the
	// full effective object (defaults for a fresh user); PUT is a merge.
	if s.playersettingssvc != nil {
		api.GET("/me/player-settings", s.handleGetPlayerSettings, s.requireAuth)
		api.PUT("/me/player-settings", s.handleUpdatePlayerSettings, s.requireAuth)
	}

	// Named playlists. The public get applies optional auth so owners can see
	// their own private playlists.
	if s.playlistsvc != nil {
		api.POST("/playlists", s.handleCreatePlaylist, s.requireAuth)
		api.GET("/me/playlists", s.handleListMyPlaylists, s.requireAuth)
		api.GET("/playlists/:id", s.handleGetPlaylist, s.optionalAuth)
		api.PATCH("/playlists/:id", s.handleUpdatePlaylist, s.requireAuth)
		api.DELETE("/playlists/:id", s.handleDeletePlaylist, s.requireAuth)
		api.POST("/playlists/:id/videos", s.handleAddPlaylistItem, s.requireAuth)
		api.PUT("/playlists/:id/videos", s.handleReorderPlaylistItems, s.requireAuth)
		api.DELETE("/playlists/:id/videos/:videoId", s.handleRemovePlaylistItem, s.requireAuth)
		// Playlist cover image: owner upload/remove + public (visibility-gated) get.
		api.GET("/playlists/:id/thumbnail", s.handleGetPlaylistThumbnail, s.optionalAuth)
		api.POST("/playlists/:id/thumbnail", s.handleSetPlaylistThumbnail, s.requireAuth)
		api.DELETE("/playlists/:id/thumbnail", s.handleDeletePlaylistThumbnail, s.requireAuth)
	}

	// Media garbage collection: admin-triggered sweep of orphaned storage blobs.
	if s.mediagcsvc != nil {
		api.POST("/admin/media/gc", s.handleAdminMediaGC, s.requireAuth, s.requireRole("admin"))
	}

	// Abuse reports: any authed user can file one; the queue + resolution are
	// restricted to moderators/admins. Reporting a video needs the video service.
	if s.moderationsvc != nil && s.videosvc != nil {
		api.POST("/videos/:id/report", s.handleReportVideo, s.requireAuth)
		api.POST("/comments/:id/report", s.handleReportComment, s.requireAuth)
		api.POST("/users/:id/report", s.handleReportAccount, s.requireAuth)
		api.GET("/admin/reports", s.handleListReports, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/reports/:id/resolve", s.handleResolveReport, s.requireAuth, s.requireRole("admin", "moderator"))
		// Hard-delete is admin-only: moderators resolve, admins can purge.
		api.DELETE("/admin/reports/:id", s.handleDeleteReport, s.requireAuth, s.requireRole("admin"))
		api.GET("/admin/videos", s.handleListAdminVideos, s.requireAuth, s.requireRole("admin", "moderator"))
		api.GET("/admin/videos/blocked", s.handleListBlockedVideos, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/videos/:id/block", s.handleBlockVideo, s.requireAuth, s.requireRole("admin", "moderator"))
		api.DELETE("/admin/videos/:id/block", s.handleUnblockVideo, s.requireAuth, s.requireRole("admin", "moderator"))
		// Upload quarantine review (§11): the queue plus approve (→ published,
		// hooks fire) / reject (→ failed, owner notified).
		api.GET("/admin/videos/quarantined", s.handleListQuarantinedVideos, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/videos/:id/approve", s.handleApproveQuarantinedVideo, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/videos/:id/reject", s.handleRejectQuarantinedVideo, s.requireAuth, s.requireRole("admin", "moderator"))
	}

	// Account mutes: a signed-in user mutes/unmutes another account and lists
	// the accounts they have muted.
	if s.mutesvc != nil {
		api.GET("/me/mutes/accounts", s.handleListMutedAccounts, s.requireAuth)
		api.POST("/me/mutes/accounts/:id", s.handleMuteAccount, s.requireAuth)
		api.DELETE("/me/mutes/accounts/:id", s.handleUnmuteAccount, s.requireAuth)
	}

	// Instance-level moderation (remote-content §8): per-user instance mutes plus
	// the admin instance blocklist (drops inbound activities, hides remote
	// content, cancels outbound deliveries).
	if s.instancemodsvc != nil {
		api.GET("/me/mutes/instances", s.handleListMutedInstances, s.requireAuth)
		api.POST("/me/mutes/instances/:domain", s.handleMuteInstance, s.requireAuth)
		api.DELETE("/me/mutes/instances/:domain", s.handleUnmuteInstance, s.requireAuth)
		api.GET("/admin/instances/blocked", s.handleListBlockedInstances, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/instances/blocked", s.handleBlockInstance, s.requireAuth, s.requireRole("admin", "moderator"))
		api.DELETE("/admin/instances/blocked/:domain", s.handleUnblockInstance, s.requireAuth, s.requireRole("admin", "moderator"))
	}

	// Remote videos (federated, metadata-only): the remote-watch surface + the
	// locally cached thumbnail. Public reads; content from blocked instances or
	// the per-video remote block-list is excluded at the query.
	if s.remotevideosvc != nil {
		api.GET("/remote-videos/:id", s.handleGetRemoteVideo)
		api.GET("/remote-videos/:id/thumbnail", s.handleGetRemoteVideoThumbnail)
	}

	// Remote-video moderation (remote-content §8): local reports of federated
	// remote videos plus the admin per-video hide, mirroring the video_blocks
	// endpoints. Audited.
	if s.moderationsvc != nil {
		api.POST("/remote-videos/:id/report", s.handleReportRemoteVideo, s.requireAuth)
		api.GET("/admin/remote-videos/blocked", s.handleListBlockedRemoteVideos, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/remote-videos/:id/block", s.handleBlockRemoteVideo, s.requireAuth, s.requireRole("admin", "moderator"))
		api.DELETE("/admin/remote-videos/:id/block", s.handleUnblockRemoteVideo, s.requireAuth, s.requireRole("admin", "moderator"))
	}

	// Outbound remote-channel follows (remote-content §3). REST contract
	// surface, mounted whenever the federation service is wired (like the
	// remote-video reads — unlike the AP root routes, which additionally need
	// FEDERATION_ENABLED); the POST itself refuses with 503 while federation
	// is disabled so no outbound fetches happen on a non-federating instance.
	if s.fedsvc != nil {
		api.POST("/me/remote-follows", s.handleCreateRemoteFollow, s.requireAuth)
		api.GET("/me/remote-follows", s.handleListRemoteFollows, s.requireAuth)
		api.DELETE("/me/remote-follows/:id", s.handleDeleteRemoteFollow, s.requireAuth)
	}

	// ATProto / Bluesky link (P10.2 extension): the caller links/inspects/unlinks
	// their Bluesky account for outbound auto cross-posting. Always mounted when
	// the service is wired (stable contract); the handlers answer 503 while
	// ATPROTO_ENABLED is off.
	if s.atprotosvc != nil {
		api.PUT("/me/atproto", s.handleLinkATProto, s.requireAuth)
		api.GET("/me/atproto", s.handleGetATProto, s.requireAuth)
		api.DELETE("/me/atproto", s.handleUnlinkATProto, s.requireAuth)
	}

	// Account blocks: a signed-in user blocks/unblocks another account (cutting
	// off direct messaging in both directions) and lists who they have blocked.
	if s.blocksvc != nil {
		api.GET("/me/blocks", s.handleListBlockedUsers, s.requireAuth)
		api.POST("/me/blocks/:id", s.handleBlockUser, s.requireAuth)
		api.DELETE("/me/blocks/:id", s.handleUnblockUser, s.requireAuth)
	}

	// Watched words: moderators/admins maintain the instance-wide watched-terms list.
	if s.watchwordsvc != nil {
		api.GET("/admin/watched-words", s.handleListWatchedWords, s.requireAuth, s.requireRole("admin", "moderator"))
		api.POST("/admin/watched-words", s.handleAddWatchedWord, s.requireAuth, s.requireRole("admin", "moderator"))
		api.DELETE("/admin/watched-words/:id", s.handleDeleteWatchedWord, s.requireAuth, s.requireRole("admin", "moderator"))
		api.GET("/admin/watched-word-matches", s.handleListWatchedWordMatches, s.requireAuth, s.requireRole("admin", "moderator"))
	}

	// Admin user management is admin-only (not moderators).
	if s.adminsvc != nil {
		api.GET("/admin/users", s.handleListUsers, s.requireAuth, s.requireRole("admin"))
		api.PATCH("/admin/users/:id", s.handleUpdateUser, s.requireAuth, s.requireRole("admin"))
		// Instance-wide overview counts for the admin dashboard cards. Read-only
		// aggregate; admin-only (this is the vidra-user admin-overview binding).
		api.GET("/admin/stats", s.handleAdminStats, s.requireAuth, s.requireRole("admin"))
	}

	// Durable audit trail (admin-only), when the audit-log service is wired.
	if s.auditLog != nil {
		api.GET("/admin/audit-log", s.handleListAuditLog, s.requireAuth, s.requireRole("admin"))
	}

	// Admin operational status. Depends only on core wiring; auth guards it.
	if s.authsvc != nil {
		api.GET("/admin/system", s.handleSystemStatus, s.requireAuth, s.requireRole("admin"))
	}

	// Admin operations: durable-queue depth snapshot + recent failures (P17.4).
	if s.jobStatusSvc != nil {
		api.GET("/admin/jobs", s.handleListJobs, s.requireAuth, s.requireRole("admin"))
	}

	// DB-backed instance settings overlay (fix_plan P10): admins read the
	// effective values + overrides and PATCH the mutable subset. Admin-only.
	if s.settingssvc != nil {
		api.GET("/admin/instance-settings", s.handleGetInstanceSettings, s.requireAuth, s.requireRole("admin"))
		api.PATCH("/admin/instance-settings", s.handleUpdateInstanceSettings, s.requireAuth, s.requireRole("admin"))
	}

	// PeerTube import / migration (fix_plan P18). Admin-only: launch a
	// dry-run/run, list runs, poll one run. The source connection is taken from
	// SERVER CONFIG only — the browser never sends a DSN or credential. Always
	// mounted when the service is wired (stable contract); the launch answers 503
	// when no source is configured. This is the vidra-user admin import UI contract.
	if s.peertubeimportsvc != nil {
		api.POST("/admin/peertube-import", s.handleLaunchPeerTubeImport, s.requireAuth, s.requireRole("admin"))
		api.GET("/admin/peertube-import", s.handleListPeerTubeImports, s.requireAuth, s.requireRole("admin"))
		api.GET("/admin/peertube-import/:id", s.handleGetPeerTubeImport, s.requireAuth, s.requireRole("admin"))
	}

	// Hybrid IPFS media mirroring (fix_plan P19). Admin-only, always mounted
	// (stable contract): status + kick a reconcile. Both answer 503 ipfs_disabled
	// when IPFS_ENABLED=false; the real payloads land in P19.2 (status) / P19.6
	// (reconcile). Config-gated inside the handler, so no service wiring is needed.
	api.GET("/ipfs/status", s.handleIPFSStatus, s.requireAuth, s.requireRole("admin"))
	api.POST("/admin/ipfs/reconcile", s.handleIPFSReconcile, s.requireAuth, s.requireRole("admin"))

	// Direct messaging (1:1 conversations + messages). All behind requireAuth;
	// non-participants get 404 so a conversation's existence is not leaked.
	if s.messagingsvc != nil {
		api.POST("/conversations", s.handleStartConversation, s.requireAuth)
		api.GET("/me/conversations", s.handleListConversations, s.requireAuth)
		api.GET("/conversations/:id/messages", s.handleListMessages, s.requireAuth)
		api.POST("/conversations/:id/messages", s.handleSendMessage, s.requireAuth)
		// DM completeness (product-decisions.md §14): read receipts, per-message
		// delete/report, and the read-receipts privacy toggle.
		api.POST("/conversations/:id/read", s.handleMarkConversationRead, s.requireAuth)
		api.DELETE("/messages/:id", s.handleDeleteMessage, s.requireAuth)
		api.POST("/messages/:id/report", s.handleReportMessage, s.requireAuth)
		api.GET("/me/messaging-prefs", s.handleGetMessagingPrefs, s.requireAuth)
		api.PATCH("/me/messaging-prefs", s.handleUpdateMessagingPrefs, s.requireAuth)
		// Attachments: the upload route carries a bounded (100 MiB) media body
		// (exempted from the JSON body limit above) and is per-user rate limited
		// (the no-quota compensating control, messaging-v2.md D6; a no-op when the
		// attachment limiter is unset). Both endpoints return 503 when blob
		// storage is not configured.
		api.POST("/conversations/:id/attachments", s.handleUploadAttachment, s.requireAuth, s.attachmentUploadRateLimit())
		api.GET("/attachments/:id", s.handleDownloadAttachment, s.requireAuth)
	}

	// E2EE key directory (ciphertext-only encrypted messaging, P11.2): own
	// device registry + one-time prekey upload/count, and the participant-gated
	// peer endpoints (device listing, atomic key claim). The encrypted
	// conversation/envelope surface itself rides the /conversations routes
	// above, branched by the conversation's immutable encrypted flag.
	if s.e2eesvc != nil {
		api.POST("/e2ee/devices", s.handleRegisterE2EEDevice, s.requireAuth)
		api.GET("/e2ee/devices", s.handleListMyE2EEDevices, s.requireAuth)
		api.DELETE("/e2ee/devices/:id", s.handleDeleteE2EEDevice, s.requireAuth)
		api.POST("/e2ee/devices/:id/one-time-keys", s.handleUploadOneTimeKeys, s.requireAuth)
		api.GET("/e2ee/devices/:id/one-time-keys/count", s.handleCountOneTimeKeys, s.requireAuth)
		api.GET("/users/:id/e2ee/devices", s.handleListUserE2EEDevices, s.requireAuth)
		api.POST("/users/:id/e2ee/claim", s.handleClaimOneTimeKeys, s.requireAuth)
	}

	// Live streams: a channel owner manages live streams + their stream keys.
	// Reading one live stream's metadata is public (privacy-gated); RTMP ingestion
	// / HLS output is a later integration boundary.
	if s.livesvc != nil {
		api.POST("/channels/:handle/live", s.handleCreateLiveStream, s.requireAuth)
		api.GET("/channels/:handle/live", s.handleListLiveStreams, s.requireAuth)
		// Public "Live now" listing (home rail): currently-live PUBLIC streams
		// across all channels. Public read; unlisted/private never listed.
		api.GET("/live", s.handleListLivePublicStreams, s.optionalAuth)
		api.GET("/live/:id", s.handleGetLiveStream, s.optionalAuth)
		api.PATCH("/live/:id", s.handleUpdateLiveStream, s.requireAuth)
		api.POST("/live/:id/key", s.handleRegenerateLiveStreamKey, s.requireAuth)
		api.DELETE("/live/:id", s.handleDeleteLiveStream, s.requireAuth)
		// Live HLS serving: the media server writes segments into LIVE_HLS_ROOT
		// keyed by stream ID; the api serves them privacy/state-gated (404 when
		// LIVE_HLS_ROOT is unset). Mirrors the VOD /hls routes.
		api.GET("/live/:id/hls/master.m3u8", s.handleGetLiveHLSMaster, s.optionalAuth)
		api.GET("/live/:id/hls/:file", s.handleGetLiveHLSFile, s.optionalAuth)
		// RTMP ingest boundary (media-server-facing): authenticated by the ingest
		// shared secret, not a user token. 404 unless LIVE_INGEST_SECRET is set.
		api.POST("/live/ingest/start", s.handleLiveIngestStart)
		api.POST("/live/ingest/stop", s.handleLiveIngestStop)
	}
}

// Handler exposes the underlying http.Handler for tests.
func (s *Server) Handler() *echo.Echo { return s.echo }

// Start begins listening on the configured address. It blocks until the server
// is shut down.
func (s *Server) Start() error {
	return s.echo.Start(s.cfg.HTTPAddr())
}

// Shutdown gracefully drains in-flight requests, bounded by ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}
