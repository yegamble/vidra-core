package usecase_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
	"vidra-core/internal/usecase"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockPDSServer simulates an ATProto PDS for testing
type MockPDSServer struct {
	*httptest.Server
	actors    map[string]*domain.ATProtoActor
	follows   map[string][]string
	likes     map[string][]string
	posts     map[string]interface{}
	responses map[string]interface{}
}

func NewMockPDSServer() *MockPDSServer {
	m := &MockPDSServer{
		actors:    make(map[string]*domain.ATProtoActor),
		follows:   make(map[string][]string),
		likes:     make(map[string][]string),
		posts:     make(map[string]interface{}),
		responses: make(map[string]interface{}),
	}

	// Setup default test actors
	m.actors["test.handle"] = &domain.ATProtoActor{
		DID:         "did:plc:testuser123",
		Handle:      "test.handle",
		DisplayName: stringPtr("Test User"),
		Bio:         stringPtr("Test bio"),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		IndexedAt:   time.Now(),
	}

	m.actors["alice.test"] = &domain.ATProtoActor{
		DID:         "did:plc:alice456",
		Handle:      "alice.test",
		DisplayName: stringPtr("Alice"),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		IndexedAt:   time.Now(),
	}

	m.actors["bob.test"] = &domain.ATProtoActor{
		DID:         "did:plc:bob789",
		Handle:      "bob.test",
		DisplayName: stringPtr("Bob"),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		IndexedAt:   time.Now(),
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.identity.resolveHandle":
			m.handleResolveHandle(w, r)
		case "/xrpc/app.bsky.actor.getProfile":
			m.handleGetProfile(w, r)
		case "/xrpc/com.atproto.repo.createRecord":
			m.handleCreateRecord(w, r)
		case "/xrpc/com.atproto.repo.deleteRecord":
			m.handleDeleteRecord(w, r)
		case "/xrpc/app.bsky.feed.getAuthorFeed":
			m.handleGetAuthorFeed(w, r)
		case "/xrpc/com.atproto.server.createSession":
			m.handleCreateSession(w, r)
		case "/xrpc/com.atproto.server.refreshSession":
			m.handleRefreshSession(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	m.Server = httptest.NewServer(handler)
	return m
}

func (m *MockPDSServer) handleResolveHandle(w http.ResponseWriter, r *http.Request) {
	handle := r.URL.Query().Get("handle")
	if actor, ok := m.actors[handle]; ok {
		json.NewEncoder(w).Encode(map[string]string{"did": actor.DID})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (m *MockPDSServer) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	did := r.URL.Query().Get("actor")
	for _, actor := range m.actors {
		if actor.DID == did {
			profile := map[string]interface{}{
				"did":    actor.DID,
				"handle": actor.Handle,
			}
			if actor.DisplayName != nil {
				profile["displayName"] = *actor.DisplayName
			}
			if actor.Bio != nil {
				profile["description"] = *actor.Bio
			}
			json.NewEncoder(w).Encode(profile)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (m *MockPDSServer) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	collection := req["collection"].(string)
	repo := req["repo"].(string)
	rkey := "record" + time.Now().Format("20060102150405")
	uri := "at://" + repo + "/" + collection + "/" + rkey
	cid := "bafyrei" + rkey // Mock CID

	// Track the created record based on type
	record := req["record"].(map[string]interface{})
	recordType := record["$type"].(string)

	switch recordType {
	case "app.bsky.graph.follow":
		subject := record["subject"].(string)
		m.follows[repo] = append(m.follows[repo], subject)
	case "app.bsky.feed.like":
		subject := record["subject"].(map[string]interface{})
		subjectURI := subject["uri"].(string)
		m.likes[repo] = append(m.likes[repo], subjectURI)
	case "app.bsky.feed.post":
		m.posts[uri] = record
	}

	json.NewEncoder(w).Encode(map[string]string{
		"uri": uri,
		"cid": cid,
	})
}

func (m *MockPDSServer) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (m *MockPDSServer) handleGetAuthorFeed(w http.ResponseWriter, r *http.Request) {
	did := r.URL.Query().Get("actor")

	// Return mock feed
	feed := map[string]interface{}{
		"feed": []interface{}{
			map[string]interface{}{
				"uri": "at://" + did + "/app.bsky.feed.post/1",
				"cid": "bafyrei123",
				"record": map[string]interface{}{
					"$type":     "app.bsky.feed.post",
					"text":      "Test post",
					"createdAt": time.Now().Format(time.RFC3339),
				},
				"likeCount": float64(5),
			},
		},
		"cursor": "next123",
	}

	json.NewEncoder(w).Encode(feed)
}

func (m *MockPDSServer) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"accessJwt":  "mock.access.token",
		"refreshJwt": "mock.refresh.token",
		"did":        "did:plc:testuser123",
	})
}

func (m *MockPDSServer) handleRefreshSession(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"accessJwt":  "mock.new.access.token",
		"refreshJwt": "mock.new.refresh.token",
		"did":        "did:plc:testuser123",
	})
}

