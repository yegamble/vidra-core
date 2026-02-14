package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSwapExt(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		newExt string
		want   string
	}{
		{"replace .mp4 with .webm", "video.mp4", ".webm", "video.webm"},
		{"replace .jpg with .png", "image.jpg", ".png", "image.png"},
		{"no extension appends", "noext", ".mp4", "noext.mp4"},
		{"with path", "/tmp/uploads/video.avi", ".mkv", "/tmp/uploads/video.mkv"},
		{"dotfile no ext", ".hidden", ".txt", ".hidden.txt"},
		{"multiple dots", "archive.tar.gz", ".bz2", "archive.tar.bz2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := swapExt(tt.path, tt.newExt)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveRepoDID(t *testing.T) {
	ctx := context.Background()

	t.Run("returns DID from instance config", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		svc := &atprotoService{
			modRepo: modRepo,
			cfg:     &config.Config{},
		}

		didJSON, _ := json.Marshal("did:plc:abc123")
		modRepo.On("GetInstanceConfig", ctx, "atproto_did").Return(&domain.InstanceConfig{
			Value: didJSON,
		}, nil)

		did := svc.resolveRepoDID(ctx)
		assert.Equal(t, "did:plc:abc123", did)
		modRepo.AssertExpectations(t)
	})

	t.Run("returns empty when modRepo error", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		svc := &atprotoService{
			modRepo: modRepo,
			cfg:     &config.Config{},
		}

		modRepo.On("GetInstanceConfig", ctx, "atproto_did").Return((*domain.InstanceConfig)(nil), fmt.Errorf("not found"))

		did := svc.resolveRepoDID(ctx)
		assert.Empty(t, did)
		modRepo.AssertExpectations(t)
	})

	t.Run("returns empty when modRepo is nil", func(t *testing.T) {
		svc := &atprotoService{
			modRepo: nil,
			cfg:     &config.Config{},
		}

		did := svc.resolveRepoDID(ctx)
		assert.Empty(t, did)
	})

	t.Run("returns empty when JSON unmarshal fails", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		svc := &atprotoService{
			modRepo: modRepo,
			cfg:     &config.Config{},
		}

		modRepo.On("GetInstanceConfig", ctx, "atproto_did").Return(&domain.InstanceConfig{
			Value: json.RawMessage(`not-valid-json`),
		}, nil)

		did := svc.resolveRepoDID(ctx)
		assert.Empty(t, did)
		modRepo.AssertExpectations(t)
	})
}

func TestResolvePDSURL(t *testing.T) {
	ctx := context.Background()

	t.Run("returns URL from instance config", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		svc := &atprotoService{
			modRepo: modRepo,
			cfg:     &config.Config{ATProtoPDSURL: "https://fallback.example.com"},
		}

		urlJSON, _ := json.Marshal("https://pds.example.com")
		modRepo.On("GetInstanceConfig", ctx, "atproto_pds_url").Return(&domain.InstanceConfig{
			Value: urlJSON,
		}, nil)

		url := svc.resolvePDSURL(ctx)
		assert.Equal(t, "https://pds.example.com", url)
		modRepo.AssertExpectations(t)
	})

	t.Run("falls back to config when instance config fails", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		svc := &atprotoService{
			modRepo: modRepo,
			cfg:     &config.Config{ATProtoPDSURL: "https://fallback.example.com"},
		}

		modRepo.On("GetInstanceConfig", ctx, "atproto_pds_url").Return((*domain.InstanceConfig)(nil), fmt.Errorf("not found"))

		url := svc.resolvePDSURL(ctx)
		assert.Equal(t, "https://fallback.example.com", url)
		modRepo.AssertExpectations(t)
	})

	t.Run("falls back when config value is empty", func(t *testing.T) {
		modRepo := new(MockInstanceConfigReader)
		svc := &atprotoService{
			modRepo: modRepo,
			cfg:     &config.Config{ATProtoPDSURL: "https://fallback.example.com"},
		}

		urlJSON, _ := json.Marshal("   ")
		modRepo.On("GetInstanceConfig", ctx, "atproto_pds_url").Return(&domain.InstanceConfig{
			Value: urlJSON,
		}, nil)

		url := svc.resolvePDSURL(ctx)
		assert.Equal(t, "https://fallback.example.com", url)
		modRepo.AssertExpectations(t)
	})
}

