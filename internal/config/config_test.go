package config

import (
	"encoding/base64"
	"strings"
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

func TestLoggingDefaults(t *testing.T) {
	for _, k := range []string{"LOG_LEVEL", "LOG_FORMAT"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
}

func TestLoggingOverrideAndNormalisation(t *testing.T) {
	t.Setenv("LOG_LEVEL", "DEBUG")
	t.Setenv("LOG_FORMAT", "Text")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug (lowercased)", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want text (lowercased)", cfg.LogFormat)
	}
}

func TestLoggingRejectsInvalidLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "loud")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid LOG_LEVEL, got nil")
	}
}

func TestLoggingRejectsInvalidFormat(t *testing.T) {
	t.Setenv("LOG_FORMAT", "yaml")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid LOG_FORMAT, got nil")
	}
}

func TestOTelDefaults(t *testing.T) {
	for _, k := range []string{"OTEL_ENABLED", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_SERVICE_NAME"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OTelEnabled {
		t.Error("OTelEnabled default should be false")
	}
	if cfg.OTelExporterProtocol != "grpc" {
		t.Errorf("OTelExporterProtocol = %q, want grpc", cfg.OTelExporterProtocol)
	}
	if cfg.OTelServiceName != "vidra-core" {
		t.Errorf("OTelServiceName = %q, want vidra-core", cfg.OTelServiceName)
	}
}

func TestOTelEnabledValid(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.OTelEnabled || cfg.OTelExporterEndpoint != "localhost:4317" {
		t.Errorf("OTel config = %+v, want enabled with endpoint", cfg)
	}
}

func TestOTelEnabledRequiresEndpoint(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for missing OTEL_EXPORTER_OTLP_ENDPOINT, got nil")
	}
}

func TestOTelRejectsBadProtocol(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "carrier-pigeon")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid OTEL_EXPORTER_OTLP_PROTOCOL, got nil")
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
	if cfg.AuthRateLimitRequests != 10 {
		t.Errorf("AuthRateLimitRequests = %d, want 10", cfg.AuthRateLimitRequests)
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
	t.Setenv("STORAGE_BACKEND", "ipfs")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for unsupported STORAGE_BACKEND, got nil")
	}
}

// setS3Env sets a complete, valid s3 storage environment; individual tests
// blank out pieces to probe validation.
func setS3Env(t *testing.T) {
	t.Helper()
	t.Setenv("STORAGE_BACKEND", "s3")
	t.Setenv("STORAGE_S3_ENDPOINT", "minio:9000")
	t.Setenv("STORAGE_S3_BUCKET", "vidra-media")
	t.Setenv("STORAGE_S3_ACCESS_KEY", "test-access")
	t.Setenv("STORAGE_S3_SECRET_KEY", "test-secret")
}

func TestStorageS3ValidConfig(t *testing.T) {
	setS3Env(t)
	t.Setenv("STORAGE_S3_USE_SSL", "false")
	t.Setenv("STORAGE_S3_FORCE_PATH_STYLE", "true")
	t.Setenv("STORAGE_S3_REGION", "us-east-1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.StorageBackend != "s3" {
		t.Errorf("StorageBackend = %q, want s3", cfg.StorageBackend)
	}
	if cfg.StorageS3Endpoint != "minio:9000" || cfg.StorageS3Bucket != "vidra-media" {
		t.Errorf("endpoint/bucket = %q/%q, want minio:9000/vidra-media", cfg.StorageS3Endpoint, cfg.StorageS3Bucket)
	}
	if cfg.StorageS3UseSSL {
		t.Error("StorageS3UseSSL = true, want false (overridden)")
	}
	if !cfg.StorageS3ForcePathStyle {
		t.Error("StorageS3ForcePathStyle = false, want true (overridden)")
	}
	if cfg.StorageS3Region != "us-east-1" {
		t.Errorf("StorageS3Region = %q, want us-east-1", cfg.StorageS3Region)
	}
}

