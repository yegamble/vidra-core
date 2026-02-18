package config

import (
	"os"
	"strings"
	"testing"

	"athena/internal/storage"

	"github.com/stretchr/testify/assert"
)

func TestLoadObjectStorageConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected ObjectStorageConfig
	}{
		{
			name:    "defaults when no env vars set",
			envVars: map[string]string{},
			expected: ObjectStorageConfig{
				UploadACLPublic:           "public-read",
				UploadACLPrivate:          "private",
				ProxifyPrivateFiles:       true,
				MaxUploadPart:             "100MB",
				MaxRequestAttempts:        3,
				PathStyle:                 false,
				StreamingPlaylistsPrefix:  "streaming-playlists/",
				StreamingPlaylistsBaseURL: "",
				WebVideosPrefix:           "web-videos/",
				WebVideosBaseURL:          "",
				UserExportsPrefix:         "user-exports/",
				UserExportsBaseURL:        "",
				OriginalVideoFilesPrefix:  "original-video-files/",
				OriginalVideoFilesBaseURL: "",
				CaptionsPrefix:            "captions/",
				CaptionsBaseURL:           "",
				StoreLiveStreams:          false,
			},
		},
		{
			name: "custom ACL settings for Backblaze (null values)",
			envVars: map[string]string{
				"S3_UPLOAD_ACL_PUBLIC":  "null",
				"S3_UPLOAD_ACL_PRIVATE": "null",
			},
			expected: ObjectStorageConfig{
				UploadACLPublic:          "null",
				UploadACLPrivate:         "null",
				ProxifyPrivateFiles:      true,
				MaxUploadPart:            "100MB",
				MaxRequestAttempts:       3,
				PathStyle:                false,
				StreamingPlaylistsPrefix: "streaming-playlists/",
				WebVideosPrefix:          "web-videos/",
				UserExportsPrefix:        "user-exports/",
				OriginalVideoFilesPrefix: "original-video-files/",
				CaptionsPrefix:           "captions/",
				StoreLiveStreams:         false,
			},
		},
		{
			name: "custom upload part size and retry attempts",
			envVars: map[string]string{
				"S3_MAX_UPLOAD_PART":      "50MB",
				"S3_MAX_REQUEST_ATTEMPTS": "5",
			},
			expected: ObjectStorageConfig{
				UploadACLPublic:          "public-read",
				UploadACLPrivate:         "private",
				ProxifyPrivateFiles:      true,
				MaxUploadPart:            "50MB",
				MaxRequestAttempts:       5,
				PathStyle:                false,
				StreamingPlaylistsPrefix: "streaming-playlists/",
				WebVideosPrefix:          "web-videos/",
				UserExportsPrefix:        "user-exports/",
				OriginalVideoFilesPrefix: "original-video-files/",
				CaptionsPrefix:           "captions/",
				StoreLiveStreams:         false,
			},
		},
		{
			name: "per-category CDN base URLs",
			envVars: map[string]string{
				"S3_STREAMING_PLAYLISTS_BASE_URL": "https://cdn.example.com/playlists",
				"S3_WEB_VIDEOS_BASE_URL":          "https://cdn.example.com/videos",
				"S3_CAPTIONS_BASE_URL":            "https://cdn.example.com/captions",
			},
			expected: ObjectStorageConfig{
				UploadACLPublic:           "public-read",
				UploadACLPrivate:          "private",
				ProxifyPrivateFiles:       true,
				MaxUploadPart:             "100MB",
				MaxRequestAttempts:        3,
				PathStyle:                 false,
				StreamingPlaylistsPrefix:  "streaming-playlists/",
				StreamingPlaylistsBaseURL: "https://cdn.example.com/playlists",
				WebVideosPrefix:           "web-videos/",
				WebVideosBaseURL:          "https://cdn.example.com/videos",
				UserExportsPrefix:         "user-exports/",
				UserExportsBaseURL:        "",
				OriginalVideoFilesPrefix:  "original-video-files/",
				OriginalVideoFilesBaseURL: "",
				CaptionsPrefix:            "captions/",
				CaptionsBaseURL:           "https://cdn.example.com/captions",
				StoreLiveStreams:          false,
			},
		},
		{
			name: "custom prefixes",
			envVars: map[string]string{
				"S3_STREAMING_PLAYLISTS_PREFIX": "hls/",
				"S3_WEB_VIDEOS_PREFIX":          "vids/",
			},
			expected: ObjectStorageConfig{
				UploadACLPublic:          "public-read",
				UploadACLPrivate:         "private",
				ProxifyPrivateFiles:      true,
				MaxUploadPart:            "100MB",
				MaxRequestAttempts:       3,
				PathStyle:                false,
				StreamingPlaylistsPrefix: "hls/",
				WebVideosPrefix:          "vids/",
				UserExportsPrefix:        "user-exports/",
				OriginalVideoFilesPrefix: "original-video-files/",
				CaptionsPrefix:           "captions/",
				StoreLiveStreams:         false,
			},
		},
		{
			name: "proxify disabled and path style enabled",
			envVars: map[string]string{
				"S3_PROXIFY_PRIVATE_FILES": "false",
				"S3_PATH_STYLE":            "true",
				"S3_STORE_LIVE_STREAMS":    "true",
			},
			expected: ObjectStorageConfig{
				UploadACLPublic:          "public-read",
				UploadACLPrivate:         "private",
				ProxifyPrivateFiles:      false,
				MaxUploadPart:            "100MB",
				MaxRequestAttempts:       3,
				PathStyle:                true,
				StreamingPlaylistsPrefix: "streaming-playlists/",
				WebVideosPrefix:          "web-videos/",
				UserExportsPrefix:        "user-exports/",
				OriginalVideoFilesPrefix: "original-video-files/",
				CaptionsPrefix:           "captions/",
				StoreLiveStreams:         true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearObjectStorageEnvVars(t)

			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg := loadObjectStorageConfig()

			assert.Equal(t, tt.expected, cfg)
		})
	}
}

