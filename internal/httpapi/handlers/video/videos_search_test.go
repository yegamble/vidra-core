package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
	"vidra-core/internal/usecase"

	"github.com/google/uuid"
)

// mockVideoRepo minimally satisfies usecase.VideoRepository for HTTP tests
type mockVideoRepo struct {
	usecase.VideoRepository
	videos         []*domain.Video
	total          int64
	getByID        *domain.Video
	capturedSearch *domain.VideoSearchRequest
	capturedList   *domain.VideoSearchRequest
}

func (m *mockVideoRepo) Create(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return m.getByID, nil
}
func (m *mockVideoRepo) GetByUserID(_ context.Context, _ string, _ int, _ int) ([]*domain.Video, int64, error) {
	return m.videos, m.total, nil
}
func (m *mockVideoRepo) Update(_ context.Context, _ *domain.Video) error    { return nil }
func (m *mockVideoRepo) Delete(_ context.Context, _ string, _ string) error { return nil }
func (m *mockVideoRepo) List(_ context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	m.capturedList = req
	return m.videos, m.total, nil
}
func (m *mockVideoRepo) Search(_ context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	m.capturedSearch = req
	return m.videos, m.total, nil
}
func (m *mockVideoRepo) UpdateProcessingInfo(_ context.Context, _ port.VideoProcessingParams) error {
	return nil
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ port.VideoProcessingWithCIDsParams) error {
	return nil
}

func TestSearchVideos_Success_WithMeta(t *testing.T) {
	vids := []*domain.Video{{ID: "v1", Title: "test one"}, {ID: "v2", Title: "another test"}}
	repo := &mockVideoRepo{videos: vids, total: 2}

	handler := SearchVideosHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=test&pageSize=10&page=1", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta == nil || resp.Meta.Total != 2 || resp.Meta.PageSize != 10 || resp.Meta.Page != 1 {
		t.Fatalf("unexpected meta: %+v", resp.Meta)
	}
	var got []*domain.Video
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(got) != 2 || got[0].ID == "" || got[1].ID == "" {
		t.Fatalf("unexpected videos: %+v", got)
	}
	// ensure query captured
	if repo.capturedSearch == nil || repo.capturedSearch.Query != "test" {
		t.Fatalf("expected captured search query 'test', got %+v", repo.capturedSearch)
	}
}

func TestSearchVideos_MissingQuery_400(t *testing.T) {
	repo := &mockVideoRepo{}
	handler := SearchVideosHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/search", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSearchVideos_AcceptsSearchAlias(t *testing.T) {
	repo := &mockVideoRepo{videos: []*domain.Video{{ID: "v1", Title: "alias test"}}, total: 1}
	handler := SearchVideosHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/videos?search=alias", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.capturedSearch == nil || repo.capturedSearch.Query != "alias" {
		t.Fatalf("expected captured search query 'alias', got %+v", repo.capturedSearch)
	}
}

