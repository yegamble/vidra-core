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
