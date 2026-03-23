package video

import (
	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/storage"
	"vidra-core/internal/usecase"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockHLSVideoRepo struct {
	usecase.VideoRepository
	getByIDFunc func(ctx context.Context, id string) (*domain.Video, error)
}

func (m *mockHLSVideoRepo) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, errors.New("not implemented")
}

type mockHLSS3Backend struct {
	storage.StorageBackend
	getURLFunc       func(key string) string
	getSignedURLFunc func(ctx context.Context, key string, expiration time.Duration) (string, error)
	downloadFunc     func(ctx context.Context, key string) (io.ReadCloser, error)
}

func (m *mockHLSS3Backend) GetURL(key string) string {
	if m.getURLFunc != nil {
		return m.getURLFunc(key)
	}
	return ""
}

func (m *mockHLSS3Backend) GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	if m.getSignedURLFunc != nil {
		return m.getSignedURLFunc(ctx, key, expiration)
	}
	return "", errors.New("not implemented")
}

func (m *mockHLSS3Backend) Upload(ctx context.Context, key string, data io.Reader, contentType string) error {
	return errors.New("not implemented")
}
func (m *mockHLSS3Backend) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	if m.downloadFunc != nil {
		return m.downloadFunc(ctx, key)
	}
	return nil, errors.New("not implemented")
}
func (m *mockHLSS3Backend) GetMetadata(ctx context.Context, key string) (*storage.FileMetadata, error) {
	return nil, errors.New("not implemented")
}

func TestHLSHandlerWithS3_InvalidPath(t *testing.T) {
	videoRepo := &mockHLSVideoRepo{}
	cfg := &config.Config{}
	s3Backend := &mockHLSS3Backend{}

	handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "path without prefix",
			path:       "/videos/123/master.m3u8",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty path after prefix",
			path:       "/api/v1/hls/",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestHLSHandlerWithS3_VideoNotFound(t *testing.T) {
	videoRepo := &mockHLSVideoRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}
	cfg := &config.Config{}
	s3Backend := &mockHLSS3Backend{}

	handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

	req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHLSHandlerWithS3_PrivateVideoAccessDenied(t *testing.T) {
	videoRepo := &mockHLSVideoRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.Video, error) {
			return &domain.Video{
				ID:      id,
				Privacy: domain.PrivacyPrivate,
				UserID:  "owner123",
			}, nil
		},
	}
	cfg := &config.Config{}
	s3Backend := &mockHLSS3Backend{}

	handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

	tests := []struct {
		name       string
		userID     string
		wantStatus int
	}{
		{
			name:       "no auth",
			userID:     "",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "different user",
			userID:     "other456",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
			if tt.userID != "" {
				ctx := context.WithValue(req.Context(), middleware.UserIDKey, tt.userID)
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}

	t.Run("owner access", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, "owner123")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.NotEqual(t, http.StatusForbidden, rec.Code, "owner should not be forbidden")
	})
}

func TestHLSHandlerWithS3_S3Redirect(t *testing.T) {
	now := time.Now()
	videoRepo := &mockHLSVideoRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.Video, error) {
			return &domain.Video{
				ID:           id,
				Privacy:      domain.PrivacyPublic,
				UserID:       "owner123",
				StorageTier:  "cold",
				S3MigratedAt: &now,
			}, nil
		},
	}
	cfg := &config.Config{
		EnableS3: true,
	}
	s3Backend := &mockHLSS3Backend{
		getURLFunc: func(key string) string {
			return "https://s3.example.com/" + key
		},
	}

	handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

	tests := []struct {
		name               string
		path               string
		wantStatus         int
		wantLocationPrefix string
		wantContentType    string
	}{
		{
			name:               "m3u8 playlist redirect",
			path:               "/api/v1/hls/video123/master.m3u8",
			wantStatus:         http.StatusFound,
			wantLocationPrefix: "https://s3.example.com/videos/video123/hls/master.m3u8",
			wantContentType:    "application/vnd.apple.mpegurl",
		},
		{
			name:               "ts segment redirect",
			path:               "/api/v1/hls/video123/720p/segment_001.ts",
			wantStatus:         http.StatusFound,
			wantLocationPrefix: "https://s3.example.com/videos/video123/hls/720p/segment_001.ts",
			wantContentType:    "video/MP2T",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantLocationPrefix, rec.Header().Get("Location"))
			assert.Equal(t, tt.wantContentType, rec.Header().Get("Content-Type"))
			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

func TestHLSHandlerWithS3_PrivateVideoSignedURL(t *testing.T) {
	now := time.Now()
	videoRepo := &mockHLSVideoRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.Video, error) {
			return &domain.Video{
				ID:           id,
				Privacy:      domain.PrivacyPrivate,
				UserID:       "owner123",
				StorageTier:  "cold",
				S3MigratedAt: &now,
			}, nil
		},
	}
	cfg := &config.Config{
		EnableS3: true,
	}

	tests := []struct {
		name            string
		signedURLFunc   func(ctx context.Context, key string, expiration time.Duration) (string, error)
		wantLocationURL string
		description     string
	}{
		{
			name: "signed URL success",
			signedURLFunc: func(ctx context.Context, key string, expiration time.Duration) (string, error) {
				return "https://s3.example.com/videos/video123/hls/master.m3u8?signature=xyz", nil
			},
			wantLocationURL: "https://s3.example.com/videos/video123/hls/master.m3u8?signature=xyz",
			description:     "should use signed URL for private video",
		},
		{
			name: "signed URL error - fallback to unsigned",
			signedURLFunc: func(ctx context.Context, key string, expiration time.Duration) (string, error) {
				return "", errors.New("signing failed")
			},
			wantLocationURL: "https://s3.example.com/videos/video123/hls/master.m3u8",
			description:     "should fallback to unsigned URL if signing fails",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s3Backend := &mockHLSS3Backend{
				getURLFunc: func(key string) string {
					return "https://s3.example.com/" + key
				},
				getSignedURLFunc: tt.signedURLFunc,
			}

			handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

			req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, "owner123")
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusFound, rec.Code, tt.description)
			assert.Equal(t, tt.wantLocationURL, rec.Header().Get("Location"), tt.description)
		})
	}
}