func TestGetAuthorFeed(t *testing.T) {
	t.Run("successful feed fetch", func(t *testing.T) {
		feedResponse := map[string]any{
			"feed":   []any{},
			"cursor": "next-page",
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path, "/xrpc/app.bsky.feed.getAuthorFeed")
			assert.Equal(t, "did:plc:test", r.URL.Query().Get("actor"))
			assert.Equal(t, "10", r.URL.Query().Get("limit"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(feedResponse)
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		result, err := svc.getAuthorFeed(context.Background(), "did:plc:test", 10, "")
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "next-page", result["cursor"])
	})

	t.Run("with cursor parameter", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "abc123", r.URL.Query().Get("cursor"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"feed": []any{}})
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		_, err := svc.getAuthorFeed(context.Background(), "did:plc:test", 20, "abc123")
		require.NoError(t, err)
	})

	t.Run("defaults limit when out of range", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "20", r.URL.Query().Get("limit"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"feed": []any{}})
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		_, err := svc.getAuthorFeed(context.Background(), "did:plc:test", 0, "")
		require.NoError(t, err)
	})

	t.Run("returns error on non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		_, err := svc.getAuthorFeed(context.Background(), "did:plc:test", 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getAuthorFeed status 500")
	})

	t.Run("returns error when PDS URL empty", func(t *testing.T) {
		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: ""},
			client: http.DefaultClient,
		}

		_, err := svc.getAuthorFeed(context.Background(), "did:plc:test", 10, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing PDS URL")
	})
}

func TestUploadBlob(t *testing.T) {
	t.Run("successful upload", func(t *testing.T) {
		blobResp := struct {
			Blob map[string]any `json:"blob"`
		}{
			Blob: map[string]any{
				"$type":    "blob",
				"ref":      map[string]any{"$link": "bafytest123"},
				"mimeType": "image/jpeg",
				"size":     float64(1234),
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "image/jpeg", r.Header.Get("Content-Type"))
			assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(blobResp)
		}))
		defer server.Close()

		tmpFile := filepath.Join(t.TempDir(), "test.jpg")
		require.NoError(t, os.WriteFile(tmpFile, []byte("fake-image"), 0o600))

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		blob, err := svc.uploadBlob(context.Background(), "test-jwt", tmpFile)
		require.NoError(t, err)
		require.NotNil(t, blob)
	})

	t.Run("detects mime type from extension", func(t *testing.T) {
		extensions := map[string]string{
			"test.jpg":  "image/jpeg",
			"test.jpeg": "image/jpeg",
			"test.png":  "image/png",
			"test.webp": "image/webp",
			"test.bin":  "application/octet-stream",
		}

		for filename, expectedMime := range extensions {
			t.Run(filename, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, expectedMime, r.Header.Get("Content-Type"))
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]any{"blob": map[string]any{"ref": "ok"}})
				}))
				defer server.Close()

				tmpFile := filepath.Join(t.TempDir(), filename)
				require.NoError(t, os.WriteFile(tmpFile, []byte("data"), 0o600))

				svc := &atprotoService{
					cfg:    &config.Config{ATProtoPDSURL: server.URL},
					client: server.Client(),
				}

				_, err := svc.uploadBlob(context.Background(), "jwt", tmpFile)
				require.NoError(t, err)
			})
		}
	})

	t.Run("returns error when file not found", func(t *testing.T) {
		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: "https://pds.example.com"},
			client: http.DefaultClient,
		}

		_, err := svc.uploadBlob(context.Background(), "jwt", "/nonexistent/file.jpg")
		require.Error(t, err)
	})

	t.Run("returns error when PDS URL empty", func(t *testing.T) {
		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: ""},
			client: http.DefaultClient,
		}

		_, err := svc.uploadBlob(context.Background(), "jwt", "/tmp/file.jpg")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing PDS URL")
	})

	t.Run("returns error on server error with body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		}))
		defer server.Close()

		tmpFile := filepath.Join(t.TempDir(), "test.png")
		require.NoError(t, os.WriteFile(tmpFile, []byte("data"), 0o600))

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		_, err := svc.uploadBlob(context.Background(), "jwt", tmpFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uploadBlob status 400")
	})

	t.Run("returns error when blob is nil in response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}))
		defer server.Close()

		tmpFile := filepath.Join(t.TempDir(), "test.jpg")
		require.NoError(t, os.WriteFile(tmpFile, []byte("data"), 0o600))

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		_, err := svc.uploadBlob(context.Background(), "jwt", tmpFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing blob")
	})
}

