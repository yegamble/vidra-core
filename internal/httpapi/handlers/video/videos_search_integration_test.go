package video

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

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

func TestListAndSearch_IncludePublishedSourceWhileTranscoding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("TestDB not available")
		return
	}

	videoRepo := repository.NewVideoRepository(td.DB)
	userRepo := repository.NewUserRepository(td.DB)
	ctx := context.Background()

	user := createTestUser(t, userRepo, ctx, "u_source_"+time.Now().Format("150405"), "source@example.com")
	video := &domain.Video{
		ID:              "11111111-1111-1111-1111-111111111111",
		ThumbnailID:     "22222222-2222-2222-2222-222222222222",
		Title:           "Source Visible Demo",
		Description:     "Published before transcoding completes",
		Privacy:         domain.PrivacyPublic,
		Status:          domain.StatusProcessing,
		WaitTranscoding: false,
		UploadDate:      time.Now(),
		UserID:          user.ID,
		OutputPaths: map[string]string{
			"source": "/static/web-videos/source-visible-demo.mp4",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := videoRepo.Create(ctx, video); err != nil {
		t.Fatalf("create video: %v", err)
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos", nil)
		rr := httptest.NewRecorder()
		ListVideosHandler(videoRepo).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 from list, got %d body=%s", rr.Code, rr.Body.String())
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
			if v.ID == video.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("published source-only video not found in list results; got %v", got)
		}
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/v1/search/videos?search=Source+Visible", nil)
		rr := httptest.NewRecorder()
		SearchVideosHandler(videoRepo).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 from search alias, got %d body=%s", rr.Code, rr.Body.String())
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
			if v.ID == video.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("published source-only video not found in search results; got %v", got)
		}
	}
}

func TestSearchVideos_SortsByRelevanceDateViewsAndDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("TestDB not available")
		return
	}

	videoRepo := repository.NewVideoRepository(td.DB)
	userRepo := repository.NewUserRepository(td.DB)
	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "u_search_sort_"+time.Now().Format("150405"), "search-sort@example.com")
	baseTime := time.Now().UTC().Add(-12 * time.Hour)

	t.Run("relevance", func(t *testing.T) {
		query := fmt.Sprintf("relevance%d", time.Now().UnixNano())
		mostRelevant := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" "+query+" "+query, baseTime.Add(1*time.Minute), 25, 60)
		middleRelevant := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" "+query, baseTime.Add(2*time.Minute), 25, 60)
		lowestRelevant := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query, baseTime.Add(3*time.Minute), 25, 60)

		results := searchVideosForTest(t, videoRepo, "/api/v1/search/videos?search="+query+"&sort=-match&count=10")
		assertLeadingVideoIDs(t, results, mostRelevant.ID, middleRelevant.ID, lowestRelevant.ID)
	})

	t.Run("date", func(t *testing.T) {
		query := fmt.Sprintf("date%d", time.Now().UnixNano())
		oldest := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" oldest", baseTime.Add(1*time.Hour), 10, 60)
		middle := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" middle", baseTime.Add(2*time.Hour), 10, 60)
		newest := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" newest", baseTime.Add(3*time.Hour), 10, 60)

		results := searchVideosForTest(t, videoRepo, "/api/v1/search/videos?search="+query+"&sort=-publishedAt&count=10")
		assertLeadingVideoIDs(t, results, newest.ID, middle.ID, oldest.ID)
	})

	t.Run("views", func(t *testing.T) {
		query := fmt.Sprintf("views%d", time.Now().UnixNano())
		lowestViews := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" lowest", baseTime.Add(4*time.Hour), 15, 60)
		middleViews := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" middle", baseTime.Add(5*time.Hour), 150, 60)
		highestViews := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" highest", baseTime.Add(6*time.Hour), 1_500, 60)

		results := searchVideosForTest(t, videoRepo, "/api/v1/search/videos?search="+query+"&sort=-views&count=10")
		assertLeadingVideoIDs(t, results, highestViews.ID, middleViews.ID, lowestViews.ID)
	})

	t.Run("duration", func(t *testing.T) {
		query := fmt.Sprintf("duration%d", time.Now().UnixNano())
		shortest := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" shortest", baseTime.Add(7*time.Hour), 10, 45)
		middle := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" middle", baseTime.Add(8*time.Hour), 10, 240)
		longest := createPublicSearchVideo(t, videoRepo, ctx, user.ID, query+" longest", baseTime.Add(9*time.Hour), 10, 1_200)

		results := searchVideosForTest(t, videoRepo, "/api/v1/search/videos?search="+query+"&sort=-duration&count=10")
		assertLeadingVideoIDs(t, results, longest.ID, middle.ID, shortest.ID)
	})
}