func TestHLSHandlerWithS3_FallbackToLocal(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		enableS3    bool
		storageTier string
		migratedAt  *time.Time
		description string
	}{
		{
			name:        "S3 disabled",
			enableS3:    false,
			storageTier: "hot",
			migratedAt:  nil,
			description: "should fallback to local when S3 is disabled",
		},
		{
			name:        "video not migrated",
			enableS3:    true,
			storageTier: "hot",
			migratedAt:  nil,
			description: "should fallback to local when video not migrated to S3",
		},
		{
			name:        "storage tier is hot",
			enableS3:    true,
			storageTier: "hot",
			migratedAt:  &now,
			description: "should fallback to local when storage tier is not cold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoRepo := &mockHLSVideoRepo{
				getByIDFunc: func(ctx context.Context, id string) (*domain.Video, error) {
					return &domain.Video{
						ID:           id,
						Privacy:      domain.PrivacyPublic,
						UserID:       "owner123",
						StorageTier:  tt.storageTier,
						S3MigratedAt: tt.migratedAt,
					}, nil
				},
			}
			cfg := &config.Config{
				EnableS3: tt.enableS3,
			}
			s3Backend := &mockHLSS3Backend{}

			handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

			req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.NotEqual(t, http.StatusFound, rec.Code, tt.description)
			assert.Empty(t, rec.Header().Get("Location"), tt.description+" - should not have Location header")
		})
	}
}

func TestHLSHandlerWithS3_ProxifyPrivateFiles(t *testing.T) {
	now := time.Now()
	videoRepo := &mockHLSVideoRepo{
		getByIDFunc: func(ctx context.Context, id string) (*domain.Video, error) {
			return &domain.Video{
				ID:           id,
				Privacy:      domain.PrivacyPrivate,
				UserID:       "owner123",
				StorageTier:  "cold",
				S3MigratedAt: &now,
			}, nil
		},
	}

	t.Run("proxify enabled - streams from S3", func(t *testing.T) {
		cfg := &config.Config{
			EnableS3: true,
			ObjectStorageConfig: config.ObjectStorageConfig{
				ProxifyPrivateFiles: true,
			},
		}
		s3Backend := &mockHLSS3Backend{
			downloadFunc: func(ctx context.Context, key string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("mock data")), nil
			},
		}

		handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

		req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, "owner123")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Location"))
		assert.Equal(t, "application/vnd.apple.mpegurl", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Header().Get("Cache-Control"), "private")
	})

	t.Run("proxify disabled - redirects with signed URL", func(t *testing.T) {
		cfg := &config.Config{
			EnableS3: true,
			ObjectStorageConfig: config.ObjectStorageConfig{
				ProxifyPrivateFiles: false,
			},
		}
		s3Backend := &mockHLSS3Backend{
			getURLFunc: func(key string) string {
				return "https://s3.example.com/" + key
			},
			getSignedURLFunc: func(ctx context.Context, key string, expiration time.Duration) (string, error) {
				return "https://s3.example.com/signed", nil
			},
		}

		handler := HLSHandlerWithS3(videoRepo, cfg, s3Backend)

		req := httptest.NewRequest("GET", "/api/v1/hls/video123/master.m3u8", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, "owner123")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, "https://s3.example.com/signed", rec.Header().Get("Location"))
	})
}