func TestSocialService_Follow(t *testing.T) {
	ctx := context.Background()
	mockPDS := NewMockPDSServer()
	defer mockPDS.Close()

	// Setup test database
	db := setupTestDB(t)
	defer db.Close()

	socialRepo := repository.NewSocialRepository(db)
	cfg := &config.Config{
		ATProtoPDSURL:      mockPDS.URL,
		ATProtoHandle:      "test.handle",
		ATProtoAppPassword: "test-password",
	}

	// Seed actors in database
	for _, actor := range mockPDS.actors {
		err := socialRepo.UpsertActor(ctx, actor)
		require.NoError(t, err)
	}

	atprotoService := usecase.NewAtprotoService(nil, cfg, nil, nil)
	socialService := usecase.NewSocialService(cfg, socialRepo, atprotoService, nil)

	// Test follow
	err := socialService.Follow(ctx, "did:plc:testuser123", "alice.test")
	assert.NoError(t, err)

	// Verify follow was created
	isFollowing, err := socialRepo.IsFollowing(ctx, "did:plc:testuser123", "did:plc:alice456")
	assert.NoError(t, err)
	assert.True(t, isFollowing)

	// Test duplicate follow
	err = socialService.Follow(ctx, "did:plc:testuser123", "alice.test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already following")
}

func TestSocialService_Unfollow(t *testing.T) {
	ctx := context.Background()
	mockPDS := NewMockPDSServer()
	defer mockPDS.Close()

	db := setupTestDB(t)
	defer db.Close()

	socialRepo := repository.NewSocialRepository(db)
	cfg := &config.Config{
		ATProtoPDSURL:      mockPDS.URL,
		ATProtoHandle:      "test.handle",
		ATProtoAppPassword: "test-password",
	}

	// Seed actors
	for _, actor := range mockPDS.actors {
		err := socialRepo.UpsertActor(ctx, actor)
		require.NoError(t, err)
	}

	atprotoService := usecase.NewAtprotoService(nil, cfg, nil, nil)
	socialService := usecase.NewSocialService(cfg, socialRepo, atprotoService, nil)

	// Create follow first
	err := socialService.Follow(ctx, "did:plc:testuser123", "alice.test")
	require.NoError(t, err)

	// Test unfollow
	err = socialService.Unfollow(ctx, "did:plc:testuser123", "alice.test")
	assert.NoError(t, err)

	// Verify follow was removed
	isFollowing, err := socialRepo.IsFollowing(ctx, "did:plc:testuser123", "did:plc:alice456")
	assert.NoError(t, err)
	assert.False(t, isFollowing)
}

func TestSocialService_Like(t *testing.T) {
	ctx := context.Background()
	mockPDS := NewMockPDSServer()
	defer mockPDS.Close()

	db := setupTestDB(t)
	defer db.Close()

	socialRepo := repository.NewSocialRepository(db)
	cfg := &config.Config{
		ATProtoPDSURL:      mockPDS.URL,
		ATProtoHandle:      "test.handle",
		ATProtoAppPassword: "test-password",
	}

	atprotoService := usecase.NewAtprotoService(nil, cfg, nil, nil)
	socialService := usecase.NewSocialService(cfg, socialRepo, atprotoService, nil)

	// Test like
	subjectURI := "at://did:plc:alice456/app.bsky.feed.post/123"
	err := socialService.Like(ctx, "did:plc:testuser123", subjectURI, "bafyrei123")
	assert.NoError(t, err)

	// Verify like was created
	hasLiked, err := socialRepo.HasLiked(ctx, "did:plc:testuser123", subjectURI)
	assert.NoError(t, err)
	assert.True(t, hasLiked)

	// Test duplicate like
	err = socialService.Like(ctx, "did:plc:testuser123", subjectURI, "bafyrei123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already liked")
}

