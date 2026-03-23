package storage

import (
	"strings"
)

type StorageCategory string

const (
	CategoryStreamingPlaylists StorageCategory = "streaming-playlists"
	CategoryWebVideos          StorageCategory = "web-videos"
	CategoryUserExports        StorageCategory = "user-exports"
	CategoryOriginalVideoFiles StorageCategory = "original-video-files"
	CategoryCaptions           StorageCategory = "captions"
)

type CategoryConfig struct {
	Prefix  string
	BaseURL string // Optional CDN base URL (e.g., "https://cdn.example.com/file/bucket")
}

type CategoryStorage struct {
	backend *S3Backend
	configs map[StorageCategory]CategoryConfig
}

func NewCategoryStorage(backend *S3Backend, configs map[StorageCategory]CategoryConfig) *CategoryStorage {
	return &CategoryStorage{
		backend: backend,
		configs: configs,
	}
}

func (cs *CategoryStorage) Key(category StorageCategory, path string) string {
	config, ok := cs.configs[category]
	if !ok {
		return path
	}

	prefix := config.Prefix
	normalizedPath := strings.TrimPrefix(path, "/")

	if prefix == "" {
		return normalizedPath
	}

	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return prefix + normalizedPath
}

func (cs *CategoryStorage) URL(category StorageCategory, path string) string {
	config, ok := cs.configs[category]
	if !ok {
		key := cs.Key(category, path)
		return cs.backend.GetURL(key)
	}

	if config.BaseURL != "" {
		key := cs.Key(category, path)
		return config.BaseURL + "/" + key
	}

	key := cs.Key(category, path)
	return cs.backend.GetURL(key)
}
