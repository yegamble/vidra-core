// Package config loads and validates Vidra backend configuration from the
// environment. Configuration is the single source of truth for runtime wiring;
// no other package should read os.Getenv directly.
package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/gommon/bytes"
)

// Config holds all runtime configuration for the vidra-core API service.
type Config struct {
	// Environment is one of "development", "test", or "production".
	Environment string

	// Logging. LogLevel is the minimum emitted level (debug|info|warn|error,
	// default info); LogFormat selects the slog handler (json — default,
	// production — or text for readable local dev). Consumed by
	// internal/observability.NewLogger at startup.
	LogLevel  string
	LogFormat string

	// OpenTelemetry tracing. Opt-in and zero-cost when OTelEnabled is false. When
	// enabled, spans export to OTelExporterEndpoint over OTelExporterProtocol
	// (grpc | http/protobuf); OTelServiceName labels the service in traces.
	OTelEnabled          bool
	OTelExporterEndpoint string
	OTelExporterProtocol string
	OTelServiceName      string

	// MetricsEnabled gates the Prometheus RED-metrics surface (a /metrics scrape
	// endpoint + the request-metrics middleware). Opt-in and zero-cost when false:
	// no registry is built, no middleware is mounted, and /metrics is not routed.
	MetricsEnabled bool

	// HTTP server.
	HTTPHost            string
	HTTPPort            int
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPShutdownTimeout time.Duration
	// HTTPRequestTimeout bounds per-request handler work via a context deadline
	// (DB/Redis/outbound calls observe it). HTTPWriteTimeout is the hard backstop.
	HTTPRequestTimeout time.Duration
	// HTTPBodyLimit is the maximum accepted request body size, as an Echo size
	// string (e.g. "8M", "512K"). Oversized requests are rejected with 413.
	HTTPBodyLimit string

	// PostgreSQL connection (DSN form, e.g. postgres://user:pass@host:5432/db).
	DatabaseURL string

	// Redis connection (URL form, e.g. redis://host:6379/0).
	RedisURL string

	// CORSAllowedOrigins is the explicit allow-list for browser origins.
	CORSAllowedOrigins []string

	// InstanceName is the human-facing name of this Vidra instance.
	InstanceName string

	// PublicBaseURL is the canonical public origin of this instance (e.g.
	// https://videos.example), used to build federation actor/object ids and
	// URLs. Required when FederationEnabled; must carry no path or trailing
	// slash (a trailing slash is trimmed at load). See .ralph/specs/federation.md.
	PublicBaseURL string

	// FederationEnabled is the master switch for ActivityPub federation. When
	// false (default) all federation routes are unmounted (they 404) — zero cost
	// when off. See .ralph/specs/federation.md.
	FederationEnabled bool

	// ATProtoEnabled is the master switch for the ATProto / Bluesky extension
	// (P10.2, .ralph/specs/atproto.md) — v1 outbound cross-posting only. When
	// false (default) the /me/atproto link/status/unlink endpoints answer 503 and
	// no auto-post worker runs. Independent of FederationEnabled: an instance may
	// enable ActivityPub only, ATProto only, both, or neither.
	ATProtoEnabled bool

	// ATProtoKeyKEK is the base64 (standard) 32-byte key-encryption key used to
	// envelope-encrypt linked Bluesky app passwords at rest (same secretbox
	// envelope as FederationKeyKEK). Deployments already running a federation KEK
	// can share it: ATProtoKEK() falls back to FederationKeyKEK when this is unset.
	// Required in production when ATProtoEnabled; with neither set (dev) app
	// passwords are stored raw with a loud boot warning. NEVER commit a real value.
	ATProtoKeyKEK string

	// MalwareScanEnabled turns on ClamAV scanning of uploaded originals before
	// publish (fail-closed: infected or unscannable media is not published).
	// Requires ClamAVAddr. Default false.
	MalwareScanEnabled bool

	// ClamAVAddr is the clamd TCP address (host:port) used when MalwareScanEnabled.
	ClamAVAddr string

	// MalwareScanMode is the ClamAV fallback policy applied when a scan cannot
	// complete (a dial/protocol/IO error): "fail-closed" (default — unscannable
	// media is not published), "fail-open" (publish anyway, logged loudly), or
	// "quarantine" (park for moderator review). An INFECTED result always fails
	// the publish regardless of mode. Validated to one of the three.
	MalwareScanMode string

	// TranscodingEnabled turns on the HLS transcoding pipeline: publishing a
	// video enqueues a durable transcode job and an in-process worker produces
	// an H.264/AAC HLS ladder served at /api/v1/videos/{id}/hls/*. Requires
	// ffmpeg + ffprobe on PATH (detected at boot; a missing binary logs a
	// warning and leaves transcoding off). Default false.
	TranscodingEnabled bool

	// TranscodingVP9Enabled additionally emits a progressive VP9/WebM alternate
	// alongside the H.264 HLS ladder (surfaced via the video /download list).
	// Only meaningful with TranscodingEnabled. Default false.
	TranscodingVP9Enabled bool

	// TranscodingAV1Enabled is accepted only as false: AV1 transcoding is
	// deferred (see fix_plan P6.3, mirrors the IPFS-storage defer). Setting it
	// true fails config validation with a documented defer note.
	TranscodingAV1Enabled bool

	// WhisperEnabled turns on auto-caption generation (fix_plan P13): a video
	// owner may request an auto-generated WebVTT caption track, produced by
	// extracting the audio (ffmpeg → 16 kHz mono WAV) and POSTing it to an
	// external Whisper transcription service. Requires WhisperEndpoint. Default
	// false — the auto-caption endpoints then answer 503 and no worker runs.
	WhisperEnabled bool

	// WhisperEndpoint is the base URL of the Whisper transcription service. The
	// worker POSTs the extracted audio to <endpoint>/inference as multipart/form-
	// data (`file` + `response_format=verbose_json` [+ `language` hint]) and
	// expects an OpenAI-/whisper.cpp-compatible verbose_json body with a
	// `segments` array of {start,end,text} (seconds), which it renders to WebVTT.
	// It is an OPERATOR-configured internal service (not user input), so it is
	// trusted and not SSRF-guarded. Required when WhisperEnabled.
	WhisperEndpoint string

	// WhisperDefaultLanguage is the BCP-47-ish caption language tag used when an
	// auto-caption request omits one — it is both the tag the caption is stored
	// under and the language hint passed to Whisper. Default "en".
	WhisperDefaultLanguage string

	// FederationKeyKEK is the base64 (standard) 32-byte key-encryption key used to
	// envelope-encrypt actor private keys at rest (AES-256-GCM via internal/secretbox).
	// Required in production when FederationEnabled; empty in dev stores keys raw
	// (with a loud boot warning). NEVER commit a real value. See federation.md §3.
	FederationKeyKEK string

	// MFAKeyKEK is the base64 (standard) 32-byte key-encryption key used to
	// envelope-encrypt TOTP shared secrets at rest (same secretbox envelope as
	// FederationKeyKEK). Deployments that already run a federation KEK can share
	// it: MFAKEK() falls back to FederationKeyKEK when this is unset. With
	// neither set (dev), TOTP secrets are stored raw with a loud boot warning.
	// NEVER commit a real value.
	MFAKeyKEK string

	// TOTPIssuer is the issuer label embedded in TOTP enrollment otpauth:// URIs
	// — what authenticator apps display next to the account. Defaults to the
	// instance name.
	TOTPIssuer string

	// LiveRTMPURL is the base RTMP ingest URL returned to a streamer on live-stream
	// create (the streamer appends their stream key in OBS). Empty until an RTMP
	// ingest is provisioned; the create response then omits it.
	LiveRTMPURL string

	// LiveIngestSecret is the shared secret the media server (RTMP ingest) presents
	// on the internal live-ingest start/stop hooks, so only it can flip a stream's
	// live state. Empty disables the ingest hooks entirely (they 404) — they are
	// only safe to expose when a secret is set.
	LiveIngestSecret string

	// LiveHLSRoot is the filesystem directory the RTMP media server writes live
	// HLS output (and session recordings) into — a volume shared read-only with
	// the api. When set, the api serves a live stream's playlist/segments from it
	// (keyed by stream ID) at GET /api/v1/live/{id}/hls/* and, for replay-enabled
	// streams, reads the recorded session from its `rec/` subdirectory to
	// republish as a VOD. Empty (default) disables live HLS serving and replay —
	// both surface as 404 / no-op until a media server is provisioned.
	LiveHLSRoot string

	// Instance about/legal metadata surfaced at GET /api/v1/instance. All
	// optional (empty when unset).
	InstanceDescription  string
	InstanceTermsURL     string
	InstancePrivacyURL   string
	InstanceContactEmail string

	// OAuthProviders are the configured OIDC login providers (OAUTH_PROVIDERS
	// comma list; per-provider OAUTH_<NAME>_ISSUER/_CLIENT_ID/_CLIENT_SECRET and
	// optional _SCOPES). Empty (the default) disables OAuth login entirely.
	// PublicBaseURL is required when any provider is configured — the OAuth
	// callback redirect URI is ALWAYS derived from it server-side (P15), never
	// from request parameters. Client secrets are secrets: never log them
	// (observability sensitive-key rules).
	OAuthProviders []OAuthProviderConfig

	// RegistrationEnabled controls whether public account signup is accepted.
	RegistrationEnabled bool

	// RegistrationRequireApproval, when true (and registration is enabled), makes
	// signup file a pending registration request for admin approval instead of
	// creating an account directly. Default false.
	RegistrationRequireApproval bool

	// QuarantineNewUploads, when true, parks a finished upload by a
	// non-privileged user (role "user" without the admin-granted
	// bypass_quarantine flag) in the 'quarantined' state instead of publishing,
	// until a moderator approves (publish, hooks fire then) or rejects it.
	// Default false. See product-decisions.md §11.
	QuarantineNewUploads bool

	// Feature toggles (all default true). These are the boot-time defaults for
	// the runtime-mutable instance feature switches: an admin can override each
	// live via PATCH /api/v1/admin/instance-settings (the DB overlay), and the
	// upload/import/live-create/comment-create gates consult the effective
	// value. When a toggle is off, the corresponding endpoint returns 403
	// feature_disabled. See fix_plan P10 (admin instance configuration).
	UploadsEnabled  bool
	ImportsEnabled  bool
	LiveEnabled     bool
	CommentsEnabled bool

	// MailEnabled turns on real outbound email over SMTP: password-reset and
	// email-verification tokens are delivered to the account's address instead
	// of being dropped (the no-op default). Requires SMTPHost + SMTPFrom.
	// DEV_MAIL_CAPTURE_ENABLED still wins when both are set (dev seam).
	MailEnabled bool

	// SMTP delivery settings (used when MailEnabled). SMTPHost/SMTPPort locate
	// the relay (STARTTLS is used whenever the server offers it); SMTPUsername/
	// SMTPPassword are optional AUTH PLAIN credentials — the password is a
	// secret and must NEVER be logged (observability sensitive-key rules);
	// SMTPFrom is the sender address on outgoing mail.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// DevMailCaptureEnabled turns on the DEVELOPMENT-ONLY in-memory mail capture:
	// account-security tokens (password reset, email verification) are held in
	// memory and retrievable via GET /api/v1/dev/email-token instead of being
	// delivered out-of-band. It exists so end-to-end tests can complete the token
	// flows without a real mailer. NEVER enable it in production — it exposes
	// single-use credentials. Default false; the process warns loudly when on.
	DevMailCaptureEnabled bool

	// ImportAllowPrivateURLs relaxes the SSRF guard on URL video import so it may
	// fetch from private/loopback addresses. DEVELOPMENT/TEST-ONLY: it exists so
	// backed end-to-end tests can import from a loopback/compose-network origin
	// (a public origin isn't reachable in CI). NEVER enable it in production — it
	// re-opens the SSRF hole the guard closes. Default false; the process warns
	// loudly when on. See internal/urlsafety.Guard.AllowPrivate.
	ImportAllowPrivateURLs bool

	// Rate limiting (Redis fixed-window) applied to the /api surface.
	RateLimitEnabled  bool
	RateLimitRequests int
	RateLimitWindow   time.Duration
	// AuthRateLimitRequests is a stricter per-IP budget applied (over the same
	// RateLimitWindow) to the sensitive auth endpoints — login, register, and the
	// password-reset / email-verify confirmations — to throttle credential
	// stuffing and token guessing. Gated by RateLimitEnabled.
	AuthRateLimitRequests int

	// JWT signing for access tokens (HS256).
	JWTSecret    string
	JWTIssuer    string
	JWTAudience  string
	JWTAccessTTL time.Duration
	// JWTRefreshTTL is the lifetime of an opaque refresh-token session.
	JWTRefreshTTL time.Duration

	// Media storage. StorageBackend selects the blob backend: "local" (default)
	// or "s3" ("ipfs" later). StorageLocalRoot is the directory for the local
	// backend.
	StorageBackend   string
	StorageLocalRoot string

	// S3-compatible object storage (STORAGE_BACKEND=s3): MinIO (dev, compose
	// "storage" profile), AWS S3, Backblaze B2, DigitalOcean Spaces. Endpoint is
	// host[:port] WITHOUT a scheme — StorageS3UseSSL selects http/https;
	// StorageS3ForcePathStyle addresses the bucket as /<bucket>/<key> (required
	// by MinIO). StorageS3AccessKey/StorageS3SecretKey are credentials and must
	// NEVER be logged (see internal/observability.IsSensitiveKey).
	StorageS3Endpoint       string
	StorageS3Bucket         string
	StorageS3AccessKey      string
	StorageS3SecretKey      string
	StorageS3Region         string
	StorageS3UseSSL         bool
	StorageS3ForcePathStyle bool

	// UploadMaxSize caps a single original-file upload, as an Echo size string
	// (e.g. "2G", "512M"). It overrides HTTPBodyLimit for the upload route only,
	// so the JSON API stays small while media uploads get headroom. Oversized
	// uploads are rejected with 413.
	UploadMaxSize string

	// InstanceDefaultQuotaBytes is the default per-user storage quota in bytes:
	// the total stored size of a user's video files (originals, renditions,
	// thumbnails) across the videos owned via their channels. 0 (or unset) =
	// unlimited. Admins can override per account (nullable
	// users.storage_quota_bytes; NULL inherits this default, 0 = unlimited).
	// Uploads/imports that would exceed the effective quota are rejected with
	// 422 quota_exceeded.
	InstanceDefaultQuotaBytes int64
}

