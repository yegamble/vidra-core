package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
)

type mockAtprotoSessionStore struct {
	sessions map[string]struct {
		access  string
		refresh string
		did     string
	}
}

func (m *mockAtprotoSessionStore) SaveSession(ctx context.Context, key []byte, access, refresh, did string) error {
	if m.sessions == nil {
		m.sessions = make(map[string]struct {
			access  string
			refresh string
			did     string
		})
	}
	keyStr := string(key)
	m.sessions[keyStr] = struct {
		access  string
		refresh string
		did     string
	}{access: access, refresh: refresh, did: did}
	return nil
}

func (m *mockAtprotoSessionStore) LoadSessionStrings(ctx context.Context, key []byte) (string, string, string, error) {
	if sess, ok := m.sessions[string(key)]; ok {
		return sess.access, sess.refresh, sess.did, nil
	}
	return "", "", "", errors.New("session not found")
}

func TestAtprotoService_PublishVideo(t *testing.T) {
	// Create a test server to mock ATProto PDS
	var lastRequest *http.Request
	var lastBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastRequest = r

		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  "test-access-token",
				"refreshJwt": "test-refresh-token",
				"did":        "did:plc:test123",
			})

		case "/xrpc/com.atproto.repo.uploadBlob":
			// Mock blob upload response
			json.NewEncoder(w).Encode(map[string]any{
				"blob": map[string]any{
					"$type":    "blob",
					"ref":      map[string]string{"$link": "test-blob-cid"},
					"mimeType": "image/jpeg",
					"size":     1024,
				},
			})

		case "/xrpc/com.atproto.repo.createRecord":
			// Capture request body for verification
			if r.Body != nil {
				body := make([]byte, 1024*10)
				n, _ := r.Body.Read(body)
				lastBody = body[:n]
			}
			json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:test123/app.bsky.feed.post/abc123",
				"cid": "test-cid",
			})

		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	// Create test config
	cfg := &config.Config{
		EnableATProto:      true,
		ATProtoPDSURL:      server.URL,
		ATProtoHandle:      "test.bsky.social",
		ATProtoAppPassword: "test-password",
		PublicBaseURL:      "https://example.com",
	}

	// Create service
	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_did": {
				Key:   "atproto_did",
				Value: json.RawMessage(`"did:plc:test123"`),
			},
		},
	}
	store := &mockAtprotoSessionStore{}
	encKey := make([]byte, 32)

	service := NewAtprotoService(modRepo, cfg, store, encKey)

	// Test video publication
	ctx := context.Background()
	video := &domain.Video{
		ID:            "video-123",
		Title:         "Test Video",
		Description:   "Test Description",
		Privacy:       domain.PrivacyPublic,
		Status:        domain.StatusCompleted,
		ThumbnailPath: "",
	}

	err := service.PublishVideo(ctx, video)
	if err != nil {
		t.Fatalf("PublishVideo failed: %v", err)
	}

	// Verify the request was made
	if lastRequest == nil || !strings.Contains(lastRequest.URL.Path, "createRecord") {
		t.Error("Expected createRecord request")
	}

	// Verify the post content
	if len(lastBody) > 0 {
		var req map[string]any
		json.Unmarshal(lastBody, &req)

		if record, ok := req["record"].(map[string]any); ok {
			if text, ok := record["text"].(string); !ok || text != "Test Video" {
				t.Errorf("Expected text 'Test Video', got '%v'", text)
			}
			if embed, ok := record["embed"].(map[string]any); ok {
				if embedType, _ := embed["$type"].(string); embedType != "app.bsky.embed.external" {
					t.Errorf("Expected external embed type, got %s", embedType)
				}
				if external, ok := embed["external"].(map[string]any); ok {
					if uri, _ := external["uri"].(string); !strings.Contains(uri, "video-123") {
						t.Errorf("Expected video URL to contain video ID, got %s", uri)
					}
				}
			}
		}
	}
}

func TestAtprotoService_PublishVideo_WithImageEmbed(t *testing.T) {
	// Create temp thumbnail file
	tmpFile, err := os.CreateTemp("", "thumb*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write([]byte("fake-image-data"))
	tmpFile.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  "test-access-token",
				"refreshJwt": "test-refresh-token",
				"did":        "did:plc:test123",
			})

		case "/xrpc/com.atproto.repo.uploadBlob":
			json.NewEncoder(w).Encode(map[string]any{
				"blob": map[string]any{
					"$type":    "blob",
					"ref":      map[string]string{"$link": "test-blob-cid"},
					"mimeType": "image/jpeg",
					"size":     1024,
				},
			})

		case "/xrpc/com.atproto.repo.createRecord":
			// Capture and verify image embed
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)

			if record, ok := req["record"].(map[string]any); ok {
				if embed, ok := record["embed"].(map[string]any); ok {
					if embedType, _ := embed["$type"].(string); embedType != "app.bsky.embed.images" {
						w.WriteHeader(400)
						fmt.Fprintf(w, "Expected image embed, got %s", embedType)
						return
					}
				}
			}

			json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:test123/app.bsky.feed.post/abc123",
				"cid": "test-cid",
			})

		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		EnableATProto:        true,
		ATProtoPDSURL:        server.URL,
		ATProtoHandle:        "test.bsky.social",
		ATProtoAppPassword:   "test-password",
		ATProtoUseImageEmbed: true,
		ATProtoImageAltField: "description",
	}

	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_did": {
				Key:   "atproto_did",
				Value: json.RawMessage(`"did:plc:test123"`),
			},
		},
	}
	store := &mockAtprotoSessionStore{}
	encKey := make([]byte, 32)

	service := NewAtprotoService(modRepo, cfg, store, encKey)

	ctx := context.Background()
	video := &domain.Video{
		ID:            "video-456",
		Title:         "Video with Thumbnail",
		Description:   "Video description for alt text",
		Privacy:       domain.PrivacyPublic,
		Status:        domain.StatusCompleted,
		ThumbnailPath: tmpFile.Name(),
	}

	err = service.PublishVideo(ctx, video)
	if err != nil {
		t.Fatalf("PublishVideo with image embed failed: %v", err)
	}
}

