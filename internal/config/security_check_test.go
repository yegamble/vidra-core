package config

import (
	"os"
	"strings"
	"testing"
)

func TestSecurityChecks(t *testing.T) {
	// Helper to reset env
	resetEnv := func() {
		os.Unsetenv("APP_ENV")
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("REDIS_URL")
		os.Unsetenv("REQUIRE_IPFS")
	}
	// Setup required envs
	setupBaseEnv := func() {
		os.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
		os.Setenv("REDIS_URL", "redis://localhost:6379")
		os.Setenv("REQUIRE_IPFS", "false") // Disable IPFS requirement for this test
	}

	t.Run("Default insecure secret in development", func(t *testing.T) {
		resetEnv()
		setupBaseEnv()
		defer resetEnv()

		os.Setenv("APP_ENV", "development")
		os.Setenv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Expected no error in development with default secret, got: %v", err)
		}
		if cfg.JWTSecret != "your-super-secret-jwt-key-change-in-production" {
			t.Errorf("Expected default secret, got %s", cfg.JWTSecret)
		}
	})

	t.Run("Default insecure secret in production", func(t *testing.T) {
		resetEnv()
		setupBaseEnv()
		defer resetEnv()

		os.Setenv("APP_ENV", "production")
		os.Setenv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")

		_, err := Load()
		if err == nil {
			t.Fatal("Expected error in production with default secret, got nil")
		}
		if !strings.Contains(err.Error(), "default insecure JWT_SECRET") {
			t.Errorf("Expected error message to mention insecure default, got: %v", err)
		}
	})

	t.Run("Short secret in production", func(t *testing.T) {
		resetEnv()
		setupBaseEnv()
		defer resetEnv()

		os.Setenv("APP_ENV", "production")
		os.Setenv("JWT_SECRET", "shortsecret") // < 32 chars

		_, err := Load()
		if err == nil {
			t.Fatal("Expected error in production with short secret, got nil")
		}
		if !strings.Contains(err.Error(), "JWT_SECRET is too short") {
			t.Errorf("Expected error message to mention short secret, got: %v", err)
		}
	})

	t.Run("Valid secret in production", func(t *testing.T) {
		resetEnv()
		setupBaseEnv()
		defer resetEnv()

		os.Setenv("APP_ENV", "production")
		// 32 chars
		validSecret := "12345678901234567890123456789012"
		os.Setenv("JWT_SECRET", validSecret)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Expected no error in production with valid secret, got: %v", err)
		}
		if cfg.JWTSecret != validSecret {
			t.Errorf("Expected valid secret, got %s", cfg.JWTSecret)
		}
	})
}
