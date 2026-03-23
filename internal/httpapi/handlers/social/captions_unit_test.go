package social

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type mockCaptionService struct {
	createCaptionFn                func(ctx context.Context, videoID uuid.UUID, req *domain.CreateCaptionRequest, file io.Reader) (*domain.Caption, error)
	getCaptionsByVideoIDFn         func(ctx context.Context, videoID uuid.UUID) (*domain.CaptionListResponse, error)
	getCaptionByIDFn               func(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error)
	getCaptionByVideoAndLanguageFn func(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error)
	getCaptionContentFn            func(ctx context.Context, captionID uuid.UUID) (io.ReadCloser, string, error)
	updateCaptionFn                func(ctx context.Context, captionID uuid.UUID, req *domain.UpdateCaptionRequest) (*domain.Caption, error)
	deleteCaptionFn                func(ctx context.Context, captionID uuid.UUID) error
}

func (m *mockCaptionService) CreateCaption(ctx context.Context, videoID uuid.UUID, req *domain.CreateCaptionRequest, file io.Reader) (*domain.Caption, error) {
	if m.createCaptionFn != nil {
		return m.createCaptionFn(ctx, videoID, req, file)
	}
	return nil, nil
}

func (m *mockCaptionService) GetCaptionsByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.CaptionListResponse, error) {
	if m.getCaptionsByVideoIDFn != nil {
		return m.getCaptionsByVideoIDFn(ctx, videoID)
	}
	return nil, nil
}

func (m *mockCaptionService) GetCaptionByID(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error) {
	if m.getCaptionByIDFn != nil {
		return m.getCaptionByIDFn(ctx, captionID)
	}
	return nil, nil
}

func (m *mockCaptionService) GetCaptionByVideoAndLanguage(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error) {
	if m.getCaptionByVideoAndLanguageFn != nil {
		return m.getCaptionByVideoAndLanguageFn(ctx, videoID, languageCode)
	}
	return nil, domain.ErrNotFound
}

func (m *mockCaptionService) GetCaptionContent(ctx context.Context, captionID uuid.UUID) (io.ReadCloser, string, error) {
	if m.getCaptionContentFn != nil {
		return m.getCaptionContentFn(ctx, captionID)
	}
	return nil, "", nil
}

func (m *mockCaptionService) UpdateCaption(ctx context.Context, captionID uuid.UUID, req *domain.UpdateCaptionRequest) (*domain.Caption, error) {
	if m.updateCaptionFn != nil {
		return m.updateCaptionFn(ctx, captionID, req)
	}
	return nil, nil
}

func (m *mockCaptionService) DeleteCaption(ctx context.Context, captionID uuid.UUID) error {
	if m.deleteCaptionFn != nil {
		return m.deleteCaptionFn(ctx, captionID)
	}
	return nil
}

type mockCaptionVideoRepo struct {
	getByIDFn func(ctx context.Context, videoID string) (*domain.Video, error)
}

func (m *mockCaptionVideoRepo) GetByID(ctx context.Context, videoID string) (*domain.Video, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, videoID)
	}
	return nil, nil
}

