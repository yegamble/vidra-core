package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/cdn"
	"github.com/vidra/vidra-core/internal/drm"
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

func TestMetricsDefaultAndOverride(t *testing.T) {
	t.Setenv("METRICS_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MetricsEnabled {
		t.Error("MetricsEnabled default should be false")
	}
	t.Setenv("METRICS_ENABLED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.MetricsEnabled {
		t.Error("MetricsEnabled should be true when METRICS_ENABLED=true")
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

// TestProductionRefusesDevelopmentEscapeHatches locks down the two flags
// deploy/README.md already claims production refuses. DEV_MAIL_CAPTURE_ENABLED
// mounts an UNAUTHENTICATED route that hands out a live password-reset token for
// any address (account takeover in one request); HTTP_IMPORT_ALLOW_PRIVATE_URLS
// re-opens the SSRF hole the dial-time guard closes. Both are set true by
// env/qa.env.example and the meta Makefile's e2e target — neither of which sets
// VIDRA_ENV=production — so the refusal must be keyed on the environment, and a
// stray copy-paste into a production env file must be a boot failure, not a
// silent hole.
func TestProductionRefusesDevelopmentEscapeHatches(t *testing.T) {
	prod := func(t *testing.T) {
		t.Helper()
		t.Setenv("VIDRA_ENV", "production")
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("REDIS_URL", "redis://x")
		t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.test")
		t.Setenv("JWT_SECRET", "a-sufficiently-long-production-secret-0001")
	}

	for _, key := range []string{"DEV_MAIL_CAPTURE_ENABLED", "HTTP_IMPORT_ALLOW_PRIVATE_URLS"} {
		t.Run(key, func(t *testing.T) {
			prod(t)
			t.Setenv(key, "true")
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=true in production, want a boot failure", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name %s — the operator has to be told which key to remove", err, key)
			}
		})
	}

	// The same flags outside production keep working: the backed e2e suite runs
	// with VIDRA_ENV unset (development) and depends on both.
	t.Run("development is unaffected", func(t *testing.T) {
		t.Setenv("VIDRA_ENV", "development")
		t.Setenv("DEV_MAIL_CAPTURE_ENABLED", "true")
		t.Setenv("HTTP_IMPORT_ALLOW_PRIVATE_URLS", "true")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() in development: %v", err)
		}
		if !cfg.DevMailCaptureEnabled || !cfg.ImportAllowPrivateURLs {
			t.Error("the development escape hatches must still be settable outside production")
		}
	})
}

// TestOwnerClaimTokenOverride locks down the dev/test-only fixed owner-claim
// token (OWNER_CLAIM_TOKEN): accepted outside production when long enough,
// refused when short, and REFUSED outright in production — a fixed,
// environment-visible admin-bootstrap credential defeats the random mint.
func TestOwnerClaimTokenOverride(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(): %v", err)
		}
		if cfg.OwnerClaimToken != "" {
			t.Errorf("OwnerClaimToken = %q, want empty by default", cfg.OwnerClaimToken)
		}
	})

	t.Run("development accepts 16+ characters", func(t *testing.T) {
		t.Setenv("VIDRA_ENV", "development")
		t.Setenv("OWNER_CLAIM_TOKEN", "fixed-owner-claim-token-0001")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load(): %v", err)
		}
		if cfg.OwnerClaimToken != "fixed-owner-claim-token-0001" {
			t.Errorf("OwnerClaimToken = %q, want the env value verbatim", cfg.OwnerClaimToken)
		}
	})

	t.Run("rejects fewer than 16 characters", func(t *testing.T) {
		t.Setenv("OWNER_CLAIM_TOKEN", "too-short-15chr")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OWNER_CLAIM_TOKEN") {
			t.Fatalf("Load() err = %v, want a boot failure naming OWNER_CLAIM_TOKEN", err)
		}
	})

	t.Run("production refuses it", func(t *testing.T) {
		t.Setenv("VIDRA_ENV", "production")
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("REDIS_URL", "redis://x")
		t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.test")
		t.Setenv("JWT_SECRET", "a-sufficiently-long-production-secret-0001")
		t.Setenv("OWNER_CLAIM_TOKEN", "fixed-owner-claim-token-0001")
		_, err := Load()
		if err == nil {
			t.Fatal("Load() accepted OWNER_CLAIM_TOKEN in production, want a boot failure")
		}
		if !strings.Contains(err.Error(), "OWNER_CLAIM_TOKEN") {
			t.Errorf("error %q does not name OWNER_CLAIM_TOKEN — the operator has to be told which key to remove", err)
		}
	})
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

func TestClamAVTimeoutDefaultAndOverride(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if cfg.ClamAVTimeout != 60*time.Second {
		t.Errorf("default ClamAVTimeout = %s, want 60s", cfg.ClamAVTimeout)
	}
	t.Setenv("CLAMAV_TIMEOUT", "90s")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load override: %v", err)
	}
	if cfg.ClamAVTimeout != 90*time.Second {
		t.Errorf("ClamAVTimeout = %s, want 90s", cfg.ClamAVTimeout)
	}
}

func TestLoadRejectsNonPositiveClamAVTimeoutWhenScanEnabled(t *testing.T) {
	t.Setenv("MALWARE_SCAN_ENABLED", "true")
	t.Setenv("CLAMAV_ADDR", "clamav:3310")
	t.Setenv("CLAMAV_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for non-positive CLAMAV_TIMEOUT with scanning enabled, got nil")
	}
}

// TestLoadVideoCodecDefaults pins the shape of an ordinary install: H.264 only.
// Both extra-codec knobs are off, so the ladder a deployment that says nothing
// about codecs produces is exactly the one it always produced.
func TestLoadVideoCodecDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if cfg.TranscodingHEVCEnabled || cfg.TranscodingAV1Enabled {
		t.Errorf("extra codecs default on (hevc %v, av1 %v); an ordinary install must produce the H.264 ladder unchanged",
			cfg.TranscodingHEVCEnabled, cfg.TranscodingAV1Enabled)
	}
}

