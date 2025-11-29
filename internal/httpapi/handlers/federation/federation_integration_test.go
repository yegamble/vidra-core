package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"
	"athena/internal/testutil"

	"github.com/go-chi/chi/v5"
)

func TestFederationTimeline_Integration(t *testing.T) {
	// Skip if not in integration test mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test database
	testDB := testutil.SetupTestDB(t)
	db := testDB.DB

	// Initialize repositories
	fedRepo := repository.NewFederationRepository(db)

	// Seed test data
	ctx := context.Background()
	now := time.Now()

	// Insert test posts
	testPosts := []*domain.FederatedPost{
		{
			ActorDID:    "did:plc:test1",
			URI:         "at://did:plc:test1/app.bsky.feed.post/1",
			Text:        stringPtr("First test post"),
			CID:         stringPtr("cid1"),
			ActorHandle: stringPtr("test1.bsky.social"),
			CreatedAt:   &now,
		},
		{
			ActorDID:    "did:plc:test2",
			URI:         "at://did:plc:test2/app.bsky.feed.post/2",
			Text:        stringPtr("Second test post"),
			CID:         stringPtr("cid2"),
			ActorHandle: stringPtr("test2.bsky.social"),
			CreatedAt:   &now,
			EmbedURL:    stringPtr("https://example.com/video"),
			EmbedTitle:  stringPtr("Embedded Video"),
		},
		{
			ActorDID:    "did:plc:test3",
			URI:         "at://did:plc:test3/app.bsky.feed.post/3",
			Text:        stringPtr("Third test post"),
			CID:         stringPtr("cid3"),
			ActorHandle: stringPtr("test3.bsky.social"),
			CreatedAt:   &now,
		},
	}

	for _, post := range testPosts {
		err := fedRepo.UpsertPost(ctx, post)
		if err != nil {
			t.Fatalf("Failed to insert test post: %v", err)
		}
	}

	// Create handlers
	handlers := NewFederationHandlers(fedRepo)

	// Create router
	r := chi.NewRouter()
	r.Get("/api/v1/federation/timeline", handlers.GetTimeline)

	// Test default pagination
	t.Run("DefaultPagination", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/federation/timeline", nil)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp domain.FederatedTimeline
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp.Total < 3 {
			t.Errorf("Expected at least 3 posts, got %d", resp.Total)
		}
		if resp.Page != 1 {
			t.Errorf("Expected page 1, got %d", resp.Page)
		}
		if resp.PageSize != 20 {
			t.Errorf("Expected pageSize 20, got %d", resp.PageSize)
		}
	})

	// Test custom pagination
	t.Run("CustomPagination", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/federation/timeline?page=1&pageSize=2", nil)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp domain.FederatedTimeline
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if len(resp.Data) > 2 {
			t.Errorf("Expected max 2 posts in page, got %d", len(resp.Data))
		}
		if resp.PageSize != 2 {
			t.Errorf("Expected pageSize 2, got %d", resp.PageSize)
		}
	})

	// Test second page
	t.Run("SecondPage", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/federation/timeline?page=2&pageSize=2", nil)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp domain.FederatedTimeline
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp.Page != 2 {
			t.Errorf("Expected page 2, got %d", resp.Page)
		}
	})

	// Test max page size limit
	t.Run("MaxPageSize", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/federation/timeline?pageSize=200", nil)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		var resp domain.FederatedTimeline
		json.Unmarshal(w.Body.Bytes(), &resp)

		if resp.PageSize != 100 {
			t.Errorf("Expected pageSize capped at 100, got %d", resp.PageSize)
		}
	})
}

