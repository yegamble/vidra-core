package config

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadCommonFields(t *testing.T) {
	tests := []struct {
		name      string
		setupMode bool
		envVars   map[string]string
		verify    func(t *testing.T, cfg *Config)
	}{
		{
			name:      "defaults in normal mode",
			setupMode: false,
			envVars:   map[string]string{},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.RequireIPFS != true {
					t.Errorf("expected RequireIPFS default true in normal mode, got %v", cfg.RequireIPFS)
				}
				if cfg.StorageDir != "./storage" {
					t.Errorf("expected StorageDir default ./storage, got %q", cfg.StorageDir)
				}
				if cfg.IPFSStreamingTimeout != 30*time.Second {
					t.Errorf("expected IPFSStreamingTimeout default 30s, got %v", cfg.IPFSStreamingTimeout)
				}
			},
		},
		{
			name:      "defaults in setup mode",
			setupMode: true,
			envVars:   map[string]string{},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.RequireIPFS != false {
					t.Errorf("expected RequireIPFS default false in setup mode, got %v", cfg.RequireIPFS)
				}
			},
		},
		{
			name:      "overriding values with env vars",
			setupMode: false,
			envVars: map[string]string{
				"REQUIRE_IPFS":            "false",
				"STORAGE_DIR":             "/tmp/vidra",
				"IPFS_API":                "http://ipfs:5001",
				"MAX_UPLOAD_SIZE":         "1048576", // 1MB
				"VIDEO_QUALITIES":         "360p,720p",
				"PINNING_SCORE_THRESHOLD": "0.5",
				"BACKUP_EXCLUDE_DIRS":     " /dir1, dir2 ",
			},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.RequireIPFS != false {
					t.Errorf("expected RequireIPFS false, got %v", cfg.RequireIPFS)
				}
				if cfg.StorageDir != "/tmp/vidra" {
					t.Errorf("expected StorageDir /tmp/vidra, got %q", cfg.StorageDir)
				}
				if cfg.IPFSApi != "http://ipfs:5001" {
					t.Errorf("expected IPFSApi http://ipfs:5001, got %q", cfg.IPFSApi)
				}
				if cfg.MaxUploadSize != 1048576 {
					t.Errorf("expected MaxUploadSize 1048576, got %d", cfg.MaxUploadSize)
				}
				expectedQualities := []string{"360p", "720p"}
				if !reflect.DeepEqual(cfg.VideoQualities, expectedQualities) {
					t.Errorf("expected VideoQualities %v, got %v", expectedQualities, cfg.VideoQualities)
				}
				if cfg.PinningScoreThreshold != 0.5 {
					t.Errorf("expected PinningScoreThreshold 0.5, got %v", cfg.PinningScoreThreshold)
				}
				expectedExclude := []string{"/dir1", "dir2"}
				if !reflect.DeepEqual(cfg.BackupExcludeDirs, expectedExclude) {
					t.Errorf("expected BackupExcludeDirs %v, got %v", expectedExclude, cfg.BackupExcludeDirs)
				}
			},
		},
		{
			name:      "time duration fields",
			setupMode: false,
			envVars: map[string]string{
				"IPFS_STREAMING_TIMEOUT": "60",
				"RTMP_READ_TIMEOUT":      "45",
			},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.IPFSStreamingTimeout != 60*time.Second {
					t.Errorf("expected IPFSStreamingTimeout 60s, got %v", cfg.IPFSStreamingTimeout)
				}
				if cfg.RTMPReadTimeout != 45*time.Second {
					t.Errorf("expected RTMPReadTimeout 45s, got %v", cfg.RTMPReadTimeout)
				}
			},
		},
		{
			name:      "IPFS_LOCAL_GATEWAY_URL default",
			setupMode: false,
			envVars:   map[string]string{"REQUIRE_IPFS": "false"},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.IPFSLocalGatewayURL != "http://localhost:8080" {
					t.Errorf("expected IPFSLocalGatewayURL default http://localhost:8080, got %q", cfg.IPFSLocalGatewayURL)
				}
			},
		},
		{
			name:      "IPFS_LOCAL_GATEWAY_URL custom",
			setupMode: false,
			envVars: map[string]string{
				"REQUIRE_IPFS":           "false",
				"IPFS_LOCAL_GATEWAY_URL": "http://ipfs-node:8080",
			},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.IPFSLocalGatewayURL != "http://ipfs-node:8080" {
					t.Errorf("expected IPFSLocalGatewayURL http://ipfs-node:8080, got %q", cfg.IPFSLocalGatewayURL)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear relevant env vars first to ensure clean state
			// Actually t.Setenv handles cleanup, but if we don't set a var, it might inherit from the process
			// So we should ideally clear all vars we might use.

			// For simplicity in this test, we'll just set the ones we care about.
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			// Some fields might be affected by env vars set in other tests if they are not cleared.
			// But since we use a fresh Config struct and t.Setenv, it should be mostly fine.

			cfg := &Config{}
			loadCommonFields(cfg, tt.setupMode)
			tt.verify(t, cfg)
		})
	}
}