func TestStorageS3SecureDefaults(t *testing.T) {
	setS3Env(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.StorageS3UseSSL {
		t.Error("StorageS3UseSSL default = false, want true (secure default)")
	}
	if cfg.StorageS3ForcePathStyle {
		t.Error("StorageS3ForcePathStyle default = true, want false")
	}
}

func TestStorageS3RequiresSettings(t *testing.T) {
	for _, missing := range []string{"STORAGE_S3_ENDPOINT", "STORAGE_S3_BUCKET", "STORAGE_S3_ACCESS_KEY", "STORAGE_S3_SECRET_KEY"} {
		t.Run(missing, func(t *testing.T) {
			setS3Env(t)
			t.Setenv(missing, "")
			if _, err := Load(); err == nil {
				t.Fatalf("Load() with %s unset expected error, got nil", missing)
			}
		})
	}
}

func TestStorageS3RejectsSchemeInEndpoint(t *testing.T) {
	setS3Env(t)
	t.Setenv("STORAGE_S3_ENDPOINT", "https://minio:9000")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for scheme-bearing STORAGE_S3_ENDPOINT, got nil")
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

func TestImportAllowPrivateURLsDefaultAndOverride(t *testing.T) {
	t.Setenv("HTTP_IMPORT_ALLOW_PRIVATE_URLS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ImportAllowPrivateURLs {
		t.Error("ImportAllowPrivateURLs = true, want false by default (SSRF guard on)")
	}
	t.Setenv("HTTP_IMPORT_ALLOW_PRIVATE_URLS", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() override error = %v", err)
	}
	if !cfg.ImportAllowPrivateURLs {
		t.Error("ImportAllowPrivateURLs = false, want true when overridden")
	}
}

func TestUploadMaxSizeRejectsInvalid(t *testing.T) {
	t.Setenv("UPLOAD_MAX_SIZE", "not-a-size")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid UPLOAD_MAX_SIZE, got nil")
	}
}

func TestInstanceDefaultQuotaBytesDefaultAndOverride(t *testing.T) {
	t.Setenv("INSTANCE_DEFAULT_QUOTA_BYTES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.InstanceDefaultQuotaBytes != 0 {
		t.Errorf("InstanceDefaultQuotaBytes = %d, want 0 (unlimited) by default", cfg.InstanceDefaultQuotaBytes)
	}
	t.Setenv("INSTANCE_DEFAULT_QUOTA_BYTES", "5368709120") // 5 GiB
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() override error = %v", err)
	}
	if cfg.InstanceDefaultQuotaBytes != 5368709120 {
		t.Errorf("InstanceDefaultQuotaBytes = %d, want 5368709120", cfg.InstanceDefaultQuotaBytes)
	}
}

func TestInstanceDefaultQuotaBytesRejectsInvalid(t *testing.T) {
	t.Setenv("INSTANCE_DEFAULT_QUOTA_BYTES", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for non-integer INSTANCE_DEFAULT_QUOTA_BYTES, got nil")
	}
	t.Setenv("INSTANCE_DEFAULT_QUOTA_BYTES", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for negative INSTANCE_DEFAULT_QUOTA_BYTES, got nil")
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

func TestMalwareScanModeDefaultAndOverride(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if cfg.MalwareScanMode != "fail-closed" {
		t.Errorf("default MalwareScanMode = %q, want fail-closed", cfg.MalwareScanMode)
	}
	for _, mode := range []string{"fail-open", "quarantine", "fail-closed"} {
		t.Setenv("MALWARE_SCAN_MODE", mode)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load %q: %v", mode, err)
		}
		if cfg.MalwareScanMode != mode {
			t.Errorf("MalwareScanMode = %q, want %q", cfg.MalwareScanMode, mode)
		}
	}
}

func TestLoadRejectsInvalidMalwareScanMode(t *testing.T) {
	t.Setenv("MALWARE_SCAN_MODE", "yolo")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid MALWARE_SCAN_MODE, got nil")
	}
}

func TestLoadRejectsAV1(t *testing.T) {
	t.Setenv("TRANSCODING_AV1_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for TRANSCODING_AV1_ENABLED=true (deferred), got nil")
	}
}

func TestWhisperDefaultsAndOverride(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if cfg.WhisperEnabled {
		t.Errorf("WhisperEnabled default = true, want false")
	}
	if cfg.WhisperDefaultLanguage != "en" {
		t.Errorf("WhisperDefaultLanguage default = %q, want en", cfg.WhisperDefaultLanguage)
	}

	t.Setenv("WHISPER_ENABLED", "true")
	t.Setenv("WHISPER_ENDPOINT", "http://whisper:8080/")
	t.Setenv("WHISPER_DEFAULT_LANGUAGE", "pt-BR")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load enabled: %v", err)
	}
	if !cfg.WhisperEnabled {
		t.Errorf("WhisperEnabled = false, want true")
	}
	if cfg.WhisperEndpoint != "http://whisper:8080" { // trailing slash trimmed
		t.Errorf("WhisperEndpoint = %q, want http://whisper:8080", cfg.WhisperEndpoint)
	}
	if cfg.WhisperDefaultLanguage != "pt-BR" {
		t.Errorf("WhisperDefaultLanguage = %q, want pt-BR", cfg.WhisperDefaultLanguage)
	}
}

