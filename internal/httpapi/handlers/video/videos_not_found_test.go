package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"
)

func TestGetVideoHandler_NotFoundSentinelReturns404(t *testing.T) {
	videoID := "123e4567-e89b-12d3-a456-426614174000"
	repo := &unitVideoRepoStub{
		getByIDFn: func(context.Context, string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID, nil), "id", videoID)
	rr := httptest.NewRecorder()

	GetVideoHandler(repo, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != "VIDEO_NOT_FOUND" {
		t.Fatalf("expected VIDEO_NOT_FOUND error, got %+v", resp.Error)
	}
}

func TestUpdateVideoHandler_NotFoundSentinelReturns404(t *testing.T) {
	videoID := "123e4567-e89b-12d3-a456-426614174001"
	repo := &unitVideoRepoStub{
		getByIDFn: func(context.Context, string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	req := withChiURLParam(
		httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID, strings.NewReader(`{"title":"updated","privacy":"public"}`)),
		"id",
		videoID,
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "owner-1"))
	rr := httptest.NewRecorder()

	UpdateVideoHandler(repo).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != "VIDEO_NOT_FOUND" {
		t.Fatalf("expected VIDEO_NOT_FOUND error, got %+v", resp.Error)
	}
}