func TestSearchVideos_AppliesCategoryTagDurationAndDateFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	td := testutil.SetupTestDB(t)
	if td == nil {
		t.Skip("TestDB not available")
		return
	}

	videoRepo := repository.NewVideoRepository(td.DB)
	userRepo := repository.NewUserRepository(td.DB)
	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "u_search_filters_"+time.Now().Format("150405"), "search-filters@example.com")
	categoryID := lookupVideoCategoryIDBySlug(t, td.DB, "music")
	baseTime := time.Date(2026, time.April, 17, 12, 0, 0, 0, time.UTC)
	query := fmt.Sprintf("filters%d", time.Now().UnixNano())

	match := createPublicSearchVideoWithOptions(t, videoRepo, ctx, user.ID, query+" match", baseTime, 120, 300, publicSearchVideoOptions{
		Tags:       []string{"music", "indie"},
		CategoryID: &categoryID,
	})
	createPublicSearchVideoWithOptions(t, videoRepo, ctx, user.ID, query+" wrong-category", baseTime, 120, 300, publicSearchVideoOptions{
		Tags: []string{"music", "indie"},
	})
	createPublicSearchVideoWithOptions(t, videoRepo, ctx, user.ID, query+" wrong-tags", baseTime, 120, 300, publicSearchVideoOptions{
		Tags:       []string{"gaming"},
		CategoryID: &categoryID,
	})
	createPublicSearchVideoWithOptions(t, videoRepo, ctx, user.ID, query+" too-short", baseTime, 120, 120, publicSearchVideoOptions{
		Tags:       []string{"music", "indie"},
		CategoryID: &categoryID,
	})
	createPublicSearchVideoWithOptions(t, videoRepo, ctx, user.ID, query+" too-old", baseTime.AddDate(0, 0, -45), 120, 300, publicSearchVideoOptions{
		Tags:       []string{"music", "indie"},
		CategoryID: &categoryID,
	})

	startDate := baseTime.AddDate(0, 0, -7).Format(time.RFC3339)
	requestURL := fmt.Sprintf(
		"/api/v1/search/videos?search=%s&categoryOneOf=%s&tagsOneOf=indie&durationMin=240&durationMax=600&startDate=%s&count=10",
		query,
		categoryID.String(),
		startDate,
	)
	results := searchVideosForTest(t, videoRepo, requestURL)
	assertLeadingVideoIDs(t, results, match.ID)
	if len(results) != 1 {
		t.Fatalf("expected exactly one filtered result, got %d (%v)", len(results), results)
	}
}

type publicSearchVideoOptions struct {
	Tags       []string
	CategoryID *uuid.UUID
}

func createPublicSearchVideo(
	t *testing.T,
	repo usecase.VideoRepository,
	ctx context.Context,
	userID string,
	title string,
	uploadDate time.Time,
	views int64,
	duration int,
) *domain.Video {
	return createPublicSearchVideoWithOptions(t, repo, ctx, userID, title, uploadDate, views, duration, publicSearchVideoOptions{})
}

func createPublicSearchVideoWithOptions(
	t *testing.T,
	repo usecase.VideoRepository,
	ctx context.Context,
	userID string,
	title string,
	uploadDate time.Time,
	views int64,
	duration int,
	options publicSearchVideoOptions,
) *domain.Video {
	t.Helper()

	video := &domain.Video{
		Title:       title,
		Description: title,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
		UploadDate:  uploadDate,
		UserID:      userID,
		Views:       views,
		Duration:    duration,
		CreatedAt:   uploadDate,
		UpdatedAt:   uploadDate,
		Tags:        options.Tags,
		CategoryID:  options.CategoryID,
	}

	if err := repo.Create(ctx, video); err != nil {
		t.Fatalf("create search video: %v", err)
	}

	return video
}

func lookupVideoCategoryIDBySlug(t *testing.T, db *sqlx.DB, slug string) uuid.UUID {
	t.Helper()

	var categoryID uuid.UUID
	if err := db.QueryRowContext(context.Background(), "SELECT id FROM video_categories WHERE slug = $1", slug).Scan(&categoryID); err != nil {
		t.Fatalf("lookup category %q: %v", slug, err)
	}

	return categoryID
}

func searchVideosForTest(t *testing.T, repo usecase.VideoRepository, requestURL string) []*domain.Video {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, requestURL, nil)
	rr := httptest.NewRecorder()
	SearchVideosHandler(repo).ServeHTTP(rr, req)
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

	return got
}

func assertLeadingVideoIDs(t *testing.T, videos []*domain.Video, expectedIDs ...string) {
	t.Helper()

	if len(videos) < len(expectedIDs) {
		t.Fatalf("expected at least %d videos, got %d", len(expectedIDs), len(videos))
	}

	for index, expectedID := range expectedIDs {
		if videos[index].ID != expectedID {
			t.Fatalf("expected result %d to be %s, got %s", index, expectedID, videos[index].ID)
		}
	}
}
