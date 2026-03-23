package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestATProtoService creates a test service pointing at the given mock server.
func newTestATProtoService(t *testing.T, serverURL string, opts ...func(*config.Config)) *atprotoService {
	t.Helper()
	cfg := &config.Config{
		EnableATProto:      true,
		ATProtoPDSURL:      serverURL,
		ATProtoHandle:      "test.bsky.social",
		ATProtoAppPassword: "test-password",
		PublicBaseURL:      "https://example.com",
		ATProtoMaxRetries:  1,
	}
	for _, opt := range opts {
		opt(cfg)
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

	svc := NewAtprotoService(modRepo, cfg, store, encKey).(*atprotoService)
	// Override retry with fast settings for tests
	svc.retry = retryConfig{maxRetries: 1, baseDelay: time.Millisecond}
	return svc
}

// newMockPDSForFeatures creates a full mock PDS that handles all endpoints for feature testing.
func newMockPDSForFeatures(t *testing.T) *httptest.Server {
	t.Helper()
	var records []map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt":  "test-access-token",
				"refreshJwt": "test-refresh-token",
				"did":        "did:plc:test123",
			})

		case r.URL.Path == "/xrpc/com.atproto.repo.createRecord":
			var req map[string]interface{}
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &req)
			records = append(records, req)
			json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:test123/app.bsky.feed.post/abc123",
				"cid": "bafyreidtest123",
			})

		case r.URL.Path == "/xrpc/com.atproto.repo.getRecord":
			uri := fmt.Sprintf("at://%s/%s/%s",
				r.URL.Query().Get("repo"),
				r.URL.Query().Get("collection"),
				r.URL.Query().Get("rkey"),
			)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"uri":   uri,
				"cid":   "bafyreidparent456",
				"value": map[string]interface{}{},
			})

		case r.URL.Path == "/xrpc/com.atproto.identity.resolveHandle":
			handle := r.URL.Query().Get("handle")
			json.NewEncoder(w).Encode(map[string]string{
				"did": "did:plc:resolved789",
			})
			_ = handle

		case r.URL.Path == "/xrpc/app.bsky.feed.getPostThread":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"thread": map[string]interface{}{
					"$type":   "app.bsky.feed.defs#threadViewPost",
					"post":    map[string]interface{}{"uri": r.URL.Query().Get("uri"), "cid": "bafyreidthread"},
					"replies": []interface{}{},
				},
			})

		case r.URL.Path == "/xrpc/com.atproto.repo.deleteRecord":
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/xrpc/com.atproto.repo.uploadBlob":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"blob": map[string]interface{}{
					"$type":    "blob",
					"ref":      map[string]string{"$link": "test-blob-cid"},
					"mimeType": "image/jpeg",
					"size":     1024,
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// ── PublishComment tests ─────────────────────────────────────────────────────

func TestPublishComment_Success(t *testing.T) {
	server := newMockPDSForFeatures(t)
	svc := newTestATProtoService(t, server.URL)

	comment := &domain.Comment{
		ID:     uuid.New(),
		Body:   "Great video!",
		Status: domain.CommentStatusActive,
	}
	video := &domain.Video{
		ID:      "video-123",
		Title:   "Test Video",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	}

	ref, err := svc.PublishComment(context.Background(), comment, video, "at://did:plc:test123/app.bsky.feed.post/abc123")
	require.NoError(t, err)
	require.NotNil(t, ref)
	assert.NotEmpty(t, ref.URI)
	assert.NotEmpty(t, ref.CID)
}

func TestPublishComment_DisabledService(t *testing.T) {
	svc := &atprotoService{enabled: false, cfg: &config.Config{}}

	ref, err := svc.PublishComment(context.Background(), &domain.Comment{}, &domain.Video{}, "at://...")
	assert.NoError(t, err)
	assert.Nil(t, ref)
}

