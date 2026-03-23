package social

import (
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRatingService struct {
	setRatingFunc           func(ctx context.Context, userID uuid.UUID, videoID uuid.UUID, rating domain.RatingValue) error
	getVideoRatingStatsFunc func(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error)
	removeRatingFunc        func(ctx context.Context, userID uuid.UUID, videoID uuid.UUID) error
	getUserRatingsFunc      func(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error)
}

func (m *mockRatingService) SetRating(ctx context.Context, userID uuid.UUID, videoID uuid.UUID, rating domain.RatingValue) error {
	if m.setRatingFunc != nil {
		return m.setRatingFunc(ctx, userID, videoID, rating)
	}
	return errors.New("not implemented")
}

func (m *mockRatingService) GetVideoRatingStats(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error) {
	if m.getVideoRatingStatsFunc != nil {
		return m.getVideoRatingStatsFunc(ctx, videoID, userID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockRatingService) RemoveRating(ctx context.Context, userID uuid.UUID, videoID uuid.UUID) error {
	if m.removeRatingFunc != nil {
		return m.removeRatingFunc(ctx, userID, videoID)
	}
	return errors.New("not implemented")
}

func (m *mockRatingService) GetUserRatings(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
	if m.getUserRatingsFunc != nil {
		return m.getUserRatingsFunc(ctx, userID, limit, offset)
	}
	return nil, errors.New("not implemented")
}

func TestSetRating_Unauthorized(t *testing.T) {
	videoID := uuid.New()
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": 1}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSetRating_InvalidVideoID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": 1}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/invalid-uuid/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetRating_InvalidJSON(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader([]byte("invalid json")))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetRating_InvalidRatingValue(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": 5}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetRating_VideoNotFound(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockRatingService{
		setRatingFunc: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID, rating domain.RatingValue) error {
			return domain.ErrNotFound
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": 1}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSetRating_ServiceError(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockRatingService{
		setRatingFunc: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID, rating domain.RatingValue) error {
			return errors.New("database error")
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": 1}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSetRating_Success_Like(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockRatingService{
		setRatingFunc: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID, rating domain.RatingValue) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, videoID, vid)
			assert.Equal(t, domain.RatingLike, rating)
			return nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": 1}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSetRating_Success_Dislike(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockRatingService{
		setRatingFunc: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID, rating domain.RatingValue) error {
			assert.Equal(t, domain.RatingDislike, rating)
			return nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	reqBody := map[string]int{"rating": -1}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID.String()+"/rating", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.SetRating(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetRating_InvalidVideoID(t *testing.T) {
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid-uuid/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetRating(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetRating_ServiceError(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockRatingService{
		getVideoRatingStatsFunc: func(ctx context.Context, vid uuid.UUID, uid *uuid.UUID) (*domain.VideoRatingStats, error) {
			return nil, errors.New("database error")
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetRating(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetRating_Success_Unauthenticated(t *testing.T) {
	videoID := uuid.New()

	stats := &domain.VideoRatingStats{
		LikesCount:    100,
		DislikesCount: 10,
	}

	mockService := &mockRatingService{
		getVideoRatingStatsFunc: func(ctx context.Context, vid uuid.UUID, uid *uuid.UUID) (*domain.VideoRatingStats, error) {
			assert.Equal(t, videoID, vid)
			assert.Nil(t, uid)
			return stats, nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetRating(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper shared.Response
	err := json.NewDecoder(rec.Body).Decode(&wrapper)
	require.NoError(t, err)
}

func TestGetRating_Success_Authenticated(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	stats := &domain.VideoRatingStats{
		LikesCount:    100,
		DislikesCount: 10,
		UserRating:    domain.RatingLike,
	}

	mockService := &mockRatingService{
		getVideoRatingStatsFunc: func(ctx context.Context, vid uuid.UUID, uid *uuid.UUID) (*domain.VideoRatingStats, error) {
			assert.Equal(t, videoID, vid)
			require.NotNil(t, uid)
			assert.Equal(t, userID, *uid)
			return stats, nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.GetRating(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRemoveRating_Unauthorized(t *testing.T) {
	videoID := uuid.New()
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+videoID.String()+"/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.RemoveRating(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRemoveRating_InvalidVideoID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/invalid-uuid/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.RemoveRating(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRemoveRating_ServiceError(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockRatingService{
		removeRatingFunc: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID) error {
			return errors.New("database error")
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+videoID.String()+"/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.RemoveRating(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRemoveRating_Success(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockRatingService{
		removeRatingFunc: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, videoID, vid)
			return nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+videoID.String()+"/rating", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.RemoveRating(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetUserRatings_Unauthorized(t *testing.T) {
	mockService := &mockRatingService{}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/ratings", nil)

	rec := httptest.NewRecorder()
	handler.GetUserRatings(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetUserRatings_ServiceError(t *testing.T) {
	userID := uuid.New()

	mockService := &mockRatingService{
		getUserRatingsFunc: func(ctx context.Context, uid uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
			return nil, errors.New("database error")
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/ratings", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.GetUserRatings(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetUserRatings_Success_DefaultPagination(t *testing.T) {
	userID := uuid.New()

	ratings := []*domain.VideoRating{
		{VideoID: uuid.New(), Rating: domain.RatingLike},
		{VideoID: uuid.New(), Rating: domain.RatingDislike},
	}

	mockService := &mockRatingService{
		getUserRatingsFunc: func(ctx context.Context, uid uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
			assert.Equal(t, userID, uid)
			assert.Equal(t, 20, limit)
			assert.Equal(t, 0, offset)
			return ratings, nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/ratings", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.GetUserRatings(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetUserRatings_Success_CustomPagination(t *testing.T) {
	userID := uuid.New()

	ratings := []*domain.VideoRating{}

	mockService := &mockRatingService{
		getUserRatingsFunc: func(ctx context.Context, uid uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
			assert.Equal(t, userID, uid)
			assert.Equal(t, 50, limit)
			assert.Equal(t, 100, offset)
			return ratings, nil
		},
	}
	handler := &RatingHandlers{ratingService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/ratings?limit=50&offset=100", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.GetUserRatings(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
