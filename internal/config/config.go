// Package config loads and validates Vidra backend configuration from the
// environment. Configuration is the single source of truth for runtime wiring;
// no other package should read os.Getenv directly.
package config

import (
	"fmt"
	"os"
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

	// LiveRTMPURL is the base RTMP ingest URL returned to a streamer on live-stream
	// create (the streamer appends their stream key in OBS). Empty until an RTMP
	// ingest is provisioned; the create response then omits it.
	LiveRTMPURL string

	// Instance about/legal metadata surfaced at GET /api/v1/instance. All
	// optional (empty when unset).
	InstanceDescription  string
	InstanceTermsURL     string
	InstancePrivacyURL   string
	InstanceContactEmail string

	// RegistrationEnabled controls whether public account signup is accepted.
	RegistrationEnabled bool

	// RegistrationRequireApproval, when true (and registration is enabled), makes
	// signup file a pending registration request for admin approval instead of
	// creating an account directly. Default false.
	RegistrationRequireApproval bool

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

	// Media storage. StorageBackend selects the blob backend ("local" today;
	// "s3"/"ipfs" later). StorageLocalRoot is the directory for the local backend.
	StorageBackend   string
	StorageLocalRoot string

	// UploadMaxSize caps a single original-file upload, as an Echo size string
	// (e.g. "2G", "512M"). It overrides HTTPBodyLimit for the upload route only,
	// so the JSON API stays small while media uploads get headroom. Oversized
	// uploads are rejected with 413.
	UploadMaxSize string
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
		HTTPHost:                    getEnv("HTTP_HOST", "0.0.0.0"),
		InstanceName:                getEnv("INSTANCE_NAME", "Vidra (dev)"),
		LiveRTMPURL:                 getEnv("LIVE_RTMP_URL", ""),
		InstanceDescription:         getEnv("INSTANCE_DESCRIPTION", ""),
		InstanceTermsURL:            getEnv("INSTANCE_TERMS_URL", ""),
		InstancePrivacyURL:          getEnv("INSTANCE_PRIVACY_URL", ""),
		InstanceContactEmail:        getEnv("INSTANCE_CONTACT_EMAIL", ""),
		RegistrationEnabled:         getEnvBool("REGISTRATION_ENABLED", true),
		RegistrationRequireApproval: getEnvBool("REGISTRATION_REQUIRE_APPROVAL", false),
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
	default:
		return fmt.Errorf("config: unsupported STORAGE_BACKEND %q (want local)", c.StorageBackend)
	}
	if _, err := bytes.Parse(c.UploadMaxSize); err != nil {
		return fmt.Errorf("config: invalid UPLOAD_MAX_SIZE %q: %w", c.UploadMaxSize, err)
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
	return nil
}

// HTTPAddr returns the host:port the HTTP server should bind to.
func (c *Config) HTTPAddr() string {
	return fmt.Sprintf("%s:%d", c.HTTPHost, c.HTTPPort)
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
