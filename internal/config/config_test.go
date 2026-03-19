package config

import (
	"fmt"
	"os"
	"path/filepath"
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
		{
			name: "all fields unset - should be setup mode",
			envVars: map[string]string{
				"PORT": "8080",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "all required fields with SETUP_COMPLETED unset - should be normal mode",
			envVars: map[string]string{
				"PORT":         "8080",
				"DATABASE_URL": "postgres://user:pass@localhost/db",
				"REDIS_URL":    "redis://localhost:6379",
				"JWT_SECRET":   "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"REQUIRE_IPFS": "false",
			},
			wantSetupMode: false,
			wantErr:       false,
		},
		{
			name: "SETUP_COMPLETED=0 with all fields - should be setup mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "0",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "SETUP_COMPLETED=1 with all fields - should be normal mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "1",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: false,
			wantErr:       false,
		},
		{
			name: "SETUP_COMPLETED=true with missing DATABASE_URL - should error",
			envVars: map[string]string{
				"PORT":            "8080",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "true",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: false,
			wantErr:       true,
		},
		{
			name: "SETUP_COMPLETED=true with missing REDIS_URL - should error",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "true",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: false,
			wantErr:       true,
		},
		{
			name: "SETUP_COMPLETED=true with missing JWT_SECRET - should error",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"SETUP_COMPLETED": "true",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: false,
			wantErr:       true,
		},
		{
			name: "SETUP_COMPLETED with whitespace ' false ' - should be setup mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "  false  ",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "SETUP_COMPLETED=FALSE (uppercase) - should be setup mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "FALSE",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: true,
			wantErr:       false,
		},
		{
			name: "SETUP_COMPLETED=True (mixed case) - should be normal mode",
			envVars: map[string]string{
				"PORT":            "8080",
				"DATABASE_URL":    "postgres://user:pass@localhost/db",
				"REDIS_URL":       "redis://localhost:6379",
				"JWT_SECRET":      "this-is-a-very-long-secret-key-at-least-32-characters-long",
				"SETUP_COMPLETED": "True",
				"REQUIRE_IPFS":    "false",
			},
			wantSetupMode: false,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENVIRONMENT", "")
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
		t.Fatal("Load() returned nil config")
	}
	if cfg.SetupMode {
		t.Error("Load() should not be in setup mode when all fields are present")
	}
}

func TestLoad_WhisperTempDirDefault(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")
	t.Setenv("WHISPER_TEMP_DIR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	expectedBase := os.TempDir()
	expectedPath := filepath.Join(expectedBase, "whisper")

	if cfg.WhisperTempDir != expectedPath {
		t.Errorf("expected WhisperTempDir to be %s, got %s", expectedPath, cfg.WhisperTempDir)
	}
}

func TestLoad_NginxDefaults(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.NginxEnabled {
		t.Error("expected NginxEnabled default false")
	}
	if cfg.NginxDomain != "localhost" {
		t.Errorf("expected NginxDomain default 'localhost', got %s", cfg.NginxDomain)
	}
	if cfg.NginxPort != 80 {
		t.Errorf("expected NginxPort default 80, got %d", cfg.NginxPort)
	}
	if cfg.NginxProtocol != "http" {
		t.Errorf("expected NginxProtocol default 'http', got %s", cfg.NginxProtocol)
	}
	if cfg.NginxTLSMode != "" {
		t.Errorf("expected NginxTLSMode default '', got %s", cfg.NginxTLSMode)
	}
	if cfg.NginxLetsEncryptEmail != "" {
		t.Errorf("expected NginxLetsEncryptEmail default '', got %s", cfg.NginxLetsEncryptEmail)
	}
}

func TestLoad_NginxHTTPConfig(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")
	t.Setenv("NGINX_ENABLED", "true")
	t.Setenv("NGINX_DOMAIN", "videos.local")
	t.Setenv("NGINX_PORT", "8080")
	t.Setenv("NGINX_PROTOCOL", "http")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !cfg.NginxEnabled {
		t.Error("expected NginxEnabled true")
	}
	if cfg.NginxDomain != "videos.local" {
		t.Errorf("expected NginxDomain 'videos.local', got %s", cfg.NginxDomain)
	}
	if cfg.NginxPort != 8080 {
		t.Errorf("expected NginxPort 8080, got %d", cfg.NginxPort)
	}
	if cfg.NginxProtocol != "http" {
		t.Errorf("expected NginxProtocol 'http', got %s", cfg.NginxProtocol)
	}
}

func TestLoad_NginxHTTPSSelfsigned(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")
	t.Setenv("NGINX_ENABLED", "true")
	t.Setenv("NGINX_DOMAIN", "videos.example.com")
	t.Setenv("NGINX_PORT", "443")
	t.Setenv("NGINX_PROTOCOL", "https")
	t.Setenv("NGINX_TLS_MODE", "self-signed")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !cfg.NginxEnabled {
		t.Error("expected NginxEnabled true")
	}
	if cfg.NginxDomain != "videos.example.com" {
		t.Errorf("expected NginxDomain 'videos.example.com', got %s", cfg.NginxDomain)
	}
	if cfg.NginxPort != 443 {
		t.Errorf("expected NginxPort 443, got %d", cfg.NginxPort)
	}
	if cfg.NginxProtocol != "https" {
		t.Errorf("expected NginxProtocol 'https', got %s", cfg.NginxProtocol)
	}
	if cfg.NginxTLSMode != "self-signed" {
		t.Errorf("expected NginxTLSMode 'self-signed', got %s", cfg.NginxTLSMode)
	}
}

func TestLoad_NginxHTTPSLetsencrypt(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")
	t.Setenv("NGINX_ENABLED", "true")
	t.Setenv("NGINX_DOMAIN", "videos.example.com")
	t.Setenv("NGINX_PORT", "443")
	t.Setenv("NGINX_PROTOCOL", "https")
	t.Setenv("NGINX_TLS_MODE", "letsencrypt")
	t.Setenv("NGINX_LETSENCRYPT_EMAIL", "admin@example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !cfg.NginxEnabled {
		t.Error("expected NginxEnabled true")
	}
	if cfg.NginxTLSMode != "letsencrypt" {
		t.Errorf("expected NginxTLSMode 'letsencrypt', got %s", cfg.NginxTLSMode)
	}
	if cfg.NginxLetsEncryptEmail != "admin@example.com" {
		t.Errorf("expected NginxLetsEncryptEmail 'admin@example.com', got %s", cfg.NginxLetsEncryptEmail)
	}
}

func TestValidateJWTSecret(t *testing.T) {
	const prodEnvKey = "ENV"

	tests := []struct {
		name        string
		secret      string
		production  bool
		wantErr     bool
		errContains string
	}{
		{
			name:       "non-prod: short secret is accepted",
			secret:     "short",
			production: false,
			wantErr:    false,
		},
		{
			name:       "non-prod: placeholder is accepted",
			secret:     "change-me",
			production: false,
			wantErr:    false,
		},
		{
			name:        "prod: secret shorter than 32 chars is rejected",
			secret:      "tooshort",
			production:  true,
			wantErr:     true,
			errContains: "minimum length is 32 characters",
		},
		{
			name:       "prod: 32-char strong secret is accepted",
			secret:     "a-very-long-and-unguessable-key!",
			production: false,
			wantErr:    false,
		},
		{
			name:        "prod: short placeholder 'change-me' is rejected for length",
			secret:      "change-me",
			production:  true,
			wantErr:     true,
			errContains: "minimum length is 32 characters",
		},
		{
			name:        "prod: canonical placeholder is rejected",
			secret:      "your-super-secret-jwt-key-change-in-production",
			production:  true,
			wantErr:     true,
			errContains: "replace placeholder/default value",
		},
		{
			name:        "prod: secret containing 'change-in-production' is rejected",
			secret:      "my-app-jwt-key-change-in-production-please",
			production:  true,
			wantErr:     true,
			errContains: "replace placeholder/default value",
		},
		{
			name:       "prod: strong random secret is accepted",
			secret:     fmt.Sprintf("strong-random-secret-%s", strings.Repeat("x", 20)),
			production: true,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.production {
				t.Setenv(prodEnvKey, "production")
			} else {
				t.Setenv(prodEnvKey, "development")
			}

			err := validateJWTSecret(tt.secret)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestLoad_IOTADefaults(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.EnableIOTA {
		t.Errorf("expected EnableIOTA default false, got true")
	}
	if cfg.IOTAMode != "docker" {
		t.Errorf("expected IOTAMode default 'docker', got %q", cfg.IOTAMode)
	}
	if cfg.IOTANetwork != "testnet" {
		t.Errorf("expected IOTANetwork default 'testnet', got %q", cfg.IOTANetwork)
	}
}

func TestLoad_IOTAFromEnv(t *testing.T) {
	setMinimumLoadEnv(t, "test-secret")
	t.Setenv("ENABLE_IOTA", "true")
	t.Setenv("IOTA_NODE_URL", "http://my-node:14265")
	t.Setenv("IOTA_MODE", "external")
	t.Setenv("IOTA_NETWORK", "mainnet")
	t.Setenv("IOTA_WALLET_ENCRYPTION_KEY", "test-key-32-chars-long-padding!!")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !cfg.EnableIOTA {
		t.Errorf("expected EnableIOTA true")
	}
	if cfg.IOTANodeURL != "http://my-node:14265" {
		t.Errorf("expected IOTANodeURL 'http://my-node:14265', got %q", cfg.IOTANodeURL)
	}
	if cfg.IOTAMode != "external" {
		t.Errorf("expected IOTAMode 'external', got %q", cfg.IOTAMode)
	}
	if cfg.IOTANetwork != "mainnet" {
		t.Errorf("expected IOTANetwork 'mainnet', got %q", cfg.IOTANetwork)
	}
}