func TestLoadRejectsWhisperEnabledWithoutEndpoint(t *testing.T) {
	t.Setenv("WHISPER_ENABLED", "true")
	t.Setenv("WHISPER_ENDPOINT", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for WHISPER_ENABLED=true without WHISPER_ENDPOINT, got nil")
	}
}

func TestLoadRejectsWhisperNonHTTPEndpoint(t *testing.T) {
	t.Setenv("WHISPER_ENABLED", "true")
	t.Setenv("WHISPER_ENDPOINT", "ftp://whisper:8080")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for a non-http(s) WHISPER_ENDPOINT, got nil")
	}
}

func TestLoadRejectsBadWhisperDefaultLanguage(t *testing.T) {
	t.Setenv("WHISPER_DEFAULT_LANGUAGE", "not a tag!")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for a malformed WHISPER_DEFAULT_LANGUAGE, got nil")
	}
}

func TestVP9DefaultOff(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TranscodingVP9Enabled {
		t.Error("TranscodingVP9Enabled should default false")
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

func TestCookieSecureDerivation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"dev without public base url", Config{Environment: "development"}, false},
		{"dev with http public base url", Config{Environment: "development", PublicBaseURL: "http://localhost:8080"}, false},
		{"dev with https public base url", Config{Environment: "development", PublicBaseURL: "https://videos.example"}, true},
		{"https scheme is case-insensitive", Config{Environment: "development", PublicBaseURL: "HTTPS://videos.example"}, true},
		{"test env plain", Config{Environment: "test"}, false},
		{"production fail-secure without base url", Config{Environment: "production"}, true},
		{"production with https base url", Config{Environment: "production", PublicBaseURL: "https://videos.example"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.CookieSecure(); got != tc.want {
				t.Errorf("CookieSecure() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTranscodingDisabledByDefault(t *testing.T) {
	t.Setenv("TRANSCODING_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TranscodingEnabled {
		t.Error("TranscodingEnabled = true, want false by default")
	}
	t.Setenv("TRANSCODING_ENABLED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.TranscodingEnabled {
		t.Error("TranscodingEnabled = false with TRANSCODING_ENABLED=true")
	}
}

func TestFederationDisabledByDefault(t *testing.T) {
	t.Setenv("FEDERATION_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.FederationEnabled {
		t.Error("FederationEnabled = true, want false by default")
	}
}

func TestFederationEnabledTrimsBaseURL(t *testing.T) {
	t.Setenv("FEDERATION_ENABLED", "true")
	t.Setenv("PUBLIC_BASE_URL", "https://videos.example/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.FederationEnabled {
		t.Error("FederationEnabled = false, want true")
	}
	if cfg.PublicBaseURL != "https://videos.example" {
		t.Errorf("PublicBaseURL = %q, want trailing slash trimmed", cfg.PublicBaseURL)
	}
}

func TestFederationRejectsBadBaseURL(t *testing.T) {
	for _, bad := range []string{"", "https://videos.example/with/path", "ftp://videos.example"} {
		t.Setenv("FEDERATION_ENABLED", "true")
		t.Setenv("PUBLIC_BASE_URL", bad)
		if _, err := Load(); err == nil {
			t.Errorf("Load() with FEDERATION_ENABLED and PUBLIC_BASE_URL=%q: expected error", bad)
		}
	}
}

// prodFedConfig is a fully-valid production config with federation on, varying
// only the KEK — so validate()'s only remaining variable is FEDERATION_KEY_KEK.
func prodFedConfig(kek string) *Config {
	return &Config{
		Environment:        "production",
		LogLevel:           "info",
		LogFormat:          "json",
		HTTPPort:           8080,
		DatabaseURL:        "postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable",
		RedisURL:           "redis://localhost:6379/0",
		HTTPRequestTimeout: time.Second,
		HTTPBodyLimit:      "8M",
		UploadMaxSize:      "64K",
		JWTSecret:          strings.Repeat("x", 40),
		JWTAccessTTL:       time.Minute,
		JWTRefreshTTL:      time.Hour,
		StorageBackend:     "local",
		StorageLocalRoot:   "/tmp/vidra",
		CORSAllowedOrigins: []string{"https://videos.example"},
		FederationEnabled:  true,
		PublicBaseURL:      "https://videos.example",
		FederationKeyKEK:   kek,
	}
}

func TestFederationProductionRequiresKEK(t *testing.T) {
	if err := prodFedConfig("").validate(); err == nil {
		t.Fatal("validate(): production federation without a KEK must error")
	}
	good := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := prodFedConfig(good).validate(); err != nil {
		t.Fatalf("validate(): unexpected error with a valid 32-byte KEK: %v", err)
	}
	if err := prodFedConfig("bad").validate(); err == nil {
		t.Fatal("validate(): a non-32-byte KEK must error")
	}
}

func TestMailDisabledByDefault(t *testing.T) {
	for _, k := range []string{"MAIL_ENABLED", "SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MailEnabled {
		t.Error("MailEnabled = true by default, want false")
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("SMTPPort default = %d, want 587", cfg.SMTPPort)
	}
}

func TestMailEnabledValidConfig(t *testing.T) {
	t.Setenv("MAIL_ENABLED", "true")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "relay-secret")
	t.Setenv("SMTP_FROM", "no-reply@example.test")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.MailEnabled || cfg.SMTPHost != "smtp.example.test" || cfg.SMTPPort != 2525 ||
		cfg.SMTPUsername != "mailer" || cfg.SMTPPassword != "relay-secret" || cfg.SMTPFrom != "no-reply@example.test" {
		t.Errorf("SMTP config not loaded: %+v", cfg)
	}
}

func TestMailEnabledRequiresHostAndFrom(t *testing.T) {
	cases := []struct{ name, host, from string }{
		{"missing host", "", "no-reply@example.test"},
		{"missing from", "smtp.example.test", ""},
		{"from not an address", "smtp.example.test", "not-an-address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAIL_ENABLED", "true")
			t.Setenv("SMTP_HOST", tc.host)
			t.Setenv("SMTP_FROM", tc.from)
			if _, err := Load(); err == nil {
				t.Errorf("Load() accepted MAIL_ENABLED with host=%q from=%q, want error", tc.host, tc.from)
			}
		})
	}
}

func TestMailEnabledRejectsBadPort(t *testing.T) {
	t.Setenv("MAIL_ENABLED", "true")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_FROM", "no-reply@example.test")
	t.Setenv("SMTP_PORT", "70000")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SMTP_PORT") {
		t.Errorf("Load() = %v, want SMTP_PORT range error", err)
	}
}