// TestLoadAcceptsExtraCodecsOnCMAF: AV1 is no longer a deferred hard failure and
// HEVC is a real knob, on the packager that can carry them.
func TestLoadAcceptsExtraCodecsOnCMAF(t *testing.T) {
	t.Setenv("TRANSCODING_HEVC_ENABLED", "true")
	t.Setenv("TRANSCODING_AV1_ENABLED", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with hevc+av1 on the default packager: %v", err)
	}
	if !cfg.TranscodingHEVCEnabled || !cfg.TranscodingAV1Enabled {
		t.Errorf("codecs = (hevc %v, av1 %v), want both on", cfg.TranscodingHEVCEnabled, cfg.TranscodingAV1Enabled)
	}
}

// TestLoadRejectsExtraCodecsOnMPEGTS pins the CMAF-only rule AND its attribution:
// the error must name the knob the operator has to change, not the packager they
// deliberately rolled back to.
func TestLoadRejectsExtraCodecsOnMPEGTS(t *testing.T) {
	for _, tc := range []struct{ env, other string }{
		{"TRANSCODING_HEVC_ENABLED", "TRANSCODING_AV1_ENABLED"},
		{"TRANSCODING_AV1_ENABLED", "TRANSCODING_HEVC_ENABLED"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("TRANSCODING_PACKAGER", "ts")
			t.Setenv(tc.env, "true")
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=true with the MPEG-TS packager", tc.env)
			}
			got := collectVarErrors(err)
			if got[tc.env] == nil {
				t.Fatalf("error is not attributed to %s; error tree = %v", tc.env, err)
			}
			if got[tc.other] != nil {
				t.Errorf("error blames %s, which is off: %v", tc.other, got[tc.other])
			}
			if !strings.Contains(got[tc.env].Error(), "TRANSCODING_PACKAGER") {
				t.Errorf("message %q does not tell the operator which packager setting conflicts", got[tc.env])
			}
		})
	}
}

// TestLoadIPFSDefaults asserts the hybrid IPFS mirror (P19) is inert by default:
// disabled, with the documented worker defaults, so existing deploys are
// unaffected and Validate() passes with no IPFS_* env set.
func TestLoadIPFSDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if cfg.IPFSEnabled {
		t.Errorf("IPFSEnabled default = true, want false")
	}
	if cfg.IPFSMirrorPrivate {
		t.Errorf("IPFSMirrorPrivate default = true, want false")
	}
	if cfg.IPFSAddTimeout != 60*time.Second {
		t.Errorf("IPFSAddTimeout default = %v, want 60s", cfg.IPFSAddTimeout)
	}
	if cfg.IPFSPinConcurrency != 2 {
		t.Errorf("IPFSPinConcurrency default = %d, want 2", cfg.IPFSPinConcurrency)
	}
	if cfg.IPFSReconcileInterval != 5*time.Minute {
		t.Errorf("IPFSReconcileInterval default = %v, want 5m", cfg.IPFSReconcileInterval)
	}
	// Private tier (P19.P1) is inert by default: no URLs, tunables INHERIT the public
	// defaults so a later opt-in never lands a zero timeout/concurrency.
	if cfg.IPFSPrivateAPIURL != "" || cfg.IPFSPrivateClusterAPIURL != "" || cfg.IPFSPrivateClusterToken != "" {
		t.Errorf("private IPFS URLs/token default non-empty: %q %q (token set=%v)",
			cfg.IPFSPrivateAPIURL, cfg.IPFSPrivateClusterAPIURL, cfg.IPFSPrivateClusterToken != "")
	}
	if cfg.IPFSPrivateAddTimeout != 60*time.Second {
		t.Errorf("IPFSPrivateAddTimeout default = %v, want inherited 60s", cfg.IPFSPrivateAddTimeout)
	}
	if cfg.IPFSPrivatePinConcurrency != 2 {
		t.Errorf("IPFSPrivatePinConcurrency default = %d, want inherited 2", cfg.IPFSPrivatePinConcurrency)
	}
}

// TestLoadIPFSPrivateInheritsTunables asserts the private worker tunables inherit
// the public values when unset, and can be overridden independently.
func TestLoadIPFSPrivateInheritsTunables(t *testing.T) {
	t.Setenv("IPFS_ADD_TIMEOUT", "30s")
	t.Setenv("IPFS_PIN_CONCURRENCY", "5")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.IPFSPrivateAddTimeout != 30*time.Second {
		t.Errorf("private add timeout = %v, want inherited 30s", cfg.IPFSPrivateAddTimeout)
	}
	if cfg.IPFSPrivatePinConcurrency != 5 {
		t.Errorf("private concurrency = %d, want inherited 5", cfg.IPFSPrivatePinConcurrency)
	}
	t.Setenv("IPFS_PRIVATE_ADD_TIMEOUT", "90s")
	t.Setenv("IPFS_PRIVATE_PIN_CONCURRENCY", "1")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load override: %v", err)
	}
	if cfg.IPFSPrivateAddTimeout != 90*time.Second {
		t.Errorf("private add timeout override = %v, want 90s", cfg.IPFSPrivateAddTimeout)
	}
	if cfg.IPFSPrivatePinConcurrency != 1 {
		t.Errorf("private concurrency override = %d, want 1", cfg.IPFSPrivatePinConcurrency)
	}
}