func clearObjectStorageEnvVars(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(key, "S3_") {
			os.Unsetenv(key)
		}
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
	}{
		{"100MB", "100MB", 100 * 1024 * 1024},
		{"50GB", "50GB", 50 * 1024 * 1024 * 1024},
		{"512KB", "512KB", 512 * 1024},
		{"lowercase mb", "100mb", 100 * 1024 * 1024},
		{"plain bytes", "1024", 1024},
		{"empty string", "", 0},
		{"invalid", "abc", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseByteSize(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToS3Config(t *testing.T) {
	cfg := &Config{
		S3Endpoint:  "s3.us-east-005.backblazeb2.com",
		S3Bucket:    "mybucket",
		S3AccessKey: "access123",
		S3SecretKey: "secret456",
		S3Region:    "us-east-1",
		ObjectStorageConfig: ObjectStorageConfig{
			UploadACLPublic:    "null",
			UploadACLPrivate:   "null",
			PathStyle:          true,
			MaxUploadPart:      "100MB",
			MaxRequestAttempts: 5,
		},
	}

	s3Cfg := cfg.ToS3Config()

	assert.Equal(t, "s3.us-east-005.backblazeb2.com", s3Cfg.Endpoint)
	assert.Equal(t, "mybucket", s3Cfg.Bucket)
	assert.Equal(t, "access123", s3Cfg.AccessKey)
	assert.Equal(t, "secret456", s3Cfg.SecretKey)
	assert.Equal(t, "us-east-1", s3Cfg.Region)
	assert.Equal(t, true, s3Cfg.PathStyle)
	assert.Equal(t, "null", s3Cfg.UploadACLPublic)
	assert.Equal(t, "null", s3Cfg.UploadACLPrivate)
	assert.Equal(t, int64(100*1024*1024), s3Cfg.MaxUploadPart)
	assert.Equal(t, 5, s3Cfg.MaxRequestAttempts)
}

func TestToCategoryConfigs(t *testing.T) {
	cfg := &Config{
		ObjectStorageConfig: ObjectStorageConfig{
			StreamingPlaylistsPrefix:  "hls/",
			StreamingPlaylistsBaseURL: "https://cdn.example.com",
			WebVideosPrefix:           "videos/",
			CaptionsPrefix:            "subs/",
			CaptionsBaseURL:           "https://cdn2.example.com",
		},
	}

	cats := cfg.ToCategoryConfigs()

	assert.Len(t, cats, 5)
	assert.Equal(t, "hls/", cats[storage.CategoryStreamingPlaylists].Prefix)
	assert.Equal(t, "https://cdn.example.com", cats[storage.CategoryStreamingPlaylists].BaseURL)
	assert.Equal(t, "videos/", cats[storage.CategoryWebVideos].Prefix)
	assert.Equal(t, "subs/", cats[storage.CategoryCaptions].Prefix)
	assert.Equal(t, "https://cdn2.example.com", cats[storage.CategoryCaptions].BaseURL)
}

func TestObjectStorageACLNullHandling(t *testing.T) {
	tests := []struct {
		name     string
		aclValue string
		expected string
	}{
		{"explicit null string for Backblaze", "null", "null"},
		{"empty string uses default", "", "public-read"},
		{"public-read", "public-read", "public-read"},
		{"private", "private", "private"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearObjectStorageEnvVars(t)
			if tt.aclValue != "" {
				t.Setenv("S3_UPLOAD_ACL_PUBLIC", tt.aclValue)
			}

			cfg := loadObjectStorageConfig()
			assert.Equal(t, tt.expected, cfg.UploadACLPublic)
		})
	}
}
