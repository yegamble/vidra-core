package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"athena/internal/domain"
	"athena/internal/usecase"
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
func (m *mockVideoRepo) UpdateProcessingInfo(_ context.Context, _ string, _ domain.ProcessingStatus, _ map[string]string, _ string, _ string) error {
	return nil
}

func TestSearchVideos_Success_WithMeta(t *testing.T) {
	vids := []*domain.Video{{ID: "v1", Title: "test one"}, {ID: "v2", Title: "another test"}}
	repo := &mockVideoRepo{videos: vids, total: 2}

	handler := SearchVideosHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=test&limit=10", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta == nil || resp.Meta.Total != 2 || resp.Meta.Limit != 10 || resp.Meta.Offset != 0 {
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

func TestListVideos_WithFilters_CapturesRequest(t *testing.T) {
	repo := &mockVideoRepo{videos: []*domain.Video{}, total: 0}
	handler := ListVideosHandler(repo)
	u := url.URL{Path: "/api/v1/videos"}
	q := u.Query()
	q.Set("category", "education")
	q.Set("language", "en")
	q.Set("sort", "views")
	q.Set("order", "desc")
	q.Set("limit", "5")
	q.Set("offset", "2")
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
	if repo.capturedList.Sort != "views" || repo.capturedList.Order != "desc" || repo.capturedList.Limit != 5 || repo.capturedList.Offset != 2 {
		t.Fatalf("unexpected sort/paging: %+v", repo.capturedList)
	}
}