// TestLoadIPFSEnabledValid checks the happy path: enabled with both required URLs
// loads and the values round-trip (trailing slashes trimmed).
func TestLoadIPFSEnabledValid(t *testing.T) {
	t.Setenv("IPFS_ENABLED", "true")
	t.Setenv("IPFS_API_URL", "http://ipfs:5001/")
	t.Setenv("IPFS_GATEWAY_URL", "https://ipfs.example.org/")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load enabled: %v", err)
	}
	if !cfg.IPFSEnabled {
		t.Fatal("IPFSEnabled = false, want true")
	}
	if cfg.IPFSAPIURL != "http://ipfs:5001" {
		t.Errorf("IPFSAPIURL = %q, want trimmed http://ipfs:5001", cfg.IPFSAPIURL)
	}
	if cfg.IPFSGatewayURL != "https://ipfs.example.org" {
		t.Errorf("IPFSGatewayURL = %q, want trimmed https://ipfs.example.org", cfg.IPFSGatewayURL)
	}
}

// TestLoadIPFSValidation is the config validation table, incl. the privacy guard
// (mirrors the TestLoadRejectsAV1 defer style): IPFS_MIRROR_PRIVATE=true without a
// private cluster URL is a hard error — private media must never reach a public
// network.
func TestLoadIPFSValidation(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "enabled without urls errors",
			env:     map[string]string{"IPFS_ENABLED": "true"},
			wantErr: true,
		},
		{
			name:    "enabled missing gateway errors",
			env:     map[string]string{"IPFS_ENABLED": "true", "IPFS_API_URL": "http://ipfs:5001"},
			wantErr: true,
		},
		{
			name:    "enabled bad api scheme errors",
			env:     map[string]string{"IPFS_ENABLED": "true", "IPFS_API_URL": "ftp://ipfs:5001", "IPFS_GATEWAY_URL": "https://gw.example.org"},
			wantErr: true,
		},
		{
			// SUPERSESSION (P19.P1): mirror-private now requires a DEDICATED
			// IPFS_PRIVATE_API_URL — a cluster URL no longer satisfies it.
			name:    "private guard: mirror private without private api url errors",
			env:     map[string]string{"IPFS_MIRROR_PRIVATE": "true"},
			wantErr: true,
		},
		{
			name:    "private guard fires even when public mirror disabled",
			env:     map[string]string{"IPFS_ENABLED": "false", "IPFS_MIRROR_PRIVATE": "true"},
			wantErr: true,
		},
		{
			// A cluster URL alone is NOT sufficient anymore (the old guard is replaced).
			name: "private guard: cluster url without private node still errors",
			env: map[string]string{
				"IPFS_ENABLED":         "true",
				"IPFS_API_URL":         "http://ipfs:5001",
				"IPFS_GATEWAY_URL":     "https://gw.example.org",
				"IPFS_MIRROR_PRIVATE":  "true",
				"IPFS_CLUSTER_API_URL": "http://cluster:9094",
			},
			wantErr: true,
		},
		{
			name: "mirror private with a dedicated private node is allowed",
			env: map[string]string{
				"IPFS_ENABLED":         "true",
				"IPFS_API_URL":         "http://ipfs:5001",
				"IPFS_GATEWAY_URL":     "https://gw.example.org",
				"IPFS_MIRROR_PRIVATE":  "true",
				"IPFS_PRIVATE_API_URL": "http://ipfs-private:5001",
			},
			wantErr: false,
		},
		{
			// Refusing to dual-home: the private node must differ from the public node.
			name: "dual-home rejected (private api url == public api url)",
			env: map[string]string{
				"IPFS_ENABLED":         "true",
				"IPFS_API_URL":         "http://ipfs:5001",
				"IPFS_GATEWAY_URL":     "https://gw.example.org",
				"IPFS_MIRROR_PRIVATE":  "true",
				"IPFS_PRIVATE_API_URL": "http://ipfs:5001",
			},
			wantErr: true,
		},
		{
			// Trailing-slash normalization must not let a dual-home sneak past.
			name: "dual-home rejected despite trailing slash",
			env: map[string]string{
				"IPFS_ENABLED":         "true",
				"IPFS_API_URL":         "http://ipfs:5001",
				"IPFS_GATEWAY_URL":     "https://gw.example.org",
				"IPFS_MIRROR_PRIVATE":  "true",
				"IPFS_PRIVATE_API_URL": "http://ipfs:5001/",
			},
			wantErr: true,
		},
		{
			name: "mirror private bad private api scheme errors",
			env: map[string]string{
				"IPFS_ENABLED":         "true",
				"IPFS_API_URL":         "http://ipfs:5001",
				"IPFS_GATEWAY_URL":     "https://gw.example.org",
				"IPFS_MIRROR_PRIVATE":  "true",
				"IPFS_PRIVATE_API_URL": "ftp://ipfs-private:5001",
			},
			wantErr: true,
		},
		{
			// Private-only tier: the operator replicates ONLY non-public media with the
			// public mirror off. Spec §2 explicitly allows this.
			name: "private-only tier allowed (public mirror disabled)",
			env: map[string]string{
				"IPFS_ENABLED":         "false",
				"IPFS_MIRROR_PRIVATE":  "true",
				"IPFS_PRIVATE_API_URL": "http://ipfs-private:5001",
			},
			wantErr: false,
		},
		{
			name: "mirror private with private node + private cluster ok",
			env: map[string]string{
				"IPFS_ENABLED":                 "true",
				"IPFS_API_URL":                 "http://ipfs:5001",
				"IPFS_GATEWAY_URL":             "https://gw.example.org",
				"IPFS_MIRROR_PRIVATE":          "true",
				"IPFS_PRIVATE_API_URL":         "http://ipfs-private:5001",
				"IPFS_PRIVATE_CLUSTER_API_URL": "http://cluster-private:9094",
			},
			wantErr: false,
		},
		{
			name: "enabled with valid urls ok",
			env: map[string]string{
				"IPFS_ENABLED":     "true",
				"IPFS_API_URL":     "http://ipfs:5001",
				"IPFS_GATEWAY_URL": "https://gw.example.org",
			},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tc.wantErr && err == nil {
				t.Fatalf("Load() = nil error, want error for %s", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Load() = %v, want nil error for %s", err, tc.name)
			}
		})
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
		// The consented plain-http deployment (VIDRA_TLS_MODE=plain-http). Secure
		// must come OFF: the browser would never send a Secure cookie back over
		// http, so leaving it on is a production instance nobody can log into.
		{"production with consented http base url", Config{Environment: "production", PublicBaseURL: "http://videos.internal", AllowPlainHTTP: true}, false},
		// The consent flag is about what validate() ACCEPTS, not about what the
		// scheme means: an https origin stays Secure whether or not somebody also
		// allowed plain http.
		{"https base url with plain http allowed", Config{Environment: "production", PublicBaseURL: "https://videos.example", AllowPlainHTTP: true}, true},
		// Development is unchanged, with or without the consent flag: the scheme
		// already answered the question.
		{"dev http without consent", Config{Environment: "development", PublicBaseURL: "http://localhost:8080"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.CookieSecure(); got != tc.want {
				t.Errorf("CookieSecure() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTranscodingEnabledByDefault(t *testing.T) {
	// Default ON: an unset TRANSCODING_ENABLED must leave the HLS ladder
	// pipeline enabled, so a stock upload produces the multi-rendition ladder
	// the player's quality selector needs (a missing ffmpeg still degrades
	// gracefully at boot — that is DetectHLSTranscoder's job, not config's).
	t.Setenv("TRANSCODING_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.TranscodingEnabled {
		t.Error("TranscodingEnabled = false, want true by default")
	}
	// Explicit opt-out still works.
	t.Setenv("TRANSCODING_ENABLED", "false")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TranscodingEnabled {
		t.Error("TranscodingEnabled = true with TRANSCODING_ENABLED=false")
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
		Environment: "production",
		// Stated explicitly because this Config never went through LoadFrom, which
		// is what applies the "all" default. validate() rejects an unrecognised
		// role, and the zero value is one.
		Role:      RoleAll,
		LogLevel:  "info",
		LogFormat: "json",
		HTTPPort:  8080,
		// Same reason as Role: the pool sizing has a validated floor (DB_MAX_CONNS
		// >= 2, for the leader elector's pinned connection) and the zero value is
		// below it, so a literal Config has to state what LoadFrom would default.
		DBMaxConns:               DefaultDBMaxConns,
		DBMinConns:               DefaultDBMinConns,
		DBConnMaxLifetime:        DefaultDBConnMaxLifetime,
		DBConnMaxIdleTime:        DefaultDBConnMaxIdleTime,
		DatabaseURL:              "postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable",
		RedisURL:                 "redis://localhost:6379/0",
		HTTPRequestTimeout:       time.Second,
		HTTPStreamRequestTimeout: time.Hour,
		HTTPBodyLimit:            "8M",
		UploadMaxSize:            "64K",
		JWTSecret:                strings.Repeat("x", 40),
		JWTAccessTTL:             time.Minute,
		JWTRefreshTTL:            time.Hour,
		StorageBackend:           "local",
		StorageLocalRoot:         "/tmp/vidra",
		CORSAllowedOrigins:       []string{"https://videos.example"},
		FederationEnabled:        true,
		PublicBaseURL:            "https://videos.example",
		FederationKeyKEK:         kek,
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

func TestATProtoDefaults(t *testing.T) {
	for _, k := range []string{"ATPROTO_ENABLED", "ATPROTO_KEY_KEK", "FEDERATION_KEY_KEK"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.ATProtoEnabled {
		t.Errorf("ATProtoEnabled default = true, want false")
	}
	if cfg.ATProtoKEK() != "" {
		t.Errorf("ATProtoKEK() default = %q, want empty", cfg.ATProtoKEK())
	}
}

func TestATProtoLoginEnabledToggle(t *testing.T) {
	// Defaults off, independent of the cross-posting switch.
	t.Setenv("ATPROTO_LOGIN_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.ATProtoLoginEnabled {
		t.Errorf("ATProtoLoginEnabled default = true, want false")
	}

	t.Setenv("ATPROTO_LOGIN_ENABLED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !cfg.ATProtoLoginEnabled {
		t.Errorf("ATProtoLoginEnabled = false, want true when ATPROTO_LOGIN_ENABLED=true")
	}
}

func TestATProtoKEKFallsBackToFederationKEK(t *testing.T) {
	fedKEK := base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("ATPROTO_KEY_KEK", "")
	t.Setenv("FEDERATION_KEY_KEK", fedKEK)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.ATProtoKEK() != fedKEK {
		t.Errorf("ATProtoKEK() = %q, want the federation KEK fallback", cfg.ATProtoKEK())
	}

	// An explicit ATProto KEK wins over the federation fallback.
	own := base64.StdEncoding.EncodeToString(bytesRepeat(1, 32))
	t.Setenv("ATPROTO_KEY_KEK", own)
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.ATProtoKEK() != own {
		t.Errorf("ATProtoKEK() = %q, want the explicit ATProto KEK", cfg.ATProtoKEK())
	}
}

func TestATProtoRejectsBadKEK(t *testing.T) {
	t.Setenv("ATPROTO_KEY_KEK", "not-base64-of-32-bytes")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a malformed ATPROTO_KEY_KEK")
	}
}

func TestATProtoProductionRequiresKEKWhenEnabled(t *testing.T) {
	t.Setenv("VIDRA_ENV", "production")
	t.Setenv("JWT_SECRET", "a-strong-production-secret-32bytes-long")
	t.Setenv("ATPROTO_ENABLED", "true")
	t.Setenv("ATPROTO_KEY_KEK", "")
	t.Setenv("FEDERATION_KEY_KEK", "")
	if _, err := Load(); err == nil {
		t.Fatal("production + ATPROTO_ENABLED without a KEK must fail validation")
	}

	// Supplying a valid KEK unblocks it.
	t.Setenv("ATPROTO_KEY_KEK", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := Load(); err != nil {
		t.Fatalf("production ATProto with a KEK failed: %v", err)
	}
}

// TestFederationATProtoEnableMatrix asserts ActivityPub and ATProto enable
// independently: all four on/off combinations boot (config loads) cleanly.
func TestFederationATProtoEnableMatrix(t *testing.T) {
	kek := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, fed := range []bool{false, true} {
		for _, at := range []bool{false, true} {
			name := fmt.Sprintf("fed=%v_atproto=%v", fed, at)
			t.Run(name, func(t *testing.T) {
				t.Setenv("VIDRA_ENV", "development")
				t.Setenv("PUBLIC_BASE_URL", "https://videos.example")
				t.Setenv("FEDERATION_ENABLED", strconv.FormatBool(fed))
				t.Setenv("ATPROTO_ENABLED", strconv.FormatBool(at))
				t.Setenv("FEDERATION_KEY_KEK", kek)
				t.Setenv("ATPROTO_KEY_KEK", kek)
				cfg, err := Load()
				if err != nil {
					t.Fatalf("combo %s failed to boot: %v", name, err)
				}
				if cfg.FederationEnabled != fed {
					t.Errorf("FederationEnabled = %v, want %v", cfg.FederationEnabled, fed)
				}
				if cfg.ATProtoEnabled != at {
					t.Errorf("ATProtoEnabled = %v, want %v", cfg.ATProtoEnabled, at)
				}
			})
		}
	}
}

// bytesRepeat returns a byte slice of n copies of b (test helper).
func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestPeerTubeImportConfig(t *testing.T) {
	clean := func(t *testing.T) {
		for _, k := range []string{
			"VIDRA_ENV", "DATABASE_URL", "REDIS_URL", "CORS_ALLOWED_ORIGINS",
			"PEERTUBE_IMPORT_ENABLED", "PEERTUBE_SOURCE_DATABASE_URL",
			"PEERTUBE_IMPORT_CONFLICT_POLICY", "PEERTUBE_IMPORT_MEDIA_MODE",
			"PEERTUBE_SOURCE_STORAGE_BACKEND",
		} {
			t.Setenv(k, "")
		}
	}

	t.Run("off by default", func(t *testing.T) {
		clean(t)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if cfg.PeerTubeImportEnabled {
			t.Error("import must be off by default")
		}
		if cfg.PeerTubeImportConflictPolicy != "skip" {
			t.Errorf("default policy = %q, want skip", cfg.PeerTubeImportConflictPolicy)
		}
		if cfg.PeerTubeImportMediaMode != "copy" {
			t.Errorf("default media mode = %q, want copy", cfg.PeerTubeImportMediaMode)
		}
		if cfg.PeerTubeImportConfigured() {
			t.Error("must not be configured by default")
		}
	})

	t.Run("invalid conflict policy is rejected", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_IMPORT_CONFLICT_POLICY", "bogus")
		if _, err := Load(); err == nil {
			t.Error("expected error for invalid conflict policy")
		}
	})

	t.Run("enabled requires a source DSN", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_IMPORT_ENABLED", "true")
		if _, err := Load(); err == nil {
			t.Error("enabling import without a source DSN must error")
		}
	})

	t.Run("invalid media mode is rejected", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_IMPORT_MEDIA_MODE", "bogus")
		if _, err := Load(); err == nil {
			t.Error("expected error for invalid media mode")
		}
	})

	t.Run("enabled with a source DSN is configured", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_IMPORT_ENABLED", "true")
		t.Setenv("PEERTUBE_SOURCE_DATABASE_URL", "postgres://ro:pw@oldhost:5432/peertube?sslmode=disable")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if !cfg.PeerTubeImportConfigured() {
			t.Error("must be configured when enabled with a source DSN")
		}
	})

	// The DSN's shape is checked so `vidra setup` can reject an answer at the
	// prompt with the api's own rule rather than a friendlier second opinion.
	t.Run("enabled with a DSN that is not one is rejected", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_IMPORT_ENABLED", "true")
		t.Setenv("PEERTUBE_SOURCE_DATABASE_URL", "peertube-db.internal")
		if _, err := Load(); err == nil {
			t.Error("a value that is not a connection string was accepted")
		}
	})

	// …and ONLY inside the enabled guard. A half-written DSN left in an env file
	// by a migration that finished last month must never become a boot failure a
	// year later, on a deployment that has no import surface at all.
	t.Run("a disabled import never fails on the source DSN", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_SOURCE_DATABASE_URL", "peertube-db.internal")
		if _, err := Load(); err != nil {
			t.Errorf("a disabled import refused to boot over its leftover source: %v", err)
		}
	})

	// The keyword/value dialect pgx also accepts. An operator whose read-only
	// replica is addressed that way has a working DSN.
	t.Run("the keyword/value DSN dialect is accepted", func(t *testing.T) {
		clean(t)
		t.Setenv("PEERTUBE_IMPORT_ENABLED", "true")
		t.Setenv("PEERTUBE_SOURCE_DATABASE_URL", "host=oldhost user=ro dbname=peertube sslmode=require")
		if _, err := Load(); err != nil {
			t.Errorf("a keyword/value DSN was refused: %v", err)
		}
	})
}

