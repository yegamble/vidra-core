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

	t.Setenv("ENV", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("GO_ENV", "")
	t.Setenv("NODE_ENV", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
}

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

func TestLoad_SetupModeAllowsPartialConfig(t *testing.T) {
	tests := []struct {
		name          string
		envVars       map[string]string
		wantSetupMode bool
		wantErr       bool
	}{
		{
			name: "missing DATABASE_URL with setup not completed - should allow setup mode",
			envVars: map[string]string{
				"PORT": "8080",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "missing REDIS_URL with setup not completed - should allow setup mode",
			envVars: map[string]string{
				"PORT":         "8080",
				"DATABASE_URL": "postgres://user:pass@localhost/db",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "missing JWT_SECRET with setup not completed - should allow setup mode",
			envVars: map[string]string{
				"PORT":         "8080",
				"DATABASE_URL": "postgres://user:pass@localhost/db",
				"REDIS_URL":    "redis://localhost:6379",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "all required fields with SETUP_COMPLETED=true - should be normal mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "true",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: false,
			wantErr:       false,
		},
		{
			name: "all required fields but SETUP_COMPLETED=false - should be setup mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "false",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			cfg, err := Load()
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && cfg == nil {
				t.Errorf("Load() returned nil config without error")
				return
			}

			if !tt.wantErr && cfg.SetupMode != tt.wantSetupMode {
				t.Errorf("Load() SetupMode = %v, want %v", cfg.SetupMode, tt.wantSetupMode)
			}
		})
	}
}

func TestLoad_NormalModeRequiresAllFields(t *testing.T) {
	t.Setenv("SETUP_COMPLETED", "true")
	t.Setenv("PORT", "8080")
	t.Setenv("REQUIRE_IPFS", "false")

	_, err := Load()
	if err == nil {
		t.Error("Load() should error when DATABASE_URL is missing in normal mode")
	}

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	_, err = Load()
	if err == nil {
		t.Error("Load() should error when REDIS_URL is missing in normal mode")
	}

	t.Setenv("REDIS_URL", "redis://localhost:6379")
	_, err = Load()
	if err == nil {
		t.Error("Load() should error when JWT_SECRET is missing in normal mode")
	}

	t.Setenv("JWT_SECRET", "this-is-a-very-long-secret-key-at-least-32-characters-long")
	cfg, err := Load()
	if err != nil {
		t.Errorf("Load() should succeed with all required fields in normal mode: %v", err)
	}
	if cfg == nil {
		t.Error("Load() returned nil config")
	}
	if cfg.SetupMode {
		t.Error("Load() should not be in setup mode when all fields are present")
	}
}
