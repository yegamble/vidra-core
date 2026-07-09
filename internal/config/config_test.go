package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
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

func TestLoadRejectsAV1(t *testing.T) {
	t.Setenv("TRANSCODING_AV1_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for TRANSCODING_AV1_ENABLED=true (deferred), got nil")
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