func TestPublishComment_NilInputs(t *testing.T) {
	svc := &atprotoService{enabled: true, cfg: &config.Config{}}

	_, err := svc.PublishComment(context.Background(), nil, &domain.Video{}, "at://...")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")

	_, err = svc.PublishComment(context.Background(), &domain.Comment{}, nil, "at://...")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestPublishComment_EmptyParentURI(t *testing.T) {
	svc := &atprotoService{enabled: true, cfg: &config.Config{}}

	_, err := svc.PublishComment(context.Background(), &domain.Comment{Status: domain.CommentStatusActive}, &domain.Video{}, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parentPostURI is required")
}

func TestPublishComment_InactiveComment(t *testing.T) {
	svc := &atprotoService{enabled: true, cfg: &config.Config{}}

	comment := &domain.Comment{Status: domain.CommentStatusDeleted}
	ref, err := svc.PublishComment(context.Background(), comment, &domain.Video{}, "at://...")
	assert.NoError(t, err)
	assert.Nil(t, ref)
}

func TestPublishComment_ReplyStructure(t *testing.T) {
	var capturedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt": "token", "refreshJwt": "refresh", "did": "did:plc:test123",
			})
		case "/xrpc/com.atproto.repo.getRecord":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"uri": "at://did:plc:test123/app.bsky.feed.post/video1",
				"cid": "bafyreidparent",
			})
		case "/xrpc/com.atproto.repo.createRecord":
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &capturedBody)
			json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:test123/app.bsky.feed.post/reply1",
				"cid": "bafyreidreply",
			})
		}
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)

	comment := &domain.Comment{
		ID:     uuid.New(),
		Body:   "Nice work on this!",
		Status: domain.CommentStatusActive,
	}
	video := &domain.Video{ID: "video-123", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted}

	_, err := svc.PublishComment(context.Background(), comment, video, "at://did:plc:test123/app.bsky.feed.post/video1")
	require.NoError(t, err)

	// Verify reply structure in the created record
	require.NotNil(t, capturedBody)
	record, ok := capturedBody["record"].(map[string]interface{})
	require.True(t, ok, "record should be a map")

	reply, ok := record["reply"].(map[string]interface{})
	require.True(t, ok, "record should contain reply")

	root, ok := reply["root"].(map[string]interface{})
	require.True(t, ok, "reply should contain root")
	assert.Equal(t, "at://did:plc:test123/app.bsky.feed.post/video1", root["uri"])
	assert.Equal(t, "bafyreidparent", root["cid"])

	parent, ok := reply["parent"].(map[string]interface{})
	require.True(t, ok, "reply should contain parent")
	assert.Equal(t, "at://did:plc:test123/app.bsky.feed.post/video1", parent["uri"])
}

// ── PublishVideoBatch tests ──────────────────────────────────────────────────