// OAuthProviderConfig describes one OIDC login provider. Providers are generic:
// any spec-compliant OIDC issuer (Google, a GitHub OIDC shim, Keycloak,
// Authentik, …) works via its discovery document. ClientSecret is sensitive
// and must never be logged.
type OAuthProviderConfig struct {
	// Name is the URL-safe identifier used in routes
	// (/api/v1/auth/oauth/<name>) and env-var lookups (dashes map to
	// underscores): lowercase letters, digits, and dashes.
	Name string
	// IssuerURL is the OIDC issuer; discovery is fetched from
	// <issuer>/.well-known/openid-configuration.
	IssuerURL string
	// ClientID / ClientSecret are the OAuth2 client credentials registered
	// with the provider.
	ClientID     string
	ClientSecret string
	// Scopes overrides the requested scopes (default: openid email profile).
	Scopes []string
}

// devJWTSecret is the obviously-fake signing key used only for local dev/test.
// Production must override JWT_SECRET; validate() rejects this value in prod.
const devJWTSecret = "dev-insecure-jwt-secret-change-me-0000000000000000"

// Load reads configuration from the environment, applying safe development
// defaults. It returns an error if a required value is missing or malformed.
//
// Required in production: DATABASE_URL, REDIS_URL. In development they default
// to local Docker Compose service addresses.
func Load() (*Config, error) {
	env := getEnv("VIDRA_ENV", "development")

	cfg := &Config{
		Environment:                 env,
		LogLevel:                    strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:                   strings.ToLower(getEnv("LOG_FORMAT", "json")),
		OTelEnabled:                 getEnvBool("OTEL_ENABLED", false),
		OTelExporterEndpoint:        getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTelExporterProtocol:        strings.ToLower(getEnv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")),
		OTelServiceName:             getEnv("OTEL_SERVICE_NAME", "vidra-core"),
		MetricsEnabled:              getEnvBool("METRICS_ENABLED", false),
		HTTPHost:                    getEnv("HTTP_HOST", "0.0.0.0"),
		InstanceName:                getEnv("INSTANCE_NAME", "Vidra (dev)"),
		PublicBaseURL:               strings.TrimRight(getEnv("PUBLIC_BASE_URL", ""), "/"),
		FederationEnabled:           getEnvBool("FEDERATION_ENABLED", false),
		FederationKeyKEK:            getEnv("FEDERATION_KEY_KEK", ""),
		ATProtoEnabled:              getEnvBool("ATPROTO_ENABLED", false),
		ATProtoKeyKEK:               getEnv("ATPROTO_KEY_KEK", ""),
		MFAKeyKEK:                   getEnv("MFA_KEY_KEK", ""),
		TOTPIssuer:                  getEnv("TOTP_ISSUER", ""),
		MalwareScanEnabled:          getEnvBool("MALWARE_SCAN_ENABLED", false),
		ClamAVAddr:                  getEnv("CLAMAV_ADDR", ""),
		MalwareScanMode:             getEnv("MALWARE_SCAN_MODE", "fail-closed"),
		TranscodingEnabled:          getEnvBool("TRANSCODING_ENABLED", false),
		TranscodingVP9Enabled:       getEnvBool("TRANSCODING_VP9_ENABLED", false),
		TranscodingAV1Enabled:       getEnvBool("TRANSCODING_AV1_ENABLED", false),
		WhisperEnabled:              getEnvBool("WHISPER_ENABLED", false),
		WhisperEndpoint:             strings.TrimRight(getEnv("WHISPER_ENDPOINT", ""), "/"),
		WhisperDefaultLanguage:      strings.TrimSpace(getEnv("WHISPER_DEFAULT_LANGUAGE", "en")),
		LiveRTMPURL:                 getEnv("LIVE_RTMP_URL", ""),
		LiveIngestSecret:            getEnv("LIVE_INGEST_SECRET", ""),
		LiveHLSRoot:                 strings.TrimRight(getEnv("LIVE_HLS_ROOT", ""), "/"),
		InstanceDescription:         getEnv("INSTANCE_DESCRIPTION", ""),
		InstanceTermsURL:            getEnv("INSTANCE_TERMS_URL", ""),
		InstancePrivacyURL:          getEnv("INSTANCE_PRIVACY_URL", ""),
		InstanceContactEmail:        getEnv("INSTANCE_CONTACT_EMAIL", ""),
		RegistrationEnabled:         getEnvBool("REGISTRATION_ENABLED", true),
		RegistrationRequireApproval: getEnvBool("REGISTRATION_REQUIRE_APPROVAL", false),
		QuarantineNewUploads:        getEnvBool("QUARANTINE_NEW_UPLOADS", false),
		UploadsEnabled:              getEnvBool("FEATURE_UPLOADS_ENABLED", true),
		ImportsEnabled:              getEnvBool("FEATURE_IMPORTS_ENABLED", true),
		LiveEnabled:                 getEnvBool("FEATURE_LIVE_ENABLED", true),
		CommentsEnabled:             getEnvBool("FEATURE_COMMENTS_ENABLED", true),
		MailEnabled:                 getEnvBool("MAIL_ENABLED", false),
		SMTPHost:                    getEnv("SMTP_HOST", ""),
		SMTPUsername:                getEnv("SMTP_USERNAME", ""),
		SMTPPassword:                getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:                    getEnv("SMTP_FROM", ""),
		DevMailCaptureEnabled:       getEnvBool("DEV_MAIL_CAPTURE_ENABLED", false),
		ImportAllowPrivateURLs:      getEnvBool("HTTP_IMPORT_ALLOW_PRIVATE_URLS", false),
		DatabaseURL:                 getEnv("DATABASE_URL", "postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable"),
		RedisURL:                    getEnv("REDIS_URL", "redis://localhost:6379/0"),
		CORSAllowedOrigins:          splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
		HTTPReadTimeout:             getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:            getEnvDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPShutdownTimeout:         getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
		HTTPRequestTimeout:          getEnvDuration("HTTP_REQUEST_TIMEOUT", 30*time.Second),
		HTTPBodyLimit:               getEnv("HTTP_BODY_LIMIT", "8M"),
		RateLimitEnabled:            getEnvBool("RATE_LIMIT_ENABLED", true),
		RateLimitWindow:             getEnvDuration("RATE_LIMIT_WINDOW", time.Minute),
		JWTSecret:                   getEnv("JWT_SECRET", devJWTSecret),
		JWTIssuer:                   getEnv("JWT_ISSUER", "vidra"),
		JWTAudience:                 getEnv("JWT_AUDIENCE", "vidra"),
		JWTAccessTTL:                getEnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:               getEnvDuration("JWT_REFRESH_TTL", 720*time.Hour),
		StorageBackend:              getEnv("STORAGE_BACKEND", "local"),
		StorageLocalRoot:            getEnv("STORAGE_LOCAL_ROOT", "./data/media"),
		StorageS3Endpoint:           getEnv("STORAGE_S3_ENDPOINT", ""),
		StorageS3Bucket:             getEnv("STORAGE_S3_BUCKET", ""),
		StorageS3AccessKey:          getEnv("STORAGE_S3_ACCESS_KEY", ""),
		StorageS3SecretKey:          getEnv("STORAGE_S3_SECRET_KEY", ""),
		StorageS3Region:             getEnv("STORAGE_S3_REGION", ""),
		StorageS3UseSSL:             getEnvBool("STORAGE_S3_USE_SSL", true),
		StorageS3ForcePathStyle:     getEnvBool("STORAGE_S3_FORCE_PATH_STYLE", false),
		UploadMaxSize:               getEnv("UPLOAD_MAX_SIZE", "2G"),
	}

	port, err := getEnvInt("HTTP_PORT", 8080)
	if err != nil {
		return nil, err
	}
	cfg.HTTPPort = port

	reqs, err := getEnvInt("RATE_LIMIT_REQUESTS", 120)
	if err != nil {
		return nil, err
	}
	cfg.RateLimitRequests = reqs

	authReqs, err := getEnvInt("AUTH_RATE_LIMIT_REQUESTS", 10)
	if err != nil {
		return nil, err
	}
	cfg.AuthRateLimitRequests = authReqs

	smtpPort, err := getEnvInt("SMTP_PORT", 587)
	if err != nil {
		return nil, err
	}
	cfg.SMTPPort = smtpPort

	quotaBytes, err := getEnvInt64("INSTANCE_DEFAULT_QUOTA_BYTES", 0)
	if err != nil {
		return nil, err
	}
	cfg.InstanceDefaultQuotaBytes = quotaBytes

	for _, name := range splitAndTrim(getEnv("OAUTH_PROVIDERS", "")) {
		prefix := "OAUTH_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		cfg.OAuthProviders = append(cfg.OAuthProviders, OAuthProviderConfig{
			Name:         name,
			IssuerURL:    strings.TrimRight(getEnv(prefix+"_ISSUER", ""), "/"),
			ClientID:     getEnv(prefix+"_CLIENT_ID", ""),
			ClientSecret: getEnv(prefix+"_CLIENT_SECRET", ""),
			Scopes:       splitAndTrim(getEnv(prefix+"_SCOPES", "")),
		})
	}

	// The TOTP issuer label defaults to the instance name (what authenticator
	// apps display); it cannot reference InstanceName inside the literal above.
	if cfg.TOTPIssuer == "" {
		cfg.TOTPIssuer = cfg.InstanceName
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Environment {
	case "development", "test", "production":
	default:
		return fmt.Errorf("config: invalid VIDRA_ENV %q (want development|test|production)", c.Environment)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: invalid LOG_LEVEL %q (want debug|info|warn|error)", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("config: invalid LOG_FORMAT %q (want json|text)", c.LogFormat)
	}
	if c.OTelEnabled {
		switch c.OTelExporterProtocol {
		case "grpc", "http/protobuf":
		default:
			return fmt.Errorf("config: invalid OTEL_EXPORTER_OTLP_PROTOCOL %q (want grpc|http/protobuf)", c.OTelExporterProtocol)
		}
		if strings.TrimSpace(c.OTelExporterEndpoint) == "" {
			return fmt.Errorf("config: OTEL_EXPORTER_OTLP_ENDPOINT is required when OTEL_ENABLED is true")
		}
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return fmt.Errorf("config: HTTP_PORT %d out of range", c.HTTPPort)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if strings.TrimSpace(c.RedisURL) == "" {
		return fmt.Errorf("config: REDIS_URL is required")
	}
	if c.HTTPRequestTimeout <= 0 {
		return fmt.Errorf("config: HTTP_REQUEST_TIMEOUT must be positive")
	}
	if _, err := bytes.Parse(c.HTTPBodyLimit); err != nil {
		return fmt.Errorf("config: invalid HTTP_BODY_LIMIT %q: %w", c.HTTPBodyLimit, err)
	}
	if c.RateLimitEnabled {
		if c.RateLimitRequests <= 0 {
			return fmt.Errorf("config: RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled")
		}
		if c.RateLimitWindow <= 0 {
			return fmt.Errorf("config: RATE_LIMIT_WINDOW must be positive when rate limiting is enabled")
		}
		if c.AuthRateLimitRequests <= 0 {
			return fmt.Errorf("config: AUTH_RATE_LIMIT_REQUESTS must be positive when rate limiting is enabled")
		}
	}
	if c.JWTAccessTTL <= 0 {
		return fmt.Errorf("config: JWT_ACCESS_TTL must be positive")
	}
	if c.JWTRefreshTTL <= 0 {
		return fmt.Errorf("config: JWT_REFRESH_TTL must be positive")
	}
	switch c.StorageBackend {
	case "local":
		if strings.TrimSpace(c.StorageLocalRoot) == "" {
			return fmt.Errorf("config: STORAGE_LOCAL_ROOT is required for the local storage backend")
		}
	case "s3":
		if strings.TrimSpace(c.StorageS3Endpoint) == "" {
			return fmt.Errorf("config: STORAGE_S3_ENDPOINT is required for the s3 storage backend")
		}
		if strings.Contains(c.StorageS3Endpoint, "://") {
			return fmt.Errorf("config: STORAGE_S3_ENDPOINT must be host[:port] without a scheme (got %q); use STORAGE_S3_USE_SSL to pick http/https", c.StorageS3Endpoint)
		}
		if strings.TrimSpace(c.StorageS3Bucket) == "" {
			return fmt.Errorf("config: STORAGE_S3_BUCKET is required for the s3 storage backend")
		}
		if strings.TrimSpace(c.StorageS3AccessKey) == "" {
			return fmt.Errorf("config: STORAGE_S3_ACCESS_KEY is required for the s3 storage backend")
		}
		if strings.TrimSpace(c.StorageS3SecretKey) == "" {
			return fmt.Errorf("config: STORAGE_S3_SECRET_KEY is required for the s3 storage backend")
		}
	default:
		return fmt.Errorf("config: unsupported STORAGE_BACKEND %q (want local|s3)", c.StorageBackend)
	}
	if _, err := bytes.Parse(c.UploadMaxSize); err != nil {
		return fmt.Errorf("config: invalid UPLOAD_MAX_SIZE %q: %w", c.UploadMaxSize, err)
	}
	if c.InstanceDefaultQuotaBytes < 0 {
		return fmt.Errorf("config: INSTANCE_DEFAULT_QUOTA_BYTES must be >= 0 (0 = unlimited), got %d", c.InstanceDefaultQuotaBytes)
	}
	if c.Environment == "production" {
		if c.JWTSecret == devJWTSecret {
			return fmt.Errorf("config: JWT_SECRET must be set in production (the dev default is not allowed)")
		}
		if len(c.JWTSecret) < 32 {
			return fmt.Errorf("config: JWT_SECRET must be at least 32 bytes in production")
		}
	}
	if c.Environment == "production" {
		for _, o := range c.CORSAllowedOrigins {
			if o == "*" {
				return fmt.Errorf("config: wildcard CORS origin is not allowed in production")
			}
		}
	}
	if c.MailEnabled {
		if strings.TrimSpace(c.SMTPHost) == "" {
			return fmt.Errorf("config: SMTP_HOST is required when MAIL_ENABLED=true")
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return fmt.Errorf("config: SMTP_PORT %d out of range", c.SMTPPort)
		}
		from := strings.TrimSpace(c.SMTPFrom)
		if from == "" {
			return fmt.Errorf("config: SMTP_FROM is required when MAIL_ENABLED=true")
		}
		if !strings.Contains(from, "@") || strings.ContainsAny(from, "\r\n") {
			return fmt.Errorf("config: SMTP_FROM must be a plain email address")
		}
	}
	if c.MalwareScanEnabled && strings.TrimSpace(c.ClamAVAddr) == "" {
		return fmt.Errorf("config: CLAMAV_ADDR is required when MALWARE_SCAN_ENABLED=true")
	}
	switch c.MalwareScanMode {
	case "", "fail-closed", "fail-open", "quarantine": // "" = default fail-closed
	default:
		return fmt.Errorf("config: MALWARE_SCAN_MODE %q must be one of fail-closed, fail-open, quarantine", c.MalwareScanMode)
	}
	if c.TranscodingAV1Enabled {
		return fmt.Errorf("config: TRANSCODING_AV1_ENABLED is not supported yet — AV1 transcoding is deferred (see fix_plan P6.3); leave it false")
	}
	if c.WhisperEnabled {
		u, err := url.Parse(c.WhisperEndpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: WHISPER_ENDPOINT must be a valid http(s) URL when WHISPER_ENABLED=true")
		}
	}
	if c.WhisperDefaultLanguage != "" && !languageTag.MatchString(c.WhisperDefaultLanguage) {
		return fmt.Errorf("config: WHISPER_DEFAULT_LANGUAGE %q must be a BCP-47-ish language tag (e.g. en, pt-BR)", c.WhisperDefaultLanguage)
	}
	if c.MFAKeyKEK != "" {
		if k, err := base64.StdEncoding.DecodeString(c.MFAKeyKEK); err != nil || len(k) != 32 {
			return fmt.Errorf("config: MFA_KEY_KEK must be base64 of exactly 32 bytes")
		}
	}
	if err := c.validateOAuth(); err != nil {
		return err
	}
	if c.FederationEnabled {
		u, err := url.Parse(c.PublicBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: PUBLIC_BASE_URL must be a valid http(s) origin when FEDERATION_ENABLED=true")
		}
		if u.Path != "" && u.Path != "/" {
			return fmt.Errorf("config: PUBLIC_BASE_URL must not include a path (got %q)", c.PublicBaseURL)
		}
		if c.Environment == "production" && u.Scheme != "https" {
			return fmt.Errorf("config: PUBLIC_BASE_URL must be https in production")
		}
		if c.FederationKeyKEK != "" {
			if k, err := base64.StdEncoding.DecodeString(c.FederationKeyKEK); err != nil || len(k) != 32 {
				return fmt.Errorf("config: FEDERATION_KEY_KEK must be base64 of exactly 32 bytes")
			}
		} else if c.Environment == "production" {
			return fmt.Errorf("config: FEDERATION_KEY_KEK is required in production when FEDERATION_ENABLED=true")
		}
	}
	if c.ATProtoKeyKEK != "" {
		if k, err := base64.StdEncoding.DecodeString(c.ATProtoKeyKEK); err != nil || len(k) != 32 {
			return fmt.Errorf("config: ATPROTO_KEY_KEK must be base64 of exactly 32 bytes")
		}
	}
	if c.ATProtoEnabled && c.Environment == "production" && c.ATProtoKEK() == "" {
		return fmt.Errorf("config: ATPROTO_KEY_KEK (or FEDERATION_KEY_KEK) is required in production when ATPROTO_ENABLED=true")
	}
	return nil
}

// oauthProviderName constrains provider names to URL-path- and env-var-safe
// identifiers: lowercase letters/digits, dash-separated.
var oauthProviderName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// languageTag is a permissive BCP-47-ish caption language tag (mirrors the
// pattern internal/video enforces on caption languages), used to validate
// WHISPER_DEFAULT_LANGUAGE at load time.
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// validateOAuth checks the configured OIDC providers. Providers are disabled
// by default (no OAUTH_PROVIDERS); each configured provider must be complete,
// and PUBLIC_BASE_URL must be set because the callback redirect URI is derived
// from it server-side (never from request parameters — P15).
func (c *Config) validateOAuth() error {
	if len(c.OAuthProviders) == 0 {
		return nil
	}
	u, err := url.Parse(c.PublicBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("config: PUBLIC_BASE_URL must be a valid http(s) origin when OAUTH_PROVIDERS is set (OAuth redirect URIs derive from it)")
	}
	if c.Environment == "production" && u.Scheme != "https" {
		return fmt.Errorf("config: PUBLIC_BASE_URL must be https in production")
	}
	seen := map[string]bool{}
	for _, p := range c.OAuthProviders {
		if !oauthProviderName.MatchString(p.Name) {
			return fmt.Errorf("config: invalid OAuth provider name %q (want lowercase letters/digits/dashes)", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("config: duplicate OAuth provider %q in OAUTH_PROVIDERS", p.Name)
		}
		seen[p.Name] = true
		envName := strings.ToUpper(strings.ReplaceAll(p.Name, "-", "_"))
		iss, err := url.Parse(p.IssuerURL)
		if err != nil || iss.Host == "" || (iss.Scheme != "http" && iss.Scheme != "https") {
			return fmt.Errorf("config: OAUTH_%s_ISSUER must be a valid http(s) URL", envName)
		}
		if c.Environment == "production" && iss.Scheme != "https" {
			return fmt.Errorf("config: OAUTH_%s_ISSUER must be https in production", envName)
		}
		if strings.TrimSpace(p.ClientID) == "" {
			return fmt.Errorf("config: OAUTH_%s_CLIENT_ID is required", envName)
		}
		if strings.TrimSpace(p.ClientSecret) == "" {
			return fmt.Errorf("config: OAUTH_%s_CLIENT_SECRET is required", envName)
		}
	}
	return nil
}

// OAuthProviderNames returns the configured provider names in configuration
// order (the order OAUTH_PROVIDERS lists them), for the public instance
// document. Never nil.
func (c *Config) OAuthProviderNames() []string {
	names := make([]string, 0, len(c.OAuthProviders))
	for _, p := range c.OAuthProviders {
		names = append(names, p.Name)
	}
	return names
}

// HTTPAddr returns the host:port the HTTP server should bind to.
func (c *Config) HTTPAddr() string {
	return fmt.Sprintf("%s:%d", c.HTTPHost, c.HTTPPort)
}

// CookieSecure reports whether auth cookies (the vidra_refresh cookie set by
// cookie-mode sessions) must carry the Secure attribute. True when the
// instance's canonical public origin is https (PUBLIC_BASE_URL), and always in
// production — fail-secure even when PUBLIC_BASE_URL is unset, since production
// deployments are expected to terminate TLS. Plain-http local development keeps
// Secure off so the cookie still works on http://localhost.
func (c *Config) CookieSecure() bool {
	if strings.HasPrefix(strings.ToLower(c.PublicBaseURL), "https://") {
		return true
	}
	return c.Environment == "production"
}

// ATProtoKEK returns the key-encryption key that seals linked Bluesky app
// passwords at rest: ATPROTO_KEY_KEK when set, otherwise FEDERATION_KEY_KEK (so a
// deployment already running a federation KEK covers ATProto without a second
// secret). Empty means no sealing (dev-only; cmd/api warns loudly).
func (c *Config) ATProtoKEK() string {
	if c.ATProtoKeyKEK != "" {
		return c.ATProtoKeyKEK
	}
	return c.FederationKeyKEK
}

// MFAKEK returns the key-encryption key that seals TOTP secrets at rest:
// MFA_KEY_KEK when set, otherwise FEDERATION_KEY_KEK (so a deployment already
// running a federation KEK covers MFA without a second secret). Empty means no
// sealing (dev-only; cmd/api warns loudly).
func (c *Config) MFAKEK() string {
	if c.MFAKeyKEK != "" {
		return c.MFAKeyKEK
	}
	return c.FederationKeyKEK
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func getEnvInt64(key string, def int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func getEnvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
