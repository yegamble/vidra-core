package config

import (
	"os"
	"strings"
	"testing"
)

// Test that scheduler defaults are set and Load() succeeds with minimal env
func TestLoad_SchedulerDefaults(t *testing.T) {
	// Required envs for Load()
	_ = os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	_ = os.Setenv("REDIS_URL", "redis://localhost:6379/0")
	_ = os.Setenv("IPFS_API", "http://localhost:5001")
	_ = os.Setenv("JWT_SECRET", "test-secret")
	defer func() {
		_ = os.Unsetenv("DATABASE_URL")
		_ = os.Unsetenv("REDIS_URL")
		_ = os.Unsetenv("IPFS_API")
		_ = os.Unsetenv("JWT_SECRET")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !cfg.EnableEncodingScheduler {
		t.Fatalf("expected EnableEncodingScheduler default true")
	}
	if cfg.EncodingSchedulerIntervalSeconds <= 0 {
		t.Fatalf("expected positive EncodingSchedulerIntervalSeconds, got %d", cfg.EncodingSchedulerIntervalSeconds)
	}
	if cfg.EncodingSchedulerBurst <= 0 {
		t.Fatalf("expected positive EncodingSchedulerBurst, got %d", cfg.EncodingSchedulerBurst)
	}
}

func TestLoad_SecurityChecks(t *testing.T) {
	// Setup minimal required envs
	setupEnv := func() {
		_ = os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db?sslmode=disable")
		_ = os.Setenv("REDIS_URL", "redis://localhost:6379/0")
		_ = os.Setenv("IPFS_API", "http://localhost:5001")
	}
	cleanupEnv := func() {
		_ = os.Unsetenv("DATABASE_URL")
		_ = os.Unsetenv("REDIS_URL")
		_ = os.Unsetenv("IPFS_API")
		_ = os.Unsetenv("JWT_SECRET")
		_ = os.Unsetenv("NODE_ENV")
	}

	t.Run("Production with insecure secret should fail", func(t *testing.T) {
		setupEnv()
		defer cleanupEnv()

		_ = os.Setenv("NODE_ENV", "production")
		_ = os.Setenv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error when using insecure secret in production, got nil")
		}
		if !strings.Contains(err.Error(), "Security risk") {
			t.Errorf("expected error to contain 'Security risk', got: %v", err)
		}
	})

	t.Run("Production with NEW insecure secret should fail", func(t *testing.T) {
		setupEnv()
		defer cleanupEnv()

		_ = os.Setenv("NODE_ENV", "production")
		_ = os.Setenv("JWT_SECRET", "CHANGE_ME_IN_PRODUCTION_openssl_rand_hex_32")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error when using new insecure secret in production, got nil")
		}
		if !strings.Contains(err.Error(), "Security risk") {
			t.Errorf("expected error to contain 'Security risk', got: %v", err)
		}
	})

	t.Run("Development with insecure secret should succeed (with warning)", func(t *testing.T) {
		setupEnv()
		defer cleanupEnv()

		_ = os.Setenv("NODE_ENV", "development")
		_ = os.Setenv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")

		_, err := Load()
		if err != nil {
			t.Fatalf("expected no error in development with insecure secret, got: %v", err)
		}
	})

	t.Run("Production with secure secret should succeed", func(t *testing.T) {
		setupEnv()
		defer cleanupEnv()

		_ = os.Setenv("NODE_ENV", "production")
		_ = os.Setenv("JWT_SECRET", "secure-random-secret-key-12345")

		_, err := Load()
		if err != nil {
			t.Fatalf("expected no error in production with secure secret, got: %v", err)
		}
	})
}