func TestMFAKEKFallbackAndValidation(t *testing.T) {
	// MFAKEK() prefers MFA_KEY_KEK and falls back to FEDERATION_KEY_KEK.
	c := &Config{MFAKeyKEK: "mfa-kek", FederationKeyKEK: "fed-kek"}
	if got := c.MFAKEK(); got != "mfa-kek" {
		t.Errorf("MFAKEK() = %q, want the dedicated MFA KEK", got)
	}
	c.MFAKeyKEK = ""
	if got := c.MFAKEK(); got != "fed-kek" {
		t.Errorf("MFAKEK() = %q, want the federation fallback", got)
	}
	c.FederationKeyKEK = ""
	if got := c.MFAKEK(); got != "" {
		t.Errorf("MFAKEK() = %q, want empty (dev raw mode)", got)
	}

	// A set MFA_KEY_KEK must be base64 of exactly 32 bytes.
	t.Setenv("MFA_KEY_KEK", "not-a-kek")
	if _, err := Load(); err == nil {
		t.Error("Load() with a malformed MFA_KEY_KEK must error")
	}
	t.Setenv("MFA_KEY_KEK", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := Load(); err != nil {
		t.Errorf("Load() with a valid MFA_KEY_KEK: %v", err)
	}
}

func TestTOTPIssuerDefaultsToInstanceName(t *testing.T) {
	t.Setenv("TOTP_ISSUER", "")
	t.Setenv("INSTANCE_NAME", "My Tube")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.TOTPIssuer != "My Tube" {
		t.Errorf("TOTPIssuer = %q, want the instance name default", cfg.TOTPIssuer)
	}
	t.Setenv("TOTP_ISSUER", "Custom Label")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.TOTPIssuer != "Custom Label" {
		t.Errorf("TOTPIssuer = %q, want the explicit override", cfg.TOTPIssuer)
	}
}