func TestPublishVideoBatch_Success(t *testing.T) {
	server := newMockPDSForFeatures(t)
	svc := newTestATProtoService(t, server.URL)

	videos := []*domain.Video{
		{ID: "v1", Title: "Video 1", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: "v2", Title: "Video 2", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: "v3", Title: "Video 3", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
	}

	results := svc.PublishVideoBatch(context.Background(), videos)
	assert.Len(t, results, 3)
	for _, r := range results {
		assert.NoError(t, r.Err, "video %s should succeed", r.VideoID)
		require.NotNil(t, r.Ref, "video %s should have a post reference", r.VideoID)
		assert.NotEmpty(t, r.Ref.URI, "video %s should have a non-empty URI", r.VideoID)
		assert.NotEmpty(t, r.Ref.CID, "video %s should have a non-empty CID", r.VideoID)
	}
}

func TestPublishVideoBatch_SkipsIneligible(t *testing.T) {
	server := newMockPDSForFeatures(t)
	svc := newTestATProtoService(t, server.URL)

	videos := []*domain.Video{
		{ID: "v1", Title: "Public", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: "v2", Title: "Private", Privacy: domain.PrivacyPrivate, Status: domain.StatusCompleted},
		nil, // nil entry should be skipped entirely
		{ID: "v4", Title: "Processing", Privacy: domain.PrivacyPublic, Status: domain.StatusProcessing},
	}

	results := svc.PublishVideoBatch(context.Background(), videos)
	// v1 succeeds with ref, v2 and v4 are ineligible (nil ref, nil err), nil is skipped
	assert.Len(t, results, 3) // nil is skipped entirely
	for _, r := range results {
		assert.NoError(t, r.Err)
	}
	// v1 (eligible) should have a ref
	require.NotNil(t, results[0].Ref, "eligible video should have a post reference")
	assert.NotEmpty(t, results[0].Ref.URI)
	// v2 and v4 (ineligible) should have nil ref
	assert.Nil(t, results[1].Ref, "private video should not have a post reference")
	assert.Nil(t, results[2].Ref, "processing video should not have a post reference")
}

func TestPublishVideoBatch_Empty(t *testing.T) {
	svc := &atprotoService{enabled: true, cfg: &config.Config{}}
	results := svc.PublishVideoBatch(context.Background(), nil)
	assert.Empty(t, results)
}

func TestPublishVideoBatch_PartialFailure(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt": "token", "refreshJwt": "refresh", "did": "did:plc:test123",
			})
		case "/xrpc/com.atproto.repo.createRecord":
			callCount++
			if callCount == 2 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:test123/app.bsky.feed.post/x",
				"cid": "bafyreid",
			})
		}
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)

	videos := []*domain.Video{
		{ID: "v1", Title: "OK", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: "v2", Title: "Fail", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
		{ID: "v3", Title: "OK2", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted},
	}

	results := svc.PublishVideoBatch(context.Background(), videos)
	assert.Len(t, results, 3)
	assert.NoError(t, results[0].Err)
	require.NotNil(t, results[0].Ref, "successful publish should have a post reference")
	assert.NotEmpty(t, results[0].Ref.URI)
	assert.Error(t, results[1].Err)
	assert.Nil(t, results[1].Ref, "failed publish should not have a post reference")
	assert.NoError(t, results[2].Err)
	require.NotNil(t, results[2].Ref, "successful publish should have a post reference")
	assert.NotEmpty(t, results[2].Ref.URI)
}

// ── ResolveHandle tests ──────────────────────────────────────────────────────

func TestResolveHandle_Success(t *testing.T) {
	server := newMockPDSForFeatures(t)
	svc := newTestATProtoService(t, server.URL)

	identity, err := svc.ResolveHandle(context.Background(), "alice.bsky.social")
	require.NoError(t, err)
	require.NotNil(t, identity)
	assert.Equal(t, "did:plc:resolved789", identity.DID)
	assert.Equal(t, "alice.bsky.social", identity.Handle)
}

func TestResolveHandle_StripsAtPrefix(t *testing.T) {
	var capturedHandle string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xrpc/com.atproto.identity.resolveHandle" {
			capturedHandle = r.URL.Query().Get("handle")
			json.NewEncoder(w).Encode(map[string]string{"did": "did:plc:test"})
		}
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)
	_, err := svc.ResolveHandle(context.Background(), "@alice.bsky.social")
	require.NoError(t, err)
	assert.Equal(t, "alice.bsky.social", capturedHandle)
}

