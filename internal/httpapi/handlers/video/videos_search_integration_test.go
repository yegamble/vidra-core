package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"
)

// This integration test ensures that search and user video endpoints
// return videos that have gone through the upload pipeline.
func TestSearchAndUserVideos_ReturnUploadedVideo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	td := testutil.SetupTestDB(t)
	if td == nil { // Skipped if DB/Redis unavailable
		t.Skip("TestDB not available")
		return
	}

	// Repositories and services
	uploadRepo := repository.NewUploadRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)
	encodingRepo := repository.NewEncodingRepository(td.DB)
	userRepo := repository.NewUserRepository(td.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Create a user
	user := createTestUser(t, userRepo, ctx, "u_search_"+time.Now().Format("150405"), "searcher@example.com")

	// Initiate upload and upload all chunks
	initResp := initiateTestUpload(t, uploadService, ctx, user.ID)
	uploadAllTestChunks(t, uploadService, ctx, initResp.SessionID, initResp.TotalChunks)

	// Complete the upload to flip video to queued and create encoding job
	if err := uploadService.CompleteUpload(ctx, initResp.SessionID); err != nil {
		t.Fatalf("complete upload error: %v", err)
	}

	// Find the video ID from session and mark it public+completed with a searchable title
	session, err := uploadRepo.GetSession(ctx, initResp.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	vid, err := videoRepo.GetByID(ctx, session.VideoID)
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	vid.Title = "Searchable Demo"
	vid.Description = "Uploaded via tests"
	vid.Privacy = domain.PrivacyPublic
	vid.Status = domain.StatusCompleted
	vid.UpdatedAt = time.Now()
	// Update requires owner user ID
	vid.UserID = user.ID
	if err := videoRepo.Update(ctx, vid); err != nil {
		t.Fatalf("update video: %v", err)
	}

	// 1) Search endpoint should include the uploaded video once completed/public
	{ // GET /api/v1/videos/search?q=Searchable
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=Searchable&limit=10", nil)
		rr := httptest.NewRecorder()
		SearchVideosHandler(videoRepo).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 from search, got %d body=%s", rr.Code, rr.Body.String())
		}
		var env Response
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode env: %v", err)
		}
		var got []*domain.Video
		b, _ := json.Marshal(env.Data)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		found := false
		for _, v := range got {
			if v.ID == vid.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("uploaded video not found in search results; got %v", got)
		}
	}

	// 2) User videos endpoint should list the uploaded video for the owner
	{ // GET /api/v1/users/{id}/videos
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+user.ID+"/videos?limit=10", nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", user.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))

		rr := httptest.NewRecorder()
		GetUserVideosHandler(videoRepo).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 from user videos, got %d body=%s", rr.Code, rr.Body.String())
		}
		var env Response
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode env: %v", err)
		}
		var got []*domain.Video
		b, _ := json.Marshal(env.Data)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		if len(got) == 0 {
			t.Fatalf("expected at least one video for user")
		}
		found := false
		for _, v := range got {
			if v.ID == vid.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("uploaded video not found in user videos; got %v", got)
		}
	}
}
