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

	// Rate limiting (Redis fixed-window) applied to the /api surface.
	RateLimitEnabled  bool
	RateLimitRequests int
	RateLimitWindow   time.Duration

	// JWT signing for access tokens (HS256).
	JWTSecret    string
	JWTIssuer    string
	JWTAudience  string
	JWTAccessTTL time.Duration
	// JWTRefreshTTL is the lifetime of an opaque refresh-token session.
	JWTRefreshTTL time.Duration
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
		Environment:         env,
		HTTPHost:            getEnv("HTTP_HOST", "0.0.0.0"),
		InstanceName:        getEnv("INSTANCE_NAME", "Vidra (dev)"),
		DatabaseURL:         getEnv("DATABASE_URL", "postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable"),
		RedisURL:            getEnv("REDIS_URL", "redis://localhost:6379/0"),
		CORSAllowedOrigins:  splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
		HTTPReadTimeout:     getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:    getEnvDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPShutdownTimeout: getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
		HTTPRequestTimeout:  getEnvDuration("HTTP_REQUEST_TIMEOUT", 30*time.Second),
		HTTPBodyLimit:       getEnv("HTTP_BODY_LIMIT", "8M"),
		RateLimitEnabled:    getEnvBool("RATE_LIMIT_ENABLED", true),
		RateLimitWindow:     getEnvDuration("RATE_LIMIT_WINDOW", time.Minute),
		JWTSecret:           getEnv("JWT_SECRET", devJWTSecret),
		JWTIssuer:           getEnv("JWT_ISSUER", "vidra"),
		JWTAudience:         getEnv("JWT_AUDIENCE", "vidra"),
		JWTAccessTTL:        getEnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:       getEnvDuration("JWT_REFRESH_TTL", 720*time.Hour),
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
	}
	if c.JWTAccessTTL <= 0 {
		return fmt.Errorf("config: JWT_ACCESS_TTL must be positive")
	}
	if c.JWTRefreshTTL <= 0 {
		return fmt.Errorf("config: JWT_REFRESH_TTL must be positive")
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
