package config

import (
	"os"
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