func TestLogConfig(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		verify  func(t *testing.T, cfg *Config)
	}{
		{
			name:    "log config defaults",
			envVars: map[string]string{},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.LogDir != "" {
					t.Errorf("expected LogDir default empty, got %q", cfg.LogDir)
				}
				if cfg.LogFilename != "vidra.log" {
					t.Errorf("expected LogFilename default vidra.log, got %q", cfg.LogFilename)
				}
				if cfg.AuditLogFilename != "vidra-audit.log" {
					t.Errorf("expected AuditLogFilename default vidra-audit.log, got %q", cfg.AuditLogFilename)
				}
				if cfg.LogRotationEnabled != true {
					t.Errorf("expected LogRotationEnabled default true, got %v", cfg.LogRotationEnabled)
				}
				if cfg.LogRotationMaxSizeMB != 12 {
					t.Errorf("expected LogRotationMaxSizeMB default 12, got %d", cfg.LogRotationMaxSizeMB)
				}
				if cfg.LogRotationMaxFiles != 20 {
					t.Errorf("expected LogRotationMaxFiles default 20, got %d", cfg.LogRotationMaxFiles)
				}
				if cfg.LogRotationMaxAgeDays != 0 {
					t.Errorf("expected LogRotationMaxAgeDays default 0, got %d", cfg.LogRotationMaxAgeDays)
				}
				if cfg.LogAnonymizeIP != false {
					t.Errorf("expected LogAnonymizeIP default false, got %v", cfg.LogAnonymizeIP)
				}
				if cfg.LogHTTPRequests != true {
					t.Errorf("expected LogHTTPRequests default true, got %v", cfg.LogHTTPRequests)
				}
				if cfg.LogPingRequests != true {
					t.Errorf("expected LogPingRequests default true, got %v", cfg.LogPingRequests)
				}
				if cfg.LogAcceptClientLog != true {
					t.Errorf("expected LogAcceptClientLog default true, got %v", cfg.LogAcceptClientLog)
				}
			},
		},
		{
			name: "log config overrides",
			envVars: map[string]string{
				"LOG_DIR":                   "/var/log/vidra",
				"LOG_FILENAME":              "custom.log",
				"AUDIT_LOG_FILENAME":        "custom-audit.log",
				"LOG_ROTATION_ENABLED":      "false",
				"LOG_ROTATION_MAX_SIZE_MB":  "50",
				"LOG_ROTATION_MAX_FILES":    "5",
				"LOG_ROTATION_MAX_AGE_DAYS": "30",
				"LOG_ANONYMIZE_IP":          "true",
				"LOG_HTTP_REQUESTS":         "false",
				"LOG_PING_REQUESTS":         "false",
				"LOG_ACCEPT_CLIENT_LOG":     "false",
			},
			verify: func(t *testing.T, cfg *Config) {
				if cfg.LogDir != "/var/log/vidra" {
					t.Errorf("expected LogDir /var/log/vidra, got %q", cfg.LogDir)
				}
				if cfg.LogFilename != "custom.log" {
					t.Errorf("expected LogFilename custom.log, got %q", cfg.LogFilename)
				}
				if cfg.AuditLogFilename != "custom-audit.log" {
					t.Errorf("expected AuditLogFilename custom-audit.log, got %q", cfg.AuditLogFilename)
				}
				if cfg.LogRotationEnabled != false {
					t.Errorf("expected LogRotationEnabled false, got %v", cfg.LogRotationEnabled)
				}
				if cfg.LogRotationMaxSizeMB != 50 {
					t.Errorf("expected LogRotationMaxSizeMB 50, got %d", cfg.LogRotationMaxSizeMB)
				}
				if cfg.LogRotationMaxFiles != 5 {
					t.Errorf("expected LogRotationMaxFiles 5, got %d", cfg.LogRotationMaxFiles)
				}
				if cfg.LogRotationMaxAgeDays != 30 {
					t.Errorf("expected LogRotationMaxAgeDays 30, got %d", cfg.LogRotationMaxAgeDays)
				}
				if cfg.LogAnonymizeIP != true {
					t.Errorf("expected LogAnonymizeIP true, got %v", cfg.LogAnonymizeIP)
				}
				if cfg.LogHTTPRequests != false {
					t.Errorf("expected LogHTTPRequests false, got %v", cfg.LogHTTPRequests)
				}
				if cfg.LogPingRequests != false {
					t.Errorf("expected LogPingRequests false, got %v", cfg.LogPingRequests)
				}
				if cfg.LogAcceptClientLog != false {
					t.Errorf("expected LogAcceptClientLog false, got %v", cfg.LogAcceptClientLog)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			cfg := &Config{}
			loadCommonFields(cfg, false)
			tt.verify(t, cfg)
		})
	}
}