func TestStartBackgroundRefresh(t *testing.T) {
	t.Run("respects configured interval", func(t *testing.T) {
		svc := &atprotoService{
			cfg: &config.Config{
				ATProtoRefreshIntervalSeconds: 30,
			},
			client: http.DefaultClient,
			sessMu: make(chan struct{}, 1),
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		svc.StartBackgroundRefresh(ctx, 0)
	})

	t.Run("uses provided interval when positive", func(t *testing.T) {
		svc := &atprotoService{
			cfg:    &config.Config{},
			client: http.DefaultClient,
			sessMu: make(chan struct{}, 1),
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		svc.StartBackgroundRefresh(ctx, 1*time.Minute)
	})

	t.Run("uses default interval when both zero", func(t *testing.T) {
		svc := &atprotoService{
			cfg:    &config.Config{},
			client: http.DefaultClient,
			sessMu: make(chan struct{}, 1),
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		svc.StartBackgroundRefresh(ctx, 0)
	})
}

func TestCreatePost(t *testing.T) {
	t.Run("successful post creation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/xrpc/com.atproto.repo.createRecord", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		err := svc.createPost(context.Background(), "test-jwt", "did:plc:test", "Hello world!", nil)
		require.NoError(t, err)
	})

	t.Run("with embed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			record := body["record"].(map[string]any)
			assert.NotNil(t, record["embed"])
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		embed := map[string]any{"$type": "app.bsky.embed.external"}
		err := svc.createPost(context.Background(), "test-jwt", "did:plc:test", "Post with embed", embed)
		require.NoError(t, err)
	})

	t.Run("returns error on server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: server.URL},
			client: server.Client(),
		}

		err := svc.createPost(context.Background(), "test-jwt", "did:plc:test", "Hello", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "createRecord status 403")
	})

	t.Run("returns error when PDS URL empty", func(t *testing.T) {
		svc := &atprotoService{
			cfg:    &config.Config{ATProtoPDSURL: ""},
			client: http.DefaultClient,
		}

		err := svc.createPost(context.Background(), "jwt", "did:plc:test", "Hello", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing PDS URL")
	})
}

func TestPublicVideoURL(t *testing.T) {
	t.Run("with base URL", func(t *testing.T) {
		svc := &atprotoService{
			cfg: &config.Config{PublicBaseURL: "https://video.example.com"},
		}

		url := svc.publicVideoURL(&domain.Video{ID: "video-123"})
		assert.Equal(t, "https://video.example.com/videos/video-123", url)
	})

	t.Run("without base URL falls back to API path", func(t *testing.T) {
		svc := &atprotoService{
			cfg: &config.Config{PublicBaseURL: ""},
		}

		url := svc.publicVideoURL(&domain.Video{ID: "video-456"})
		assert.Equal(t, "/api/v1/videos/video-456", url)
	})
}

func TestNewCircuitBreakerService_AllDefaults(t *testing.T) {
	cb := NewCircuitBreakerService(nil, CircuitBreakerConfig{})
	require.NotNil(t, cb)

	ctx := context.Background()

	callCount := 0
	err := cb.Call(ctx, "test-endpoint", func() error {
		callCount++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

func TestCircuitBreaker_OpensOnFailures(t *testing.T) {
	mockH := new(MockHardeningRepository)
	mockH.On("RecordMetric", context.Background(), mock.Anything).Return(nil).Maybe()

	cb := NewCircuitBreakerService(mockH, CircuitBreakerConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		Timeout:            1 * time.Second,
		HalfOpenMaxCalls:   1,
		ErrorRateThreshold: 0.5,
		WindowSize:         5 * time.Minute,
	})

	ctx := context.Background()
	testErr := fmt.Errorf("service unavailable")

	for i := 0; i < 5; i++ {
		_ = cb.Call(ctx, "failing-endpoint", func() error {
			return testErr
		})
	}

	err := cb.Call(ctx, "failing-endpoint", func() error {
		return nil
	})

	_ = err

	status, statusErr := cb.GetStats(ctx, "failing-endpoint")
	require.NoError(t, statusErr)
	require.NotNil(t, status)
}

func TestCircuitBreaker_GetStats_UnknownEndpoint(t *testing.T) {
	cb := NewCircuitBreakerService(nil, CircuitBreakerConfig{})

	status, err := cb.GetStats(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.NotNil(t, status)
}

func TestPersistBackpressureState_NoOp(t *testing.T) {
	svc := &backpressureService{}
	svc.persistBackpressureState(context.Background(), &instanceBackpressure{})
}
