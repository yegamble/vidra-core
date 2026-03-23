// Package integration contains integration tests for the ATProto publisher service.
// These tests use an in-process mock PDS (httptest.NewServer) so they
// do NOT require Docker services. The TEST_INTEGRATION guard is applied to
// tests that exercise the full service lifecycle.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/usecase"
)

// ── In-process mock PDS ───────────────────────────────────────────────────────

type mockPDSState struct {
	mu            sync.Mutex
	accessTokens  map[string]string // token -> did
	refreshTokens map[string]string // token -> did
	records       []map[string]interface{}
	blobs         []map[string]interface{}
}

func newMockPDSState() *mockPDSState {
	return &mockPDSState{
		accessTokens:  make(map[string]string),
		refreshTokens: make(map[string]string),
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newMockPDS(t *testing.T) (*httptest.Server, *mockPDSState) {
	t.Helper()
	state := newMockPDSState()

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		access := "access-" + randomHex(16)
		refresh := "refresh-" + randomHex(16)
		did := "did:plc:test123"
		state.mu.Lock()
		state.accessTokens[access] = did
		state.refreshTokens[refresh] = did
		state.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"accessJwt":  access,
			"refreshJwt": refresh,
			"did":        did,
			"handle":     "test.handle",
		})
	})

	mux.HandleFunc("/xrpc/com.atproto.server.refreshSession", func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		state.mu.Lock()
		did, ok := state.refreshTokens[auth]
		if ok {
			delete(state.refreshTokens, auth)
		}
		state.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"invalid refresh token"}`, http.StatusUnauthorized)
			return
		}
		newAccess := "access-" + randomHex(16)
		newRefresh := "refresh-" + randomHex(16)
		state.mu.Lock()
		state.accessTokens[newAccess] = did
		state.refreshTokens[newRefresh] = did
		state.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"accessJwt":  newAccess,
			"refreshJwt": newRefresh,
			"did":        did,
		})
	})

	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		state.mu.Lock()
		_, ok := state.accessTokens[auth]
		state.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		var record map[string]interface{}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
		json.Unmarshal(body, &record)
		state.mu.Lock()
		state.records = append(state.records, record)
		state.mu.Unlock()

		cid := "bafy" + randomHex(12)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"uri": "at://did:plc:test123/app.bsky.feed.post/" + randomHex(8),
			"cid": cid,
		})
	})

	mux.HandleFunc("/xrpc/com.atproto.repo.uploadBlob", func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		state.mu.Lock()
		_, ok := state.accessTokens[auth]
		state.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
		cid := "bafyb" + randomHex(12)
		mimeType := r.Header.Get("Content-Type")
		state.mu.Lock()
		state.blobs = append(state.blobs, map[string]interface{}{
			"cid": cid, "size": len(body), "mimeType": mimeType,
		})
		state.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"blob": map[string]interface{}{
				"$type":    "blob",
				"ref":      map[string]string{"$link": cid},
				"mimeType": mimeType,
				"size":     len(body),
			},
		})
	})

	mux.HandleFunc("/xrpc/app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"feed": []interface{}{}, "cursor": ""})
	})

	// getRecord — return stored record or synthetic response
	mux.HandleFunc("/xrpc/com.atproto.repo.getRecord", func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		collection := r.URL.Query().Get("collection")
		rkey := r.URL.Query().Get("rkey")
		targetURI := "at://" + repo + "/" + collection + "/" + rkey
		state.mu.Lock()
		var foundRecord map[string]interface{}
		var foundCID string
		for _, rec := range state.records {
			if rec["_uri"] == targetURI {
				foundRecord = rec
				if c, ok := rec["_cid"].(string); ok {
					foundCID = c
				}
				break
			}
		}
		state.mu.Unlock()
		cid := foundCID
		if cid == "" {
			cid = "bafyreid" + randomHex(12)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"uri":   targetURI,
			"cid":   cid,
			"value": foundRecord,
		})
	})

	// resolveHandle — returns a DID for any handle
	mux.HandleFunc("/xrpc/com.atproto.identity.resolveHandle", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"did": "did:plc:test123",
		})
	})

	// getPostThread — returns thread view
	mux.HandleFunc("/xrpc/app.bsky.feed.getPostThread", func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		state.mu.Lock()
		replies := []interface{}{}
		for _, rec := range state.records {
			if record, ok := rec["record"].(map[string]interface{}); ok {
				if reply, ok := record["reply"].(map[string]interface{}); ok {
					if parent, ok := reply["parent"].(map[string]interface{}); ok {
						if parentURI, _ := parent["uri"].(string); parentURI == uri {
							replies = append(replies, map[string]interface{}{
								"$type": "app.bsky.feed.defs#threadViewPost",
								"post":  map[string]interface{}{"uri": uri, "cid": "bafyreid" + randomHex(6)},
							})
						}
					}
				}
			}
		}
		state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"thread": map[string]interface{}{
				"$type":   "app.bsky.feed.defs#threadViewPost",
				"post":    map[string]interface{}{"uri": uri, "cid": "bafyreid" + randomHex(6)},
				"replies": replies,
			},
		})
	})

	// deleteRecord — removes a record
	mux.HandleFunc("/xrpc/com.atproto.repo.deleteRecord", func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		state.mu.Lock()
		_, ok := state.accessTokens[auth]
		state.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, state
}

// ── Test helpers ─────────────────────────────────────────────────────────────

type inMemSessionStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newInMemSessionStore() *inMemSessionStore {
	return &inMemSessionStore{data: make(map[string][]byte)}
}

func (s *inMemSessionStore) SaveSession(_ context.Context, key []byte, access, refresh, did string) error {
	entry, _ := json.Marshal(map[string]string{"access": access, "refresh": refresh, "did": did})
	s.mu.Lock()
	s.data[string(key)] = entry
	s.mu.Unlock()
	return nil
}

func (s *inMemSessionStore) LoadSessionStrings(_ context.Context, key []byte) (string, string, string, error) {
	s.mu.Lock()
	entry, ok := s.data[string(key)]
	s.mu.Unlock()
	if !ok {
		return "", "", "", nil
	}
	var m map[string]string
	json.Unmarshal(entry, &m)
	return m["access"], m["refresh"], m["did"], nil
}

type staticCfgReader struct{ pdsURL string }

func (r *staticCfgReader) GetInstanceConfig(_ context.Context, key string) (*domain.InstanceConfig, error) {
	if key == "atproto_pds_url" {
		val, _ := json.Marshal(r.pdsURL)
		return &domain.InstanceConfig{Key: key, Value: json.RawMessage(val)}, nil
	}
	return nil, domain.ErrNotFound
}

func newATProtoService(pdsURL string, store usecase.AtprotoSessionStore) usecase.AtprotoPublisher {
	cfg := &config.Config{
		EnableATProto:      true,
		ATProtoPDSURL:      pdsURL,
		ATProtoHandle:      "test.handle",
		ATProtoAppPassword: "test-app-password",
		PublicBaseURL:      "https://example.com",
	}
	return usecase.NewAtprotoService(&staticCfgReader{pdsURL: pdsURL}, cfg, store, make([]byte, 32))
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestAtprotoService_PublishVideo verifies that PublishVideo creates a record on the mock PDS.
func TestAtprotoService_PublishVideo(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	video := &domain.Video{
		ID:          uuid.New().String(),
		Title:       "Test Video",
		Description: "Integration test video",
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
	}

	err := service.PublishVideo(ctx, video)
	require.NoError(t, err, "PublishVideo should succeed against mock PDS")

	// Verify a record was created
	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 1, recordCount, "expected 1 record created in mock PDS")
}

// TestAtprotoService_DisabledMode verifies PublishVideo is a no-op when disabled.
func TestAtprotoService_DisabledMode(t *testing.T) {
	ts, state := newMockPDS(t)

	cfg := &config.Config{
		EnableATProto: false,
		ATProtoPDSURL: ts.URL,
	}
	store := newInMemSessionStore()
	service := usecase.NewAtprotoService(&staticCfgReader{pdsURL: ts.URL}, cfg, store, make([]byte, 32))

	ctx := context.Background()
	video := &domain.Video{
		ID:      uuid.New().String(),
		Title:   "Test Video",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	}

	err := service.PublishVideo(ctx, video)
	assert.NoError(t, err, "PublishVideo with EnableATProto=false should be a no-op")

	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 0, recordCount, "no records should be created when ATProto is disabled")
}

// TestAtprotoService_PrivateVideoSkipped verifies private videos are not published.
func TestAtprotoService_PrivateVideoSkipped(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	err := service.PublishVideo(ctx, &domain.Video{
		ID:      uuid.New().String(),
		Title:   "Private Video",
		Privacy: domain.PrivacyPrivate,
		Status:  domain.StatusCompleted,
	})
	assert.NoError(t, err, "PublishVideo for private video should be no-op")

	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 0, recordCount, "private video should not create a PDS record")
}

// TestAtprotoService_ThumbnailUpload verifies blob upload when a thumbnail file exists.
func TestAtprotoService_ThumbnailUpload(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	// Create a temp thumbnail file
	tmpDir := t.TempDir()
	thumbPath := filepath.Join(tmpDir, "thumb.jpg")
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}
	require.NoError(t, os.WriteFile(thumbPath, jpegHeader, 0644))

	ctx := context.Background()
	err := service.PublishVideo(ctx, &domain.Video{
		ID:            uuid.New().String(),
		Title:         "Video with Thumbnail",
		Privacy:       domain.PrivacyPublic,
		Status:        domain.StatusCompleted,
		ThumbnailPath: thumbPath,
	})
	require.NoError(t, err, "PublishVideo with thumbnail should succeed")

	// Should have both a blob upload and a record
	state.mu.Lock()
	blobCount := len(state.blobs)
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 1, blobCount, "expected 1 blob uploaded for thumbnail")
	assert.Equal(t, 1, recordCount, "expected 1 record created")
}

// TestAtprotoService_PendingVideoSkipped verifies incomplete videos are not published.
func TestAtprotoService_PendingVideoSkipped(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	err := service.PublishVideo(ctx, &domain.Video{
		ID:      uuid.New().String(),
		Title:   "Processing Video",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusProcessing, // Not yet completed
	})
	assert.NoError(t, err, "PublishVideo for pending video should be no-op")

	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 0, recordCount, "pending video should not create a PDS record")
}

// TestAtprotoService_StartBackgroundRefresh verifies the background refresh can be cancelled.
func TestAtprotoService_StartBackgroundRefresh(t *testing.T) {
	ts, _ := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		service.StartBackgroundRefresh(ctx, 50*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		// OK — goroutine exited cleanly
	case <-time.After(500 * time.Millisecond):
		t.Error("StartBackgroundRefresh didn't exit after context cancellation")
	}
}

// ── New feature integration tests ────────────────────────────────────────────

// TestAtprotoService_PublishComment verifies comment syndication as a reply.
func TestAtprotoService_PublishComment(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()

	// First publish a video to get a parent post URI
	video := &domain.Video{
		ID:      uuid.New().String(),
		Title:   "Video for comments",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	}
	err := service.PublishVideo(ctx, video)
	require.NoError(t, err)

	// Now publish a comment as a reply
	comment := &domain.Comment{
		ID:      uuid.New(),
		VideoID: uuid.MustParse(video.ID),
		Body:    "This is a great video! Really enjoyed the content.",
		Status:  domain.CommentStatusActive,
	}

	ref, err := service.PublishComment(ctx, comment, video, "at://did:plc:test123/app.bsky.feed.post/parentrkey")
	require.NoError(t, err, "PublishComment should succeed")
	require.NotNil(t, ref)
	assert.NotEmpty(t, ref.URI, "returned URI should not be empty")
	assert.NotEmpty(t, ref.CID, "returned CID should not be empty")

	// Verify 2 records were created (1 video + 1 comment)
	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 2, recordCount, "expected 2 records: 1 video post + 1 comment reply")

	// Verify the comment record has reply structure
	state.mu.Lock()
	lastRecord := state.records[len(state.records)-1]
	state.mu.Unlock()

	record, ok := lastRecord["record"].(map[string]interface{})
	require.True(t, ok)
	_, hasReply := record["reply"]
	assert.True(t, hasReply, "comment record should have reply field")
}

// TestAtprotoService_PublishComment_InactiveSkipped verifies deleted comments are not syndicated.
func TestAtprotoService_PublishComment_InactiveSkipped(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	comment := &domain.Comment{
		ID:     uuid.New(),
		Body:   "Deleted comment",
		Status: domain.CommentStatusDeleted,
	}
	video := &domain.Video{ID: uuid.New().String(), Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted}

	ref, err := service.PublishComment(ctx, comment, video, "at://did:plc:test123/app.bsky.feed.post/x")
	assert.NoError(t, err)
	assert.Nil(t, ref, "inactive comment should return nil ref")

	state.mu.Lock()
	count := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 0, count, "no records should be created for inactive comments")
}

// TestAtprotoService_PublishVideoBatch verifies batch video syndication.
func TestAtprotoService_PublishVideoBatch(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	videos := []*domain.Video{
		{ID: uuid.New().String(), Title: "Batch Video 1", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: uuid.New().String(), Title: "Batch Video 2", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: uuid.New().String(), Title: "Batch Video 3", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: uuid.New().String(), Title: "Private - Skip", Privacy: domain.PrivacyPrivate, Status: domain.StatusCompleted},
	}

	results := service.PublishVideoBatch(ctx, videos)
	assert.Len(t, results, 4)

	for _, r := range results {
		assert.NoError(t, r.Err, "batch item %s should not error", r.VideoID)
	}

	// Only 3 public videos should create records
	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 3, recordCount, "expected 3 records for 3 public videos")
}

// TestAtprotoService_ResolveHandle verifies handle resolution.
func TestAtprotoService_ResolveHandle(t *testing.T) {
	ts, _ := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	identity, err := service.ResolveHandle(ctx, "alice.bsky.social")
	require.NoError(t, err)
	require.NotNil(t, identity)
	assert.Equal(t, "did:plc:test123", identity.DID)
	assert.Equal(t, "alice.bsky.social", identity.Handle)
}

// TestAtprotoService_ResolveHandle_WithAtPrefix verifies @ prefix is stripped.
func TestAtprotoService_ResolveHandle_WithAtPrefix(t *testing.T) {
	ts, _ := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()
	identity, err := service.ResolveHandle(ctx, "@bob.bsky.social")
	require.NoError(t, err)
	require.NotNil(t, identity)
	assert.Equal(t, "bob.bsky.social", identity.Handle)
}

// TestAtprotoService_AutoSyncEnabled verifies the auto-sync flag.
func TestAtprotoService_AutoSyncEnabled(t *testing.T) {
	ts, _ := newMockPDS(t)

	t.Run("disabled by default", func(t *testing.T) {
		store := newInMemSessionStore()
		service := newATProtoService(ts.URL, store)
		assert.False(t, service.AutoSyncEnabled())
	})

	t.Run("enabled when configured", func(t *testing.T) {
		cfg := &config.Config{
			EnableATProto:          true,
			ATProtoPDSURL:          ts.URL,
			ATProtoHandle:          "test.handle",
			ATProtoAppPassword:     "test-app-password",
			ATProtoAutoSyncEnabled: true,
		}
		store := newInMemSessionStore()
		service := usecase.NewAtprotoService(&staticCfgReader{pdsURL: ts.URL}, cfg, store, make([]byte, 32))
		assert.True(t, service.AutoSyncEnabled())
	})
}

// TestAtprotoService_EndToEnd_VideoAndComment exercises the full flow:
// publish a video, then publish a comment as a reply, verify both records exist.
func TestAtprotoService_EndToEnd_VideoAndComment(t *testing.T) {
	ts, state := newMockPDS(t)
	store := newInMemSessionStore()
	service := newATProtoService(ts.URL, store)

	ctx := context.Background()

	// Step 1: Publish video
	video := &domain.Video{
		ID:          uuid.New().String(),
		Title:       "E2E Test Video",
		Description: "Full flow test",
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
	}
	err := service.PublishVideo(ctx, video)
	require.NoError(t, err)

	// Step 2: Publish 3 comments
	for i := 0; i < 3; i++ {
		comment := &domain.Comment{
			ID:      uuid.New(),
			VideoID: uuid.MustParse(video.ID),
			Body:    "Comment " + uuid.New().String()[:8],
			Status:  domain.CommentStatusActive,
		}
		ref, err := service.PublishComment(ctx, comment, video, "at://did:plc:test123/app.bsky.feed.post/video1")
		require.NoError(t, err)
		assert.NotNil(t, ref)
	}

	// Step 3: Verify total records: 1 video + 3 comments = 4
	state.mu.Lock()
	recordCount := len(state.records)
	state.mu.Unlock()
	assert.Equal(t, 4, recordCount, "expected 4 records: 1 video + 3 comments")
}

// TestAtprotoService_DockerMockPDS exercises the Docker-hosted mock PDS
// when the test-integration profile is running.
func TestAtprotoService_DockerMockPDS(t *testing.T) {
	pdsURL := os.Getenv("ATPROTO_PDS_URL")
	if pdsURL == "" {
		pdsURL = "http://localhost:19200"
	}

	// Check if Docker mock PDS is available
	resp, err := http.Get(pdsURL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skip("Docker mock ATProto PDS not available, skipping Docker integration tests")
	}
	resp.Body.Close()

	store := newInMemSessionStore()
	cfg := &config.Config{
		EnableATProto:          true,
		ATProtoPDSURL:          pdsURL,
		ATProtoHandle:          "test.handle",
		ATProtoAppPassword:     "test-app-password",
		PublicBaseURL:          "https://example.com",
		ATProtoAutoSyncEnabled: true,
	}
	service := usecase.NewAtprotoService(&staticCfgReader{pdsURL: pdsURL}, cfg, store, make([]byte, 32))

	ctx := context.Background()

	t.Run("PublishVideo", func(t *testing.T) {
		video := &domain.Video{
			ID:          uuid.New().String(),
			Title:       "Docker PDS Test Video",
			Description: "Published via Docker mock PDS",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
		}
		err := service.PublishVideo(ctx, video)
		require.NoError(t, err)

		// Verify via debug endpoint
		resp, err := http.Get(pdsURL + "/test/records")
		require.NoError(t, err)
		defer resp.Body.Close()
		var records []map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&records))
		assert.GreaterOrEqual(t, len(records), 1, "at least 1 record should exist in Docker PDS")
	})

	t.Run("PublishComment", func(t *testing.T) {
		comment := &domain.Comment{
			ID:     uuid.New(),
			Body:   "Comment via Docker PDS",
			Status: domain.CommentStatusActive,
		}
		video := &domain.Video{ID: uuid.New().String(), Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted}

		ref, err := service.PublishComment(ctx, comment, video, "at://did:plc:test123/app.bsky.feed.post/dockertest")
		require.NoError(t, err)
		assert.NotNil(t, ref)
	})

	t.Run("ResolveHandle", func(t *testing.T) {
		identity, err := service.ResolveHandle(ctx, "test.bsky.social")
		require.NoError(t, err)
		assert.NotEmpty(t, identity.DID)
	})

	t.Run("BatchPublish", func(t *testing.T) {
		videos := []*domain.Video{
			{ID: uuid.New().String(), Title: "Batch 1", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
			{ID: uuid.New().String(), Title: "Batch 2", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		}
		results := service.PublishVideoBatch(ctx, videos)
		assert.Len(t, results, 2)
		for _, r := range results {
			assert.NoError(t, r.Err)
		}
	})

	t.Run("AutoSyncEnabled", func(t *testing.T) {
		assert.True(t, service.AutoSyncEnabled())
	})
}