func TestYtdlpImportConfig(t *testing.T) {
	clean := func(t *testing.T) {
		for _, k := range []string{
			"VIDRA_ENV", "DATABASE_URL", "REDIS_URL", "CORS_ALLOWED_ORIGINS",
			"YTDLP_IMPORT_ENABLED", "YTDLP_PATH", "YTDLP_TIMEOUT", "YTDLP_PROXY",
			"YTDLP_MAX_HEIGHT",
		} {
			t.Setenv(k, "")
		}
	}

	t.Run("off by default with sane defaults", func(t *testing.T) {
		clean(t)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if cfg.YtdlpImportEnabled {
			t.Error("yt-dlp import must be OFF by default")
		}
		if cfg.YtdlpPath != "yt-dlp" {
			t.Errorf("default YtdlpPath = %q, want yt-dlp", cfg.YtdlpPath)
		}
		if cfg.YtdlpTimeout != 15*time.Minute {
			t.Errorf("default YtdlpTimeout = %v, want 15m", cfg.YtdlpTimeout)
		}
		if cfg.YtdlpMaxHeight != 1080 {
			t.Errorf("default YtdlpMaxHeight = %d, want 1080", cfg.YtdlpMaxHeight)
		}
	})

	t.Run("enabled with defaults validates", func(t *testing.T) {
		clean(t)
		t.Setenv("YTDLP_IMPORT_ENABLED", "true")
		if _, err := Load(); err != nil {
			t.Fatalf("enabled with defaults should validate: %v", err)
		}
	})

	t.Run("enabled with empty path is rejected", func(t *testing.T) {
		clean(t)
		t.Setenv("YTDLP_IMPORT_ENABLED", "true")
		t.Setenv("YTDLP_PATH", " ")
		if _, err := Load(); err == nil {
			t.Error("enabled with a blank YTDLP_PATH must error")
		}
	})

	t.Run("enabled with non-positive timeout is rejected", func(t *testing.T) {
		clean(t)
		t.Setenv("YTDLP_IMPORT_ENABLED", "true")
		t.Setenv("YTDLP_TIMEOUT", "0s")
		if _, err := Load(); err == nil {
			t.Error("enabled with a zero YTDLP_TIMEOUT must error")
		}
	})

	t.Run("enabled with a bogus proxy scheme is rejected", func(t *testing.T) {
		clean(t)
		t.Setenv("YTDLP_IMPORT_ENABLED", "true")
		t.Setenv("YTDLP_PROXY", "ftp://nope")
		if _, err := Load(); err == nil {
			t.Error("a non-http(s)/socks proxy must error")
		}
	})

	t.Run("enabled with an http proxy validates", func(t *testing.T) {
		clean(t)
		t.Setenv("YTDLP_IMPORT_ENABLED", "true")
		t.Setenv("YTDLP_PROXY", "http://egress.internal:3128")
		if _, err := Load(); err != nil {
			t.Fatalf("http proxy should validate: %v", err)
		}
	})

	t.Run("out-of-range max height is rejected even when disabled", func(t *testing.T) {
		clean(t)
		t.Setenv("YTDLP_MAX_HEIGHT", "10000")
		if _, err := Load(); err == nil {
			t.Error("an absurd YTDLP_MAX_HEIGHT must error")
		}
	})
}