func TestCreateCaption_InvalidVideoID(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/videos/invalid-id/captions", nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString())
	req = req.WithContext(ctx)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-id")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateCaption_Unauthorized(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/videos/"+uuid.NewString()+"/captions", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.NewString())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateCaption_VideoNotFound(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("POST", "/api/v1/videos/"+videoID.String()+"/captions", nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreateCaption_Forbidden(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	otherUserID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: otherUserID.String(),
			}, nil
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("POST", "/api/v1/videos/"+videoID.String()+"/captions", nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateCaption_MissingRequiredFields(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("file_format", "vtt")
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/videos/"+videoID.String()+"/captions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateCaption_Success(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	captionID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		createCaptionFn: func(ctx context.Context, vid uuid.UUID, req *domain.CreateCaptionRequest, file io.Reader) (*domain.Caption, error) {
			assert.Equal(t, videoID, vid)
			assert.Equal(t, "en", req.LanguageCode)
			assert.Equal(t, "English", req.Label)
			return &domain.Caption{
				ID:           captionID,
				VideoID:      videoID,
				LanguageCode: "en",
				Label:        "English",
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("language_code", "en")
	writer.WriteField("label", "English")
	writer.WriteField("file_format", "vtt")
	part, _ := writer.CreateFormFile("caption_file", "test.vtt")
	part.Write([]byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nTest caption"))
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/videos/"+videoID.String()+"/captions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestGetCaptions_InvalidVideoID(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/videos/invalid-id/captions", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-id")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptions(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetCaptions_VideoNotFound(t *testing.T) {
	videoID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptions(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetCaptions_PrivateVideoUnauthorized(t *testing.T) {
	videoID := uuid.New()
	ownerID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID.String(),
				UserID:  ownerID.String(),
				Privacy: domain.PrivacyPrivate,
			}, nil
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptions(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestGetCaptions_Success(t *testing.T) {
	videoID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID.String(),
				Privacy: domain.PrivacyPublic,
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionsByVideoIDFn: func(ctx context.Context, vid uuid.UUID) (*domain.CaptionListResponse, error) {
			return &domain.CaptionListResponse{
				Captions: []domain.Caption{
					{ID: uuid.New(), VideoID: videoID, LanguageCode: "en"},
				},
				Count: 1,
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptions(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetCaptionContent_InvalidVideoID(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/videos/invalid-id/captions/"+uuid.NewString()+"/content", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-id")
	rctx.URLParams.Add("captionId", uuid.NewString())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptionContent(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetCaptionContent_InvalidCaptionID(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+uuid.NewString()+"/captions/invalid-id/content", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.NewString())
	rctx.URLParams.Add("captionId", "invalid-id")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptionContent(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetCaptionContent_CaptionNotBelongToVideo(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	otherVideoID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID.String(),
				Privacy: domain.PrivacyPublic,
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: otherVideoID,
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String()+"/content", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptionContent(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateCaption_Unauthorized(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("PUT", "/api/v1/videos/"+uuid.NewString()+"/captions/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.NewString())
	rctx.URLParams.Add("captionId", uuid.NewString())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.UpdateCaption(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUpdateCaption_InvalidJSON(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: videoID,
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), strings.NewReader("invalid json"))
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.UpdateCaption(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateCaption_Success(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: videoID,
			}, nil
		},
		updateCaptionFn: func(ctx context.Context, cid uuid.UUID, req *domain.UpdateCaptionRequest) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: videoID,
				Label:   "Updated Label",
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	reqBody := map[string]interface{}{
		"label": "Updated Label",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), bytes.NewReader(body))
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.UpdateCaption(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDeleteCaption_Unauthorized(t *testing.T) {
	handler := NewCaptionHandlers(nil, nil)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/"+uuid.NewString()+"/captions/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.NewString())
	rctx.URLParams.Add("captionId", uuid.NewString())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.DeleteCaption(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeleteCaption_CaptionNotFound(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.DeleteCaption(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteCaption_Success(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: videoID,
			}, nil
		},
		deleteCaptionFn: func(ctx context.Context, cid uuid.UUID) error {
			return nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.DeleteCaption(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateCaption_ServiceError(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		createCaptionFn: func(ctx context.Context, vid uuid.UUID, req *domain.CreateCaptionRequest, file io.Reader) (*domain.Caption, error) {
			return nil, errors.New("service error")
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("language_code", "en")
	writer.WriteField("label", "English")
	part, _ := writer.CreateFormFile("caption_file", "test.vtt")
	part.Write([]byte("WEBVTT"))
	writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/videos/"+videoID.String()+"/captions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.CreateCaption(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetCaptions_ServiceError(t *testing.T) {
	videoID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID.String(),
				Privacy: domain.PrivacyPublic,
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionsByVideoIDFn: func(ctx context.Context, vid uuid.UUID) (*domain.CaptionListResponse, error) {
			return nil, errors.New("service error")
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptions(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetCaptionContent_PrivateVideoUnauthorized(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	ownerID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID.String(),
				UserID:  ownerID.String(),
				Privacy: domain.PrivacyPrivate,
			}, nil
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String()+"/content", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptionContent(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestGetCaptionContent_CaptionNotFound(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID.String(),
				Privacy: domain.PrivacyPublic,
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("GET", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String()+"/content", nil)
	rec := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCaptionContent(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateCaption_VideoNotFound(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), strings.NewReader("{}"))
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.UpdateCaption(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateCaption_CaptionNotBelongToVideo(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()
	otherVideoID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: otherVideoID,
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	reqBody := map[string]interface{}{
		"label": "Updated Label",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("PUT", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), bytes.NewReader(body))
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.UpdateCaption(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteCaption_VideoNotFound(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionHandlers(nil, mockVideoRepo)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.DeleteCaption(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteCaption_CaptionNotBelongToVideo(t *testing.T) {
	videoID := uuid.New()
	captionID := uuid.New()
	userID := uuid.New()
	otherVideoID := uuid.New()

	mockVideoRepo := &mockCaptionVideoRepo{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
			}, nil
		},
	}

	mockService := &mockCaptionService{
		getCaptionByIDFn: func(ctx context.Context, cid uuid.UUID) (*domain.Caption, error) {
			return &domain.Caption{
				ID:      captionID,
				VideoID: otherVideoID,
			}, nil
		},
	}

	handler := NewCaptionHandlers(mockService, mockVideoRepo)

	req := httptest.NewRequest("DELETE", "/api/v1/videos/"+videoID.String()+"/captions/"+captionID.String(), nil)
	rec := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("captionId", captionID.String())
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	handler.DeleteCaption(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