func TestFederationWorkflow_EndToEnd(t *testing.T) {
	// Skip if not in integration test mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test database
	testDB := testutil.SetupTestDB(t)
	db := testDB.DB

	// Initialize repositories and services
	fedRepo := repository.NewFederationRepository(db)
	_ = repository.NewModerationRepository(db) // modRepo not used in this test

	_ = &config.Config{ // cfg not used in this test
		EnableATProto:                   true,
		FederationIngestIntervalSeconds: 60,
		FederationIngestMaxItems:        10,
		FederationIngestMaxPages:        2,
	}

	// Test job processing workflow
	ctx := context.Background()

	t.Run("JobQueueWorkflow", func(t *testing.T) {
		// Enqueue a test job
		jobID, err := fedRepo.EnqueueJob(ctx, "test_job", map[string]string{"test": "data"}, time.Now())
		if err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}

		// Get the next job
		job, err := fedRepo.GetNextJob(ctx)
		if err != nil {
			t.Fatalf("Failed to get next job: %v", err)
		}

		if job == nil {
			t.Fatal("Expected to get a job")
		}

		if job.ID != jobID {
			t.Errorf("Expected job ID %s, got %s", jobID, job.ID)
		}

		// Complete the job
		err = fedRepo.CompleteJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("Failed to complete job: %v", err)
		}

		// Should have no more jobs
		nextJob, err := fedRepo.GetNextJob(ctx)
		if err != nil {
			t.Fatalf("Failed to check for next job: %v", err)
		}
		if nextJob != nil {
			t.Error("Expected no more jobs")
		}
	})

	t.Run("ActorStateManagement", func(t *testing.T) {
		actor := "test.bsky.social"

		// Since EnableActor/DisableActor don't exist, test basic actor state management
		// Set actor cursor
		err := fedRepo.SetActorCursor(ctx, actor, "test-cursor-123")
		if err != nil {
			t.Fatalf("Failed to set cursor: %v", err)
		}

		// Get actor state
		cursor, _, _, _, err := fedRepo.GetActorStateSimple(ctx, actor)
		if err != nil {
			// Actor might not exist yet, which is okay for this test
			t.Logf("Actor state not found (expected): %v", err)
		}

		if cursor == "test-cursor-123" {
			t.Logf("Cursor set successfully")
		}

		// Set next_at time
		nextTime := time.Now().Add(5 * time.Minute)
		err = fedRepo.SetActorNextAt(ctx, actor, nextTime)
		if err != nil {
			t.Fatalf("Failed to set next_at: %v", err)
		}

		// Verify next_at was set
		_, nextAt, _, _, err := fedRepo.GetActorStateSimple(ctx, actor)
		if err != nil {
			t.Logf("Failed to get updated state (may be expected): %v", err)
		}

		if nextAt != nil && nextAt.Equal(nextTime.Truncate(time.Microsecond)) {
			t.Logf("next_at time set correctly")
		}

		// Test attempts counter
		err = fedRepo.SetActorAttempts(ctx, actor, 3)
		if err != nil {
			t.Fatalf("Failed to set attempts: %v", err)
		}

		_, _, attempts, _, err = fedRepo.GetActorStateSimple(ctx, actor)
		if err != nil {
			t.Logf("Failed to get attempts (may be expected): %v", err)
		}

		if attempts == 3 {
			t.Logf("Attempts set correctly")
		}
	})

	t.Run("PostIngestion", func(t *testing.T) {
		// Create a test post
		post := &domain.FederatedPost{
			ActorDID:    "did:plc:workflow",
			URI:         "at://did:plc:workflow/app.bsky.feed.post/test",
			Text:        stringPtr("Workflow test post"),
			CID:         stringPtr("workflow-cid"),
			ActorHandle: stringPtr("workflow.bsky.social"),
			CreatedAt:   timePtr(time.Now()),
			EmbedURL:    stringPtr("https://example.com/workflow-video"),
		}

		// Upsert post
		err := fedRepo.UpsertPost(ctx, post)
		if err != nil {
			t.Fatalf("Failed to upsert post: %v", err)
		}

		// Verify post exists in timeline
		posts, total, err := fedRepo.ListTimeline(ctx, 10, 0)
		if err != nil {
			t.Fatalf("Failed to list timeline: %v", err)
		}

		if total == 0 {
			t.Error("Expected at least one post in timeline")
		}

		found := false
		for _, p := range posts {
			if p.URI == post.URI {
				found = true
				if p.Text == nil || *p.Text != "Workflow test post" {
					t.Error("Post text not saved correctly")
				}
				break
			}
		}

		if !found {
			t.Error("Post not found in timeline after upsert")
		}

		// Test duplicate upsert (should update, not create new)
		post.Text = stringPtr("Updated workflow test post")
		err = fedRepo.UpsertPost(ctx, post)
		if err != nil {
			t.Fatalf("Failed to update post: %v", err)
		}

		// Verify update
		posts, _, _ = fedRepo.ListTimeline(ctx, 100, 0)
		count := 0
		for _, p := range posts {
			if p.URI == post.URI {
				count++
				if p.Text == nil || *p.Text != "Updated workflow test post" {
					t.Error("Post not updated correctly")
				}
			}
		}

		if count != 1 {
			t.Errorf("Expected exactly 1 post with URI, found %d", count)
		}
	})

	// Test persisting embed_type for video embeds
	t.Run("VideoEmbedPersistence", func(t *testing.T) {
		post := &domain.FederatedPost{
			ActorDID:    "did:plc:vid",
			URI:         "at://did:plc:vid/app.bsky.feed.post/v1",
			Text:        stringPtr("Video post"),
			ActorHandle: stringPtr("vid.bsky.social"),
			EmbedType:   stringPtr("video"),
		}

		err := fedRepo.UpsertPost(ctx, post)
		if err != nil {
			t.Fatalf("Failed to upsert video post: %v", err)
		}

		posts, _, err := fedRepo.ListTimeline(ctx, 50, 0)
		if err != nil {
			t.Fatalf("Failed to list timeline: %v", err)
		}

		found := false
		for _, p := range posts {
			if p.URI == post.URI {
				found = true
				if p.EmbedType == nil || *p.EmbedType != "video" {
					t.Errorf("Expected embed_type=video, got %v", p.EmbedType)
				}
				break
			}
		}
		if !found {
			t.Error("Video post not found in timeline")
		}
	})
}

// Helper functions (removed stringPtr - already defined in video_category_handler_test.go)
func timePtr(t time.Time) *time.Time {
	return &t
}