// Malformed (non-empty, unparseable) typed env values must be FATAL at load —
// env files may be generated, and a typo must never boot with a silently
// substituted default. Empty still means "use the default" (KEY= == unset).
func TestLoadRejectsMalformedTypedEnv(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"bool typo", "REGISTRATION_ENABLED", "ture"},
		{"bool yes-word", "OTEL_ENABLED", "yes"},
		{"bool trailing junk", "FEDERATION_ENABLED", "false "},
		{"duration missing unit", "HTTP_READ_TIMEOUT", "15"},
		{"duration word", "RATE_LIMIT_WINDOW", "soon"},
		{"duration bad unit", "JWT_ACCESS_TTL", "15 minutes"},
		{"int word", "HTTP_PORT", "eighty"},
		{"int trailing junk", "SMTP_PORT", "587x"},
		{"int float", "IPFS_PIN_CONCURRENCY", "2.5"},
		{"int64 size suffix", "INSTANCE_DEFAULT_QUOTA_BYTES", "5G"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() expected error for %s=%q, got nil", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q should name the offending variable %s", err, tc.key)
			}
		})
	}
}

func TestLoadReportsAllMalformedTypedEnv(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "definitely")
	t.Setenv("HTTP_READ_TIMEOUT", "fast")
	t.Setenv("HTTP_PORT", "eighty-eighty")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for three malformed variables, got nil")
	}
	for _, key := range []string{"OTEL_ENABLED", "HTTP_READ_TIMEOUT", "HTTP_PORT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error should report every malformed variable; %s missing from %q", key, err)
		}
	}
}

