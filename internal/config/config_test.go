package config

import (
	"strings"
	"testing"
)

func setMinimumLoadEnv(t *testing.T, jwtSecret string) {
	t.Helper()

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("IPFS_API", "http://localhost:5001")
	t.Setenv("REQUIRE_IPFS", "true")
	t.Setenv("JWT_SECRET", jwtSecret)

	// Keep environment explicit in tests so production checks are deterministic.
	t.Setenv("ENV", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("GO_ENV", "")
	t.Setenv("NODE_ENV", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
}

// Test that scheduler defaults are set and Load() succeeds with minimal env.
func TestLoad_SchedulerDefaults(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")

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

func TestLoad_RejectsInsecureJWTSecretInProduction(t *testing.T) {
	setMinimumLoadEnv(t, "your-super-secret-jwt-key-change-in-production")
	t.Setenv("ENVIRONMENT", "production")

	_, err := Load()
	if err == nil {
		t.Fatal("expected production JWT secret validation error, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET is insecure for production") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_AllowsStrongJWTSecretInProduction(t *testing.T) {
	setMinimumLoadEnv(t, "2f57c7f4b9d54d0f9f09177f9fe7056f7f6b50ca9df9ad58f353f3b4c2cb68f9")
	t.Setenv("ENVIRONMENT", "production")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed with strong production secret: %v", err)
	}
	if cfg.JWTSecret == "" {
		t.Fatal("expected JWT secret to be loaded")
	}
}

func TestParsePortFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "default",
			args: []string{"-test.v"},
			want: 8080,
		},
		{
			name: "short flag separate value",
			args: []string{"-port", "9090"},
			want: 9090,
		},
		{
			name: "short flag inline value",
			args: []string{"-port=9091"},
			want: 9091,
		},
		{
			name: "long flag inline value",
			args: []string{"--port=9092"},
			want: 9092,
		},
		{
			name: "invalid value keeps default",
			args: []string{"-port", "invalid"},
			want: 8080,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePortFromArgs(tc.args, 8080)
			if got != tc.want {
				t.Fatalf("parsePortFromArgs(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}
