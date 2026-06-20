package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Ensure a clean env so defaults apply.
	for _, k := range []string{"VIDRA_ENV", "HTTP_PORT", "DATABASE_URL", "REDIS_URL", "CORS_ALLOWED_ORIGINS"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, want development", cfg.Environment)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080", cfg.HTTPPort)
	}
	if cfg.HTTPAddr() != "0.0.0.0:8080" {
		t.Errorf("HTTPAddr() = %q, want 0.0.0.0:8080", cfg.HTTPAddr())
	}
	if cfg.HTTPShutdownTimeout != 20*time.Second {
		t.Errorf("HTTPShutdownTimeout = %v, want 20s", cfg.HTTPShutdownTimeout)
	}
	if cfg.HTTPRequestTimeout != 30*time.Second {
		t.Errorf("HTTPRequestTimeout = %v, want 30s", cfg.HTTPRequestTimeout)
	}
	if cfg.HTTPBodyLimit != "8M" {
		t.Errorf("HTTPBodyLimit = %q, want 8M", cfg.HTTPBodyLimit)
	}
}

func TestRateLimitDefaults(t *testing.T) {
	for _, k := range []string{"RATE_LIMIT_ENABLED", "RATE_LIMIT_REQUESTS", "RATE_LIMIT_WINDOW"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.RateLimitEnabled {
		t.Error("RateLimitEnabled = false, want true by default")
	}
	if cfg.RateLimitRequests != 120 {
		t.Errorf("RateLimitRequests = %d, want 120", cfg.RateLimitRequests)
	}
	if cfg.RateLimitWindow != time.Minute {
		t.Errorf("RateLimitWindow = %v, want 1m", cfg.RateLimitWindow)
	}
}

func TestStorageDefaults(t *testing.T) {
	for _, k := range []string{"STORAGE_BACKEND", "STORAGE_LOCAL_ROOT"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.StorageBackend != "local" {
		t.Errorf("StorageBackend = %q, want local", cfg.StorageBackend)
	}
	if cfg.StorageLocalRoot == "" {
		t.Error("StorageLocalRoot is empty, want a default")
	}
}

func TestStorageRejectsUnsupportedBackend(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "s3")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for unsupported STORAGE_BACKEND, got nil")
	}
}

func TestUploadMaxSizeDefaultAndOverride(t *testing.T) {
	t.Setenv("UPLOAD_MAX_SIZE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UploadMaxSize != "2G" {
		t.Errorf("UploadMaxSize = %q, want 2G", cfg.UploadMaxSize)
	}
	t.Setenv("UPLOAD_MAX_SIZE", "512M")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() override error = %v", err)
	}
	if cfg.UploadMaxSize != "512M" {
		t.Errorf("UploadMaxSize = %q, want 512M", cfg.UploadMaxSize)
	}
}

func TestUploadMaxSizeRejectsInvalid(t *testing.T) {
	t.Setenv("UPLOAD_MAX_SIZE", "not-a-size")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid UPLOAD_MAX_SIZE, got nil")
	}
}

func TestRateLimitDisabledSkipsValidation(t *testing.T) {
	t.Setenv("RATE_LIMIT_ENABLED", "false")
	t.Setenv("RATE_LIMIT_REQUESTS", "0") // invalid, but ignored when disabled
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RateLimitEnabled {
		t.Error("RateLimitEnabled = true, want false")
	}
}

func TestRateLimitRejectsNonPositiveWhenEnabled(t *testing.T) {
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_REQUESTS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for non-positive RATE_LIMIT_REQUESTS, got nil")
	}
}

func TestJWTDefaults(t *testing.T) {
	for _, k := range []string{"JWT_SECRET", "JWT_ISSUER", "JWT_AUDIENCE", "JWT_ACCESS_TTL"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.JWTIssuer != "vidra" || cfg.JWTAudience != "vidra" {
		t.Errorf("issuer/audience = %q/%q, want vidra/vidra", cfg.JWTIssuer, cfg.JWTAudience)
	}
	if cfg.JWTAccessTTL != 15*time.Minute {
		t.Errorf("JWTAccessTTL = %v, want 15m", cfg.JWTAccessTTL)
	}
}

func TestProductionRejectsDevJWTSecret(t *testing.T) {
	t.Setenv("VIDRA_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.test")
	t.Setenv("JWT_SECRET", "") // falls back to the dev default
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for dev JWT secret in production, got nil")
	}
}

func TestProductionAcceptsStrongJWTSecret(t *testing.T) {
	t.Setenv("VIDRA_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.test")
	t.Setenv("JWT_SECRET", "a-sufficiently-long-production-secret-0001")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestLoadInvalidBodyLimit(t *testing.T) {
	t.Setenv("HTTP_BODY_LIMIT", "not-a-size")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid HTTP_BODY_LIMIT, got nil")
	}
}

func TestLoadBodyLimitOverride(t *testing.T) {
	t.Setenv("HTTP_BODY_LIMIT", "512K")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPBodyLimit != "512K" {
		t.Errorf("HTTPBodyLimit = %q, want 512K", cfg.HTTPBodyLimit)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	t.Setenv("HTTP_PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid HTTP_PORT, got nil")
	}
}

func TestLoadPortOutOfRange(t *testing.T) {
	t.Setenv("HTTP_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for out-of-range HTTP_PORT, got nil")
	}
}

func TestLoadInvalidEnv(t *testing.T) {
	t.Setenv("VIDRA_ENV", "staging")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid VIDRA_ENV, got nil")
	}
}

func TestProductionRejectsWildcardCORS(t *testing.T) {
	t.Setenv("VIDRA_ENV", "production")
	t.Setenv("CORS_ALLOWED_ORIGINS", "*")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for wildcard CORS in production, got nil")
	}
}

func TestCORSOriginsParsing(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://a.test, http://b.test ,")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("CORSAllowedOrigins = %v, want 2 entries", cfg.CORSAllowedOrigins)
	}
}