func TestLoadEmptyTypedEnvMeansUnset(t *testing.T) {
	// KEY= in a generated env file is the same as omitting KEY entirely.
	t.Setenv("REGISTRATION_ENABLED", "")
	t.Setenv("HTTP_READ_TIMEOUT", "")
	t.Setenv("HTTP_PORT", "")
	t.Setenv("INSTANCE_DEFAULT_QUOTA_BYTES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.RegistrationEnabled {
		t.Error("RegistrationEnabled should default to true when empty")
	}
	if cfg.HTTPReadTimeout != 15*time.Second {
		t.Errorf("HTTPReadTimeout = %v, want 15s default", cfg.HTTPReadTimeout)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080 default", cfg.HTTPPort)
	}
	if cfg.InstanceDefaultQuotaBytes != 0 {
		t.Errorf("InstanceDefaultQuotaBytes = %d, want 0 default", cfg.InstanceDefaultQuotaBytes)
	}
}

// ---------------------------------------------------------------------------
// CDN delivery (phase-4 delivery item 2)
// ---------------------------------------------------------------------------

// TestLoadCDNDeliveryDefaults: the whole surface is inert unless an operator
// opts in. No base URL means no CDN source is ever built, which is the shipped
// behaviour every existing install keeps across this change.
func TestLoadCDNDeliveryDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if cfg.DeliveryCDNBaseURL != "" || cfg.DeliveryCDNPurgeURL != "" ||
		cfg.DeliveryCDNPurgeHeader != "" || cfg.DeliveryCDNPurgeToken != "" ||
		cfg.DeliveryCDNPurgeMethod != "" {
		t.Errorf("CDN delivery is not inert by default: %+v", cfg.DeliveryCDNBaseURL)
	}
	if cfg.DeliveryCDNPurgeTimeout != defaultCDNPurgeTimeout {
		t.Errorf("DELIVERY_CDN_PURGE_TIMEOUT default = %v, want %v",
			cfg.DeliveryCDNPurgeTimeout, defaultCDNPurgeTimeout)
	}
}