func TestSearchVideos_CapturesPeerTubeFiltersAndAliases(t *testing.T) {
	repo := &mockVideoRepo{videos: []*domain.Video{{ID: "v1", Title: "filtered"}}, total: 1}
	handler := SearchVideosHandler(repo)
	categoryID := uuid.New()
	startDate := "2026-04-01T00:00:00Z"
	endDate := "2026-04-30T23:59:59Z"

	u := url.URL{Path: "/api/v1/search/videos"}
	q := u.Query()
	q.Set("search", "creator")
	q.Add("tagsOneOf", "music")
	q.Add("tagsOneOf", "indie")
	q.Set("categoryOneOf", categoryID.String())
	q.Set("durationMin", "60")
	q.Set("durationMax", "600")
	q.Set("startDate", startDate)
	q.Set("endDate", endDate)
	q.Set("sort", "-publishedAt")
	q.Set("count", "10")
	q.Set("start", "20")
	u.RawQuery = q.Encode()

	req := httptest.NewRequest(http.MethodGet, u.String(), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.capturedSearch == nil {
		t.Fatalf("expected captured search request")
	}
	if repo.capturedSearch.Query != "creator" {
		t.Fatalf("expected query 'creator', got %+v", repo.capturedSearch)
	}
	if repo.capturedSearch.Sort != "upload_date" || repo.capturedSearch.Order != "desc" {
		t.Fatalf("expected upload_date desc sort, got %+v", repo.capturedSearch)
	}
	if repo.capturedSearch.Limit != 10 || repo.capturedSearch.Offset != 20 {
		t.Fatalf("expected limit=10 offset=20, got %+v", repo.capturedSearch)
	}
	if repo.capturedSearch.DurationMin == nil || *repo.capturedSearch.DurationMin != 60 {
		t.Fatalf("expected durationMin=60, got %+v", repo.capturedSearch)
	}
	if repo.capturedSearch.DurationMax == nil || *repo.capturedSearch.DurationMax != 600 {
		t.Fatalf("expected durationMax=600, got %+v", repo.capturedSearch)
	}
	if repo.capturedSearch.CategoryID == nil || *repo.capturedSearch.CategoryID != categoryID {
		t.Fatalf("expected categoryID=%s, got %+v", categoryID, repo.capturedSearch)
	}
	if len(repo.capturedSearch.Tags) != 2 || repo.capturedSearch.Tags[0] != "music" || repo.capturedSearch.Tags[1] != "indie" {
		t.Fatalf("expected tags to be preserved, got %+v", repo.capturedSearch)
	}
	expectedStart, _ := time.Parse(time.RFC3339, startDate)
	expectedEnd, _ := time.Parse(time.RFC3339, endDate)
	if repo.capturedSearch.PublishedAfter == nil || !repo.capturedSearch.PublishedAfter.Equal(expectedStart) {
		t.Fatalf("expected PublishedAfter=%s, got %+v", expectedStart, repo.capturedSearch)
	}
	if repo.capturedSearch.PublishedBefore == nil || !repo.capturedSearch.PublishedBefore.Equal(expectedEnd) {
		t.Fatalf("expected PublishedBefore=%s, got %+v", expectedEnd, repo.capturedSearch)
	}
}

func TestListVideos_WithFilters_CapturesRequest(t *testing.T) {
	repo := &mockVideoRepo{videos: []*domain.Video{}, total: 0}
	handler := ListVideosHandler(repo)
	u := url.URL{Path: "/api/v1/videos"}
	q := u.Query()
	q.Set("category", "education")
	q.Set("language", "en")
	q.Set("sort", "views")
	q.Set("order", "desc")
	q.Set("pageSize", "5")
	q.Set("page", "1")
	u.RawQuery = q.Encode()

	req := httptest.NewRequest(http.MethodGet, u.String(), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.capturedList == nil {
		t.Fatalf("expected captured List request")
	}
	// CategoryID should be nil for this test since we're not filtering by category
	if repo.capturedList.CategoryID != nil || repo.capturedList.Language != "en" {
		t.Fatalf("unexpected filters: %+v", repo.capturedList)
	}
	if repo.capturedList.Sort != "views" || repo.capturedList.Order != "desc" || repo.capturedList.Limit != 5 || repo.capturedList.Offset != 0 {
		t.Fatalf("unexpected sort/paging: %+v", repo.capturedList)
	}
}

func TestListVideos_AcceptsPeerTubeSortAndPaginationAliases(t *testing.T) {
	repo := &mockVideoRepo{videos: []*domain.Video{}, total: 0}
	handler := ListVideosHandler(repo)
	categoryID := uuid.New()

	u := url.URL{Path: "/api/v1/videos"}
	q := u.Query()
	q.Set("categoryOneOf", categoryID.String())
	q.Set("sort", "-publishedAt")
	q.Set("count", "8")
	q.Set("start", "16")
	u.RawQuery = q.Encode()

	req := httptest.NewRequest(http.MethodGet, u.String(), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if repo.capturedList == nil {
		t.Fatalf("expected captured list request")
	}
	if repo.capturedList.CategoryID == nil || *repo.capturedList.CategoryID != categoryID {
		t.Fatalf("expected categoryID=%s, got %+v", categoryID, repo.capturedList)
	}
	if repo.capturedList.Sort != "upload_date" || repo.capturedList.Order != "desc" {
		t.Fatalf("expected upload_date desc sort, got %+v", repo.capturedList)
	}
	if repo.capturedList.Limit != 8 || repo.capturedList.Offset != 16 {
		t.Fatalf("expected limit=8 offset=16, got %+v", repo.capturedList)
	}
}

func TestListVideos_WithPagePagination_ReturnsMeta(t *testing.T) {
	repo := &mockVideoRepo{videos: []*domain.Video{}, total: 0}
	handler := ListVideosHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos?page=2&pageSize=5", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta == nil || resp.Meta.Page != 2 || resp.Meta.PageSize != 5 || resp.Meta.Limit != 5 || resp.Meta.Offset != 5 {
		t.Fatalf("unexpected meta: %+v", resp.Meta)
	}
}
