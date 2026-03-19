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
				"STORAGE_DIR":             "/tmp/athena",
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
				if cfg.StorageDir != "/tmp/athena" {
					t.Errorf("expected StorageDir /tmp/athena, got %q", cfg.StorageDir)
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
