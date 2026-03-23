package config

import (
	"fmt"

	"athena/internal/storage"
)

func (cfg *Config) ToS3Config() storage.S3Config {
	return storage.S3Config{
		Endpoint:           cfg.S3Endpoint,
		Bucket:             cfg.S3Bucket,
		AccessKey:          cfg.S3AccessKey,
		SecretKey:          cfg.S3SecretKey,
		Region:             cfg.S3Region,
		PathStyle:          cfg.ObjectStorageConfig.PathStyle,
		UploadACLPublic:    cfg.ObjectStorageConfig.UploadACLPublic,
		UploadACLPrivate:   cfg.ObjectStorageConfig.UploadACLPrivate,
		MaxUploadPart:      parseByteSize(cfg.ObjectStorageConfig.MaxUploadPart),
		MaxRequestAttempts: cfg.ObjectStorageConfig.MaxRequestAttempts,
	}
}

func (cfg *Config) ToCategoryConfigs() map[storage.StorageCategory]storage.CategoryConfig {
	return map[storage.StorageCategory]storage.CategoryConfig{
		storage.CategoryStreamingPlaylists: {
			Prefix:  cfg.ObjectStorageConfig.StreamingPlaylistsPrefix,
			BaseURL: cfg.ObjectStorageConfig.StreamingPlaylistsBaseURL,
		},
		storage.CategoryWebVideos: {
			Prefix:  cfg.ObjectStorageConfig.WebVideosPrefix,
			BaseURL: cfg.ObjectStorageConfig.WebVideosBaseURL,
		},
		storage.CategoryUserExports: {
			Prefix:  cfg.ObjectStorageConfig.UserExportsPrefix,
			BaseURL: cfg.ObjectStorageConfig.UserExportsBaseURL,
		},
		storage.CategoryOriginalVideoFiles: {
			Prefix:  cfg.ObjectStorageConfig.OriginalVideoFilesPrefix,
			BaseURL: cfg.ObjectStorageConfig.OriginalVideoFilesBaseURL,
		},
		storage.CategoryCaptions: {
			Prefix:  cfg.ObjectStorageConfig.CaptionsPrefix,
			BaseURL: cfg.ObjectStorageConfig.CaptionsBaseURL,
		},
	}
}

func parseByteSize(s string) int64 {
	if s == "" {
		return 0
	}
	var size int64
	var unit string
	n, _ := fmt.Sscanf(s, "%d%s", &size, &unit)
	if n == 0 {
		return 0
	}
	switch unit {
	case "KB", "kb":
		return size * 1024
	case "MB", "mb":
		return size * 1024 * 1024
	case "GB", "gb":
		return size * 1024 * 1024 * 1024
	default:
		return size
	}
}

type ObjectStorageConfig struct {
	UploadACLPublic  string
	UploadACLPrivate string

	ProxifyPrivateFiles bool

	MaxUploadPart string

	MaxRequestAttempts int

	PathStyle bool

	StreamingPlaylistsPrefix  string
	StreamingPlaylistsBaseURL string

	WebVideosPrefix  string
	WebVideosBaseURL string

	UserExportsPrefix  string
	UserExportsBaseURL string

	OriginalVideoFilesPrefix  string
	OriginalVideoFilesBaseURL string

	CaptionsPrefix  string
	CaptionsBaseURL string

	StoreLiveStreams bool
}

func loadObjectStorageConfig() ObjectStorageConfig {
	return ObjectStorageConfig{
		UploadACLPublic:           GetEnvOrDefault("S3_UPLOAD_ACL_PUBLIC", "public-read"),
		UploadACLPrivate:          GetEnvOrDefault("S3_UPLOAD_ACL_PRIVATE", "private"),
		ProxifyPrivateFiles:       GetBoolEnv("S3_PROXIFY_PRIVATE_FILES", true),
		MaxUploadPart:             GetEnvOrDefault("S3_MAX_UPLOAD_PART", "100MB"),
		MaxRequestAttempts:        GetIntEnv("S3_MAX_REQUEST_ATTEMPTS", 3),
		PathStyle:                 GetBoolEnv("S3_PATH_STYLE", false),
		StreamingPlaylistsPrefix:  GetEnvOrDefault("S3_STREAMING_PLAYLISTS_PREFIX", "streaming-playlists/"),
		StreamingPlaylistsBaseURL: GetEnvOrDefault("S3_STREAMING_PLAYLISTS_BASE_URL", ""),
		WebVideosPrefix:           GetEnvOrDefault("S3_WEB_VIDEOS_PREFIX", "web-videos/"),
		WebVideosBaseURL:          GetEnvOrDefault("S3_WEB_VIDEOS_BASE_URL", ""),
		UserExportsPrefix:         GetEnvOrDefault("S3_USER_EXPORTS_PREFIX", "user-exports/"),
		UserExportsBaseURL:        GetEnvOrDefault("S3_USER_EXPORTS_BASE_URL", ""),
		OriginalVideoFilesPrefix:  GetEnvOrDefault("S3_ORIGINAL_VIDEO_FILES_PREFIX", "original-video-files/"),
		OriginalVideoFilesBaseURL: GetEnvOrDefault("S3_ORIGINAL_VIDEO_FILES_BASE_URL", ""),
		CaptionsPrefix:            GetEnvOrDefault("S3_CAPTIONS_PREFIX", "captions/"),
		CaptionsBaseURL:           GetEnvOrDefault("S3_CAPTIONS_BASE_URL", ""),
		StoreLiveStreams:          GetBoolEnv("S3_STORE_LIVE_STREAMS", false),
	}
}