// TestLoadCDNBaseURLIsNormalised: a trailing slash must not survive into the
// edge URL, which is built as base + "/" + objectKey.
func TestLoadCDNBaseURLIsNormalised(t *testing.T) {
	t.Setenv("DELIVERY_CDN_BASE_URL", "  https://cdn.example.com/media/  ")
	t.Setenv("DELIVERY_CDN_PURGE_METHOD", " post ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeliveryCDNBaseURL != "https://cdn.example.com/media" {
		t.Errorf("base URL = %q, want the trimmed, unslashed form", cfg.DeliveryCDNBaseURL)
	}
	if cfg.DeliveryCDNPurgeMethod != "POST" {
		t.Errorf("purge method = %q, want POST — a hand-edited env file's case is not a config error", cfg.DeliveryCDNPurgeMethod)
	}
}

// TestLoadCDNValidation. Every failing row is a way to configure a CDN that
// LOOKS wired and silently is not: the delivery resolver fails open, so a
// misconfigured edge degrades quietly to origin serving. Quiet is right at
// request time and wrong at boot, so boot is where it is loud.
func TestLoadCDNValidation(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "no cdn at all",
			env:     map[string]string{},
			wantErr: false,
		},
		{
			name:    "base url alone is a complete configuration",
			env:     map[string]string{"DELIVERY_CDN_BASE_URL": "https://cdn.example.com"},
			wantErr: false,
		},
		{
			name: "base url with a path prefix",
			env:  map[string]string{"DELIVERY_CDN_BASE_URL": "https://cdn.example.com/media"},
		},
		{
			name:    "base url with no scheme",
			env:     map[string]string{"DELIVERY_CDN_BASE_URL": "cdn.example.com"},
			wantErr: true,
		},
		{
			name:    "origin-relative base url has nothing to redirect to",
			env:     map[string]string{"DELIVERY_CDN_BASE_URL": "/media"},
			wantErr: true,
		},
		{
			name:    "non-http scheme",
			env:     map[string]string{"DELIVERY_CDN_BASE_URL": "ftp://cdn.example.com"},
			wantErr: true,
		},
		{
			// The object key is appended to the PATH, so a query string in the
			// base silently ends up before it.
			name:    "base url carrying a query string",
			env:     map[string]string{"DELIVERY_CDN_BASE_URL": "https://cdn.example.com?sig=x"},
			wantErr: true,
		},
		{
			// The "I configured the CDN and it does nothing" bug: the operator
			// filled in the purge half and no source was ever built.
			name:    "purge url without a base url",
			env:     map[string]string{"DELIVERY_CDN_PURGE_URL": "https://api.example/purge/{url}"},
			wantErr: true,
		},
		{
			name:    "purge token without a base url",
			env:     map[string]string{"DELIVERY_CDN_PURGE_TOKEN": "tok"},
			wantErr: true,
		},
		{
			// A credential configured with nowhere to send it.
			name: "purge token without a purge url",
			env: map[string]string{
				"DELIVERY_CDN_BASE_URL":    "https://cdn.example.com",
				"DELIVERY_CDN_PURGE_TOKEN": "tok",
			},
			wantErr: true,
		},
		{
			name: "purge url template with placeholders is valid",
			env: map[string]string{
				"DELIVERY_CDN_BASE_URL":  "https://cdn.example.com",
				"DELIVERY_CDN_PURGE_URL": "https://api.example/purge?url={url_encoded}",
			},
		},
		{
			name: "the whole template can be {url}",
			env: map[string]string{
				"DELIVERY_CDN_BASE_URL":  "https://cdn.example.com",
				"DELIVERY_CDN_PURGE_URL": "{url}",
			},
		},
		{
			name: "relative purge url",
			env: map[string]string{
				"DELIVERY_CDN_BASE_URL":  "https://cdn.example.com",
				"DELIVERY_CDN_PURGE_URL": "/purge/{key}",
			},
			wantErr: true,
		},
		{
			// A method with a space in it produces a request line the edge
			// answers 400 to, once, in ITS logs.
			name: "purge method that is not a bare token",
			env: map[string]string{
				"DELIVERY_CDN_BASE_URL":     "https://cdn.example.com",
				"DELIVERY_CDN_PURGE_URL":    "{url}",
				"DELIVERY_CDN_PURGE_METHOD": "POST /purge",
			},
			wantErr: true,
		},
		{
			name: "zero purge timeout",
			env: map[string]string{
				"DELIVERY_CDN_BASE_URL":      "https://cdn.example.com",
				"DELIVERY_CDN_PURGE_TIMEOUT": "0s",
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tc.wantErr && err == nil {
				t.Fatalf("Load() = nil error, want error for %s", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Load() = %v, want nil error for %s", err, tc.name)
			}
		})
	}
}

// TestCDNPlainHTTPEdgeRefusedInProduction. Mixed content is invisible from the
// server: a browser on an https page blocks http media before the request ever
// leaves it, so this instance sees no request, no error and no log line. The
// only place it can be caught is boot.
func TestCDNPlainHTTPEdgeRefusedInProduction(t *testing.T) {
	base := map[string]string{
		"VIDRA_ENV":             "production",
		"DATABASE_URL":          "postgres://u:p@db:5432/vidra?sslmode=disable",
		"REDIS_URL":             "redis://redis:6379/0",
		"JWT_SECRET":            strings.Repeat("k", 48),
		"PUBLIC_BASE_URL":       "https://vidra.example.com",
		"DELIVERY_CDN_BASE_URL": "http://cdn.example.com",
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted a plain-http CDN edge in production")
	}
	if !strings.Contains(err.Error(), "DELIVERY_CDN_BASE_URL") {
		t.Fatalf("error does not name the variable: %v", err)
	}

	// The consented lab/LAN install: the same escape hatch PUBLIC_BASE_URL has.
	t.Setenv("VIDRA_ALLOW_PLAIN_HTTP", "true")
	if _, err := Load(); err != nil {
		t.Fatalf("Load with VIDRA_ALLOW_PLAIN_HTTP = %v, want the plain-http edge accepted", err)
	}
}

