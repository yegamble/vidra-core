package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"

	"github.com/google/uuid"
)

type sourceVideoRepo struct {
	usecase.VideoRepository
	video *domain.Video
	err   error
}

func (r *sourceVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return r.video, r.err
}

func TestGetVideoSource_OK(t *testing.T) {
	videoID := uuid.New().String()
	userID := "user-1"
	repo := &sourceVideoRepo{
		video: &domain.Video{
			ID:     videoID,
			UserID: userID,
			S3URLs: map[string]string{"source": "https://s3.example.com/source/myvideo.mp4"},
		},
	}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			FileDownloadURL string `json:"fileDownloadUrl"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.FileDownloadURL == "" {
		t.Fatal("expected non-empty fileDownloadUrl")
	}
}

func TestGetVideoSource_LocalPath(t *testing.T) {
	videoID := uuid.New().String()
	userID := "user-1"
	repo := &sourceVideoRepo{
		video: &domain.Video{
			ID:          videoID,
			UserID:      userID,
			OutputPaths: map[string]string{"source": "/uploads/myvideo.mp4"},
		},
	}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetVideoSource_NoSource(t *testing.T) {
	videoID := uuid.New().String()
	userID := "user-1"
	repo := &sourceVideoRepo{
		video: &domain.Video{ID: videoID, UserID: userID},
	}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetVideoSource_NotFound(t *testing.T) {
	videoID := uuid.New().String()
	repo := &sourceVideoRepo{err: domain.ErrNotFound}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetVideoSource_Forbidden(t *testing.T) {
	videoID := uuid.New().String()
	repo := &sourceVideoRepo{
		video: &domain.Video{
			ID:     videoID,
			UserID: "owner-user",
			S3URLs: map[string]string{"source": "https://s3.example.com/source/myvideo.mp4"},
		},
	}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "other-user"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestGetVideoSource_AdminCanAccess(t *testing.T) {
	videoID := uuid.New().String()
	repo := &sourceVideoRepo{
		video: &domain.Video{
			ID:     videoID,
			UserID: "owner-user",
			S3URLs: map[string]string{"source": "https://s3.example.com/source/myvideo.mp4"},
		},
	}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "admin-user")
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetVideoSource_Unauthenticated(t *testing.T) {
	videoID := uuid.New().String()
	repo := &sourceVideoRepo{
		video: &domain.Video{
			ID:     videoID,
			UserID: "owner-user",
			S3URLs: map[string]string{"source": "https://s3.example.com/source/myvideo.mp4"},
		},
	}

	h := GetVideoSourceHandler(repo)
	req := withChiURLParam(httptest.NewRequest(http.MethodGet, "/videos/"+videoID+"/source", nil), "id", videoID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
