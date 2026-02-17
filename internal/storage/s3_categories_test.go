package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCategoryStorage_KeyGeneration(t *testing.T) {
	tests := []struct {
		name     string
		category StorageCategory
		path     string
		want     string
	}{
		{
			name:     "streaming playlists with default prefix",
			category: CategoryStreamingPlaylists,
			path:     "video-123/master.m3u8",
			want:     "streaming-playlists/video-123/master.m3u8",
		},
		{
			name:     "web videos with default prefix",
			category: CategoryWebVideos,
			path:     "video-456.mp4",
			want:     "web-videos/video-456.mp4",
		},
		{
			name:     "user exports",
			category: CategoryUserExports,
			path:     "user-789/export.zip",
			want:     "user-exports/user-789/export.zip",
		},
		{
			name:     "original video files",
			category: CategoryOriginalVideoFiles,
			path:     "original-123.mov",
			want:     "original-video-files/original-123.mov",
		},
		{
			name:     "captions",
			category: CategoryCaptions,
			path:     "video-123/en.vtt",
			want:     "captions/video-123/en.vtt",
		},
	}

	backend, err := NewS3Backend(newS3ConfigForTests())
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}

	configs := map[StorageCategory]CategoryConfig{
		CategoryStreamingPlaylists: {Prefix: "streaming-playlists/", BaseURL: ""},
		CategoryWebVideos:          {Prefix: "web-videos/", BaseURL: ""},
		CategoryUserExports:        {Prefix: "user-exports/", BaseURL: ""},
		CategoryOriginalVideoFiles: {Prefix: "original-video-files/", BaseURL: ""},
		CategoryCaptions:           {Prefix: "captions/", BaseURL: ""},
	}

	cs := NewCategoryStorage(backend, configs)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cs.Key(tt.category, tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCategoryStorage_URLGeneration(t *testing.T) {
	tests := []struct {
		name     string
		category StorageCategory
		path     string
		baseURL  string
		want     string
	}{
		{
			name:     "CDN base URL for streaming playlists",
			category: CategoryStreamingPlaylists,
			path:     "video-123/master.m3u8",
			baseURL:  "https://cdn.example.com/file/bucket",
			want:     "https://cdn.example.com/file/bucket/streaming-playlists/video-123/master.m3u8",
		},
		{
			name:     "CDN base URL for web videos",
			category: CategoryWebVideos,
			path:     "video-456.mp4",
			baseURL:  "https://cdn.example.com/file/bucket",
			want:     "https://cdn.example.com/file/bucket/web-videos/video-456.mp4",
		},
		{
			name:     "fallback to S3 URL when no CDN configured",
			category: CategoryUserExports,
			path:     "user-789/export.zip",
			baseURL:  "",
			want:     "http://127.0.0.1:1/athena-test-bucket/user-exports/user-789/export.zip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := NewS3Backend(newS3ConfigForTests())
			if err != nil {
				t.Fatalf("NewS3Backend() error = %v", err)
			}

			configs := map[StorageCategory]CategoryConfig{
				CategoryStreamingPlaylists: {Prefix: "streaming-playlists/", BaseURL: tt.baseURL},
				CategoryWebVideos:          {Prefix: "web-videos/", BaseURL: tt.baseURL},
				CategoryUserExports:        {Prefix: "user-exports/", BaseURL: tt.baseURL},
			}

			cs := NewCategoryStorage(backend, configs)
			got := cs.URL(tt.category, tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCategoryStorage_CustomPrefixes(t *testing.T) {
	backend, err := NewS3Backend(newS3ConfigForTests())
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}

	configs := map[StorageCategory]CategoryConfig{
		CategoryStreamingPlaylists: {Prefix: "hls/", BaseURL: ""},
		CategoryWebVideos:          {Prefix: "vids/", BaseURL: ""},
	}

	cs := NewCategoryStorage(backend, configs)

	tests := []struct {
		name     string
		category StorageCategory
		path     string
		wantKey  string
	}{
		{
			name:     "custom HLS prefix",
			category: CategoryStreamingPlaylists,
			path:     "stream.m3u8",
			wantKey:  "hls/stream.m3u8",
		},
		{
			name:     "custom videos prefix",
			category: CategoryWebVideos,
			path:     "video.mp4",
			wantKey:  "vids/video.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cs.Key(tt.category, tt.path)
			assert.Equal(t, tt.wantKey, got)
		})
	}
}

func TestCategoryStorage_EmptyPrefix(t *testing.T) {
	backend, err := NewS3Backend(newS3ConfigForTests())
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}

	configs := map[StorageCategory]CategoryConfig{
		CategoryWebVideos: {Prefix: "", BaseURL: ""},
	}

	cs := NewCategoryStorage(backend, configs)

	got := cs.Key(CategoryWebVideos, "video.mp4")
	want := "video.mp4"
	assert.Equal(t, want, got)
}

func TestCategoryStorage_TrailingSlashes(t *testing.T) {
	backend, err := NewS3Backend(newS3ConfigForTests())
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}

	tests := []struct {
		name    string
		prefix  string
		path    string
		wantKey string
	}{
		{
			name:    "prefix with trailing slash",
			prefix:  "videos/",
			path:    "file.mp4",
			wantKey: "videos/file.mp4",
		},
		{
			name:    "prefix without trailing slash",
			prefix:  "videos",
			path:    "file.mp4",
			wantKey: "videos/file.mp4",
		},
		{
			name:    "path with leading slash",
			prefix:  "videos/",
			path:    "/file.mp4",
			wantKey: "videos/file.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configs := map[StorageCategory]CategoryConfig{
				CategoryWebVideos: {Prefix: tt.prefix, BaseURL: ""},
			}

			cs := NewCategoryStorage(backend, configs)
			got := cs.Key(CategoryWebVideos, tt.path)
			assert.Equal(t, tt.wantKey, got)
		})
	}
}