func TestResolveHandle_EmptyHandle(t *testing.T) {
	svc := &atprotoService{enabled: true, cfg: &config.Config{ATProtoPDSURL: "https://example.com"}}
	_, err := svc.ResolveHandle(context.Background(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "handle is required")
}

func TestResolveHandle_Disabled(t *testing.T) {
	svc := &atprotoService{enabled: false, cfg: &config.Config{}}
	_, err := svc.ResolveHandle(context.Background(), "alice.bsky.social")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestResolveHandle_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"handle not found"}`))
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)
	_, err := svc.ResolveHandle(context.Background(), "nonexistent.bsky.social")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolveHandle status 404")
}

// ── AutoSyncEnabled tests ────────────────────────────────────────────────────

func TestAutoSyncEnabled(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		autoSync bool
		want     bool
	}{
		{"both enabled", true, true, true},
		{"atproto disabled", false, true, false},
		{"autosync disabled", true, false, false},
		{"both disabled", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &atprotoService{
				enabled: tt.enabled,
				cfg:     &config.Config{ATProtoAutoSyncEnabled: tt.autoSync},
			}
			assert.Equal(t, tt.want, svc.AutoSyncEnabled())
		})
	}
}

// ── truncateText tests ───────────────────────────────────────────────────────

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name string
		text string
		max  int
		want string
	}{
		{"under limit", "hello", 300, "hello"},
		{"at limit", strings.Repeat("a", 300), 300, strings.Repeat("a", 300)},
		{"over limit", strings.Repeat("a", 310), 300, strings.Repeat("a", 297) + "..."},
		{"unicode", strings.Repeat("日", 310), 300, strings.Repeat("日", 297) + "..."},
		{"empty", "", 300, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateText(tt.text, tt.max)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ── resolveRecordCID tests ───────────────────────────────────────────────────

func TestResolveRecordCID_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/xrpc/com.atproto.repo.getRecord", r.URL.Path)
		assert.Equal(t, "did:plc:test", r.URL.Query().Get("repo"))
		assert.Equal(t, "app.bsky.feed.post", r.URL.Query().Get("collection"))
		assert.Equal(t, "rkey123", r.URL.Query().Get("rkey"))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"uri": "at://did:plc:test/app.bsky.feed.post/rkey123",
			"cid": "bafyreidresolved",
		})
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)
	cid, err := svc.resolveRecordCID(context.Background(), "at://did:plc:test/app.bsky.feed.post/rkey123")
	require.NoError(t, err)
	assert.Equal(t, "bafyreidresolved", cid)
}

func TestResolveRecordCID_InvalidURI(t *testing.T) {
	svc := &atprotoService{cfg: &config.Config{ATProtoPDSURL: "https://example.com"}, retry: defaultRetryConfig()}
	_, err := svc.resolveRecordCID(context.Background(), "invalid-uri")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid AT URI")
}

// ── Retry integration tests (HTTP calls with retry) ──────────────────────────

func TestCreateRecord_RetriesOnServerError(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{
				"accessJwt": "token", "refreshJwt": "refresh", "did": "did:plc:test123",
			})
		case "/xrpc/com.atproto.repo.createRecord":
			calls++
			if calls < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:test123/app.bsky.feed.post/x",
				"cid": "bafyreid",
			})
		}
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)
	svc.retry = retryConfig{maxRetries: 3, baseDelay: time.Millisecond}

	ref, err := svc.createRecord(context.Background(), "token", "did:plc:test123", "test", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, ref)
	assert.Equal(t, 2, calls, "should have retried once")
}

func TestUploadBlob_RetriesOnServerError(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xrpc/com.atproto.repo.uploadBlob" {
			calls++
			if calls < 2 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"blob": map[string]interface{}{
					"$type": "blob", "ref": map[string]string{"$link": "cid"}, "size": 5,
				},
			})
		}
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)
	svc.retry = retryConfig{maxRetries: 3, baseDelay: time.Millisecond}

	// Create a temp file for upload
	tmpFile := t.TempDir() + "/test.jpg"
	require.NoError(t, writeTestFile(tmpFile, []byte("hello")))

	blob, err := svc.uploadBlob(context.Background(), "token", tmpFile)
	require.NoError(t, err)
	assert.NotNil(t, blob)
	assert.Equal(t, 2, calls)
}

func TestResolveHandle_RetriesOn429(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xrpc/com.atproto.identity.resolveHandle" {
			calls++
			if calls < 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"did": "did:plc:test"})
		}
	}))
	defer server.Close()

	svc := newTestATProtoService(t, server.URL)
	svc.retry = retryConfig{maxRetries: 3, baseDelay: time.Millisecond}

	identity, err := svc.ResolveHandle(context.Background(), "alice.bsky.social")
	require.NoError(t, err)
	assert.Equal(t, "did:plc:test", identity.DID)
	assert.Equal(t, 2, calls)
}

// writeTestFile is a helper to create a test file.
func writeTestFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