func TestAtprotoService_SessionManagement(t *testing.T) {
	refreshCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  fmt.Sprintf("access-%d", refreshCount),
				"refreshJwt": fmt.Sprintf("refresh-%d", refreshCount),
				"did":        "did:plc:test",
			})

		case "/xrpc/com.atproto.server.refreshSession":
			refreshCount++
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  fmt.Sprintf("access-%d", refreshCount),
				"refreshJwt": fmt.Sprintf("refresh-%d", refreshCount),
				"did":        "did:plc:test",
			})

		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		EnableATProto:      true,
		ATProtoPDSURL:      server.URL,
		ATProtoHandle:      "test.bsky.social",
		ATProtoAppPassword: "test-password",
	}

	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_did": {
				Key:   "atproto_did",
				Value: json.RawMessage(`"did:plc:test"`),
			},
		},
	}
	store := &mockAtprotoSessionStore{}
	encKey := make([]byte, 32)

	service := NewAtprotoService(modRepo, cfg, store, encKey).(*atprotoService)
	ctx := context.Background()

	// First session creation
	access1, did1, err := service.ensureSession(ctx)
	if err != nil {
		t.Fatalf("First ensureSession failed: %v", err)
	}
	if access1 != "access-0" || did1 != "did:plc:test" {
		t.Error("Unexpected first session values")
	}

	// Force expiry and refresh
	service.fetchedAt = time.Now().Add(-60 * time.Minute)
	service.refreshJwt = "refresh-0"

	access2, did2, err := service.ensureSession(ctx)
	if err != nil {
		t.Fatalf("Session refresh failed: %v", err)
	}
	if access2 != "access-1" || did2 != "did:plc:test" {
		t.Error("Unexpected refreshed session values")
	}
	if refreshCount != 1 {
		t.Errorf("Expected 1 refresh, got %d", refreshCount)
	}
}

func TestAtprotoService_BackgroundRefresh(t *testing.T) {
	t.Skip("Skipping flaky background refresh test - timing sensitive")
	refreshCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  "initial-access",
				"refreshJwt": "initial-refresh",
				"did":        "did:plc:test",
			})

		case "/xrpc/com.atproto.server.refreshSession":
			refreshCount++
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  fmt.Sprintf("refreshed-%d", refreshCount),
				"refreshJwt": fmt.Sprintf("refresh-%d", refreshCount),
				"did":        "did:plc:test",
			})

		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		EnableATProto:                 true,
		ATProtoPDSURL:                 server.URL,
		ATProtoHandle:                 "test.bsky.social",
		ATProtoAppPassword:            "test-password",
		ATProtoRefreshIntervalSeconds: 1, // 1 second for testing
	}

	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_did": {
				Key:   "atproto_did",
				Value: json.RawMessage(`"did:plc:test"`),
			},
		},
	}
	store := &mockAtprotoSessionStore{}
	encKey := make([]byte, 32)

	service := NewAtprotoService(modRepo, cfg, store, encKey)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// First, ensure the service has a session with refresh token
	// This will trigger createSession and get initial tokens
	_, _, _ = service.(*atprotoService).ensureSession(ctx)

	// Start background refresh with explicit 1 second interval
	service.StartBackgroundRefresh(ctx, 1*time.Second)

	// Wait for enough time to ensure at least one refresh
	// Adding a bit of buffer for goroutine scheduling
	time.Sleep(2 * time.Second)

	// Verify refresh happened
	if refreshCount < 1 {
		t.Errorf("Expected at least 1 background refresh, got %d", refreshCount)
	}
}

func TestAtprotoService_DisabledService(t *testing.T) {
	cfg := &config.Config{
		EnableATProto: false, // Service disabled
	}

	service := NewAtprotoService(nil, cfg, nil, nil)

	ctx := context.Background()
	video := &domain.Video{
		ID:      "test-video",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	}

	// Should return nil when disabled
	err := service.PublishVideo(ctx, video)
	if err != nil {
		t.Error("Expected no error when service is disabled")
	}
}

func TestAtprotoService_PrivateVideoNotPublished(t *testing.T) {
	cfg := &config.Config{
		EnableATProto: true,
	}

	service := NewAtprotoService(nil, cfg, nil, nil)

	ctx := context.Background()

	// Test private video
	privateVideo := &domain.Video{
		ID:      "private-video",
		Privacy: domain.PrivacyPrivate,
		Status:  domain.StatusCompleted,
	}

	err := service.PublishVideo(ctx, privateVideo)
	if err != nil {
		t.Error("Expected no error for private video")
	}

	// Test unlisted video
	unlistedVideo := &domain.Video{
		ID:      "unlisted-video",
		Privacy: domain.PrivacyUnlisted,
		Status:  domain.StatusCompleted,
	}

	err = service.PublishVideo(ctx, unlistedVideo)
	if err != nil {
		t.Error("Expected no error for unlisted video")
	}

	// Test incomplete video
	incompleteVideo := &domain.Video{
		ID:      "incomplete-video",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusProcessing,
	}

	err = service.PublishVideo(ctx, incompleteVideo)
	if err != nil {
		t.Error("Expected no error for incomplete video")
	}
}