func TestSocialService_Comment(t *testing.T) {
	ctx := context.Background()
	mockPDS := NewMockPDSServer()
	defer mockPDS.Close()

	db := setupTestDB(t)
	defer db.Close()

	socialRepo := repository.NewSocialRepository(db)
	cfg := &config.Config{
		ATProtoPDSURL:      mockPDS.URL,
		ATProtoHandle:      "test.handle",
		ATProtoAppPassword: "test-password",
	}

	// Seed actor
	actor := mockPDS.actors["test.handle"]
	err := socialRepo.UpsertActor(ctx, actor)
	require.NoError(t, err)

	atprotoService := usecase.NewAtprotoService(nil, cfg, nil, nil)
	socialService := usecase.NewSocialService(cfg, socialRepo, atprotoService, nil)

	// Test comment creation
	rootURI := "at://did:plc:alice456/app.bsky.feed.post/123"
	comment, err := socialService.Comment(
		ctx,
		"did:plc:testuser123",
		"This is a test comment",
		rootURI,
		"bafyrei123",
		"",
		"",
	)

	assert.NoError(t, err)
	assert.NotNil(t, comment)
	assert.Equal(t, "This is a test comment", comment.Text)
	assert.Equal(t, rootURI, comment.RootURI)
	assert.Equal(t, "did:plc:testuser123", comment.ActorDID)

	// Verify comment was stored
	comments, err := socialRepo.GetComments(ctx, rootURI, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, comments, 1)
}

func TestSocialService_ModerationLabel(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	socialRepo := repository.NewSocialRepository(db)
	cfg := &config.Config{
		EnableATProtoLabeler: true,
	}

	socialService := usecase.NewSocialService(cfg, socialRepo, nil, nil)

	// Apply moderation label
	err := socialService.ApplyModerationLabel(
		ctx,
		"did:plc:testuser123",
		"spam",
		"Spamming content",
		"did:plc:moderator",
		"",
		24*time.Hour,
	)
	assert.NoError(t, err)

	// Get moderation labels
	labels, err := socialService.GetModerationLabels(ctx, "did:plc:testuser123")
	assert.NoError(t, err)
	assert.Len(t, labels, 1)
	assert.Equal(t, "spam", labels[0].LabelType)
}

func TestSocialService_SocialStats(t *testing.T) {
	ctx := context.Background()
	mockPDS := NewMockPDSServer()
	defer mockPDS.Close()

	db := setupTestDB(t)
	defer db.Close()

	socialRepo := repository.NewSocialRepository(db)
	cfg := &config.Config{
		ATProtoPDSURL: mockPDS.URL,
	}

	// Seed actors and social data
	for _, actor := range mockPDS.actors {
		err := socialRepo.UpsertActor(ctx, actor)
		require.NoError(t, err)
	}

	// Create some follows
	follow1 := &domain.Follow{
		FollowerDID:  "did:plc:alice456",
		FollowingDID: "did:plc:testuser123",
		URI:          "at://follow1",
		CreatedAt:    time.Now(),
	}
	err := socialRepo.CreateFollow(ctx, follow1)
	require.NoError(t, err)

	follow2 := &domain.Follow{
		FollowerDID:  "did:plc:bob789",
		FollowingDID: "did:plc:testuser123",
		URI:          "at://follow2",
		CreatedAt:    time.Now(),
	}
	err = socialRepo.CreateFollow(ctx, follow2)
	require.NoError(t, err)

	socialService := usecase.NewSocialService(cfg, socialRepo, nil, nil)

	// Get social stats
	stats, err := socialService.GetSocialStats(ctx, "test.handle")
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, int64(2), stats.Followers)
}

// Helper functions

func setupTestDB(t *testing.T) *sqlx.DB {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping database tests")
	}

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)

	// Run migrations or setup test schema
	setupTestSchema(db)

	return db
}

func setupTestSchema(db *sqlx.DB) {
	// This would run the necessary migrations for testing
	// For now, we'll assume the test database has the schema ready
}

func stringPtr(s string) *string {
	return &s
}