// TestLoadDRMValidation. Every failing row is a way to end up with an instance
// whose operator believes content is protected while it is not, or one that
// would write content keys unsealed. Both are silent at request time, so boot is
// where they are loud.
func TestLoadDRMValidation(t *testing.T) {
	kek := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("d", 32)))
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name: "no drm at all is the shipped configuration",
			env:  map[string]string{},
		},
		{
			name: "explicit none",
			env:  map[string]string{"DRM_PROVIDER": "none"},
		},
		{
			// An explicitly empty value is the same as unset everywhere else in
			// this file; it must not become an unknown provider.
			name: "empty is none",
			env:  map[string]string{"DRM_PROVIDER": ""},
		},
		{
			name: "clearkey-test with a KEK",
			env:  map[string]string{"DRM_PROVIDER": "clearkey-test", "DRM_KEY_KEK": kek},
		},
		{
			name: "case and whitespace are normalised, not rejected",
			env:  map[string]string{"DRM_PROVIDER": "  ClearKey-Test  ", "DRM_KEY_KEK": kek},
		},
		{
			// The failure the closed set exists for: falling through to "none"
			// would serve unprotected media on an instance configured for
			// protection, and nothing would ever say so.
			name:    "a misspelled provider must not fall back to none",
			env:     map[string]string{"DRM_PROVIDER": "clearkey", "DRM_KEY_KEK": kek},
			wantErr: true,
		},
		{
			name:    "a provider not built into this binary",
			env:     map[string]string{"DRM_PROVIDER": "widevine", "DRM_KEY_KEK": kek},
			wantErr: true,
		},
		{
			// Without a KEK the content keys would have to be stored unsealed,
			// which is exactly the doctrine interfaces.md §10 states.
			name:    "clearkey-test with no KEK",
			env:     map[string]string{"DRM_PROVIDER": "clearkey-test"},
			wantErr: true,
		},
		{
			// The "I configured DRM and it does nothing" shape.
			name:    "a KEK with no provider is refused, not ignored",
			env:     map[string]string{"DRM_KEY_KEK": kek},
			wantErr: true,
		},
		{
			name:    "a KEK that is not 32 bytes",
			env:     map[string]string{"DRM_PROVIDER": "clearkey-test", "DRM_KEY_KEK": base64.StdEncoding.EncodeToString([]byte("too short"))},
			wantErr: true,
		},
		{
			name:    "a KEK that is not base64",
			env:     map[string]string{"DRM_PROVIDER": "clearkey-test", "DRM_KEY_KEK": "!!! not base64 !!!"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tc.wantErr && err == nil {
				t.Fatalf("Load() = nil error, want error for %s", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Load() = %v, want nil error for %s", err, tc.name)
			}
		})
	}
}

// TestDRMKeyKEKHasNoFallback. MFA_KEY_KEK and ATPROTO_KEY_KEK both fall back to
// FEDERATION_KEY_KEK; DRM_KEY_KEK deliberately does not, because a content key
// and an ActivityPub actor key are different trust domains. The absence of a
// fallback is a design decision, so it gets a test rather than only a comment —
// adding a DRMKEK() accessor later would silently make a federation-key
// compromise a content-key compromise.
func TestDRMKeyKEKHasNoFallback(t *testing.T) {
	t.Setenv("FEDERATION_KEY_KEK", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32))))
	t.Setenv("DRM_PROVIDER", "clearkey-test")
	if _, err := Load(); err == nil {
		t.Fatal("clearkey-test booted with only FEDERATION_KEY_KEK set; DRM_KEY_KEK must not fall back to it")
	}
	t.Setenv("DRM_KEY_KEK", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("d", 32))))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with both KEKs: %v", err)
	}
	if cfg.DRMKeyKEK == cfg.FederationKeyKEK {
		t.Error("the DRM KEK resolved to the federation KEK")
	}
}

// TestDRMProviderNamesAgree is the drift guard for the two strings this package
// spells twice, for the same reason TestCDNPurgeTimeoutDefaultsAgree exists:
// internal/config is a leaf and does not import the packages that consume its
// output, so the accepted values live here as constants and a TEST-only import
// keeps them equal to the provider names internal/drm dispatches on. If they
// diverged, config would accept a value drm.New then rejects — turning a
// friendly boot message into a bare "unknown provider" crash.
func TestDRMProviderNamesAgree(t *testing.T) {
	if drmProviderNone != drm.ProviderNone {
		t.Errorf("config %q != drm.ProviderNone %q", drmProviderNone, drm.ProviderNone)
	}
	if drmProviderClearKeyTest != drm.ProviderClearKeyTest {
		t.Errorf("config %q != drm.ProviderClearKeyTest %q", drmProviderClearKeyTest, drm.ProviderClearKeyTest)
	}
}

// TestCDNPurgeTimeoutDefaultsAgree is the drift guard for the one number this
// package spells twice. internal/config is a leaf and does not import the
// packages that consume its output, so the default lives here as a constant and
// this test — a TEST-only import, no production coupling — keeps it equal to
// the value internal/cdn falls back to when handed a non-positive timeout.
func TestCDNPurgeTimeoutDefaultsAgree(t *testing.T) {
	if defaultCDNPurgeTimeout != cdn.DefaultPurgeTimeout {
		t.Fatalf("config default = %s, cdn.DefaultPurgeTimeout = %s; an operator who sets neither would get one of two different timeouts depending on which code path filled it in",
			defaultCDNPurgeTimeout, cdn.DefaultPurgeTimeout)
	}
}
