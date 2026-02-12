package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
	ucviews "athena/internal/usecase/views"
)

type unitViewsRepoStub struct {
	createUserViewFn               func(ctx context.Context, view *domain.UserView) error
	updateUserViewFn               func(ctx context.Context, view *domain.UserView) error
	getUserViewBySessionAndVideoFn func(ctx context.Context, sessionID, videoID string) (*domain.UserView, error)
	getVideoAnalyticsFn            func(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error)
	getDailyVideoStatsFn           func(ctx context.Context, videoID string, startDate, endDate time.Time) ([]domain.DailyVideoStats, error)
	getUserEngagementStatsFn       func(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error)
	getTrendingVideosFn            func(ctx context.Context, limit int) ([]domain.TrendingVideo, error)
	updateTrendingVideoFn          func(ctx context.Context, trending *domain.TrendingVideo) error
	getBatchTrendingStatsFn        func(ctx context.Context, videoIDs []string) ([]domain.VideoTrendingStats, error)
	batchUpdateTrendingVideosFn    func(ctx context.Context, videos []*domain.TrendingVideo) error
	incrementVideoViewsFn          func(ctx context.Context, videoID string) error
	getUniqueViewsFn               func(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error)
	calculateEngagementScoreFn     func(ctx context.Context, videoID string, hoursBack int) (float64, error)
	aggregateDailyStatsFn          func(ctx context.Context, date time.Time) error
	cleanupOldViewsFn              func(ctx context.Context, daysToKeep int) error
	getViewsByDateRangeFn          func(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error)
	getTopVideosFn                 func(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}, error)
}

func (s *unitViewsRepoStub) CreateUserView(ctx context.Context, view *domain.UserView) error {
	if s.createUserViewFn != nil {
		return s.createUserViewFn(ctx, view)
	}
	return nil
}

func (s *unitViewsRepoStub) UpdateUserView(ctx context.Context, view *domain.UserView) error {
	if s.updateUserViewFn != nil {
		return s.updateUserViewFn(ctx, view)
	}
	return nil
}

func (s *unitViewsRepoStub) GetUserViewBySessionAndVideo(ctx context.Context, sessionID, videoID string) (*domain.UserView, error) {
	if s.getUserViewBySessionAndVideoFn != nil {
		return s.getUserViewBySessionAndVideoFn(ctx, sessionID, videoID)
	}
	return nil, nil
}

func (s *unitViewsRepoStub) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	if s.getVideoAnalyticsFn != nil {
		return s.getVideoAnalyticsFn(ctx, filter)
	}
	return &domain.ViewAnalyticsResponse{
		TotalViews:      1,
		UniqueViews:     1,
		DeviceBreakdown: map[string]int64{"mobile": 1},
		CountryBreakdown: map[string]int64{
			"US": 1,
		},
	}, nil
}

func (s *unitViewsRepoStub) GetDailyVideoStats(ctx context.Context, videoID string, startDate, endDate time.Time) ([]domain.DailyVideoStats, error) {
	if s.getDailyVideoStatsFn != nil {
		return s.getDailyVideoStatsFn(ctx, videoID, startDate, endDate)
	}
	return []domain.DailyVideoStats{{VideoID: videoID, TotalViews: 5}}, nil
}

func (s *unitViewsRepoStub) GetUserEngagementStats(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error) {
	if s.getUserEngagementStatsFn != nil {
		return s.getUserEngagementStatsFn(ctx, userID, startDate, endDate)
	}
	return []domain.UserEngagementStats{{UserID: userID, VideosWatched: 1}}, nil
}

func (s *unitViewsRepoStub) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	if s.getTrendingVideosFn != nil {
		return s.getTrendingVideosFn(ctx, limit)
	}
	return []domain.TrendingVideo{{VideoID: uuid.NewString(), EngagementScore: 10}}, nil
}

func (s *unitViewsRepoStub) UpdateTrendingVideo(ctx context.Context, trending *domain.TrendingVideo) error {
	if s.updateTrendingVideoFn != nil {
		return s.updateTrendingVideoFn(ctx, trending)
	}
	return nil
}

func (s *unitViewsRepoStub) GetBatchTrendingStats(ctx context.Context, videoIDs []string) ([]domain.VideoTrendingStats, error) {
	if s.getBatchTrendingStatsFn != nil {
		return s.getBatchTrendingStatsFn(ctx, videoIDs)
	}
	return nil, nil
}

func (s *unitViewsRepoStub) BatchUpdateTrendingVideos(ctx context.Context, videos []*domain.TrendingVideo) error {
	if s.batchUpdateTrendingVideosFn != nil {
		return s.batchUpdateTrendingVideosFn(ctx, videos)
	}
	return nil
}

func (s *unitViewsRepoStub) IncrementVideoViews(ctx context.Context, videoID string) error {
	if s.incrementVideoViewsFn != nil {
		return s.incrementVideoViewsFn(ctx, videoID)
	}
	return nil
}

func (s *unitViewsRepoStub) GetUniqueViews(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error) {
	if s.getUniqueViewsFn != nil {
		return s.getUniqueViewsFn(ctx, videoID, startDate, endDate)
	}
	return 1, nil
}

func (s *unitViewsRepoStub) CalculateEngagementScore(ctx context.Context, videoID string, hoursBack int) (float64, error) {
	if s.calculateEngagementScoreFn != nil {
		return s.calculateEngagementScoreFn(ctx, videoID, hoursBack)
	}
	return 1, nil
}

func (s *unitViewsRepoStub) AggregateDailyStats(ctx context.Context, date time.Time) error {
	if s.aggregateDailyStatsFn != nil {
		return s.aggregateDailyStatsFn(ctx, date)
	}
	return nil
}

func (s *unitViewsRepoStub) CleanupOldViews(ctx context.Context, daysToKeep int) error {
	if s.cleanupOldViewsFn != nil {
		return s.cleanupOldViewsFn(ctx, daysToKeep)
	}
	return nil
}

func (s *unitViewsRepoStub) GetViewsByDateRange(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error) {
	if s.getViewsByDateRangeFn != nil {
		return s.getViewsByDateRangeFn(ctx, filter)
	}
	return []domain.UserView{{VideoID: filter.VideoID}}, nil
}

func (s *unitViewsRepoStub) GetTopVideos(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
	VideoID     string  `db:"video_id"`
	TotalViews  int64   `db:"total_views"`
	UniqueViews int64   `db:"unique_views"`
	AvgDuration float64 `db:"avg_duration"`
}, error) {
	if s.getTopVideosFn != nil {
		return s.getTopVideosFn(ctx, startDate, endDate, limit)
	}
	return []struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}{
		{VideoID: uuid.NewString(), TotalViews: 100, UniqueViews: 80},
	}, nil
}

func newUnitViewsHandler(repo *unitViewsRepoStub, videoRepo *unitVideoRepoStub) *ViewsHandler {
	if repo == nil {
		repo = &unitViewsRepoStub{}
	}
	if videoRepo == nil {
		videoRepo = &unitVideoRepoStub{
			getByIDFn: func(_ context.Context, id string) (*domain.Video, error) {
				return &domain.Video{ID: id, Title: "unit-video"}, nil
			},
		}
	}
	return NewViewsHandler(ucviews.NewService(repo, videoRepo))
}

func TestViewsHandler_TrackView_UnitBranches(t *testing.T) {
	videoID := uuid.NewString()

	t.Run("invalid json", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/views", strings.NewReader("{bad"))
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.TrackView(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INVALID_JSON", response.Error.Code)
	})

	t.Run("validation error", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/views", strings.NewReader(`{"session_id":"","fingerprint":"x"}`))
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.TrackView(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INVALID_REQUEST", response.Error.Code)
	})

	t.Run("service failure", func(t *testing.T) {
		videoRepo := &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				return nil, errors.New("db timeout")
			},
		}
		handler := newUnitViewsHandler(&unitViewsRepoStub{}, videoRepo)
		body := `{"session_id":"` + uuid.NewString() + `","fingerprint":"fp","watch_duration":10,"video_duration":100}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/views", strings.NewReader(body))
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.TrackView(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("success with authenticated user", func(t *testing.T) {
		var capturedUserID *string
		repo := &unitViewsRepoStub{
			createUserViewFn: func(_ context.Context, view *domain.UserView) error {
				capturedUserID = view.UserID
				return nil
			},
		}
		handler := newUnitViewsHandler(repo, nil)
		body := `{"session_id":"` + uuid.NewString() + `","fingerprint":"fp","watch_duration":10,"video_duration":100}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/views", strings.NewReader(body))
		req = withChiURLParam(req, "videoId", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
		rr := httptest.NewRecorder()
		handler.TrackView(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		// Close the service to flush the async view queue before asserting
		handler.viewsService.Close()
		require.NotNil(t, capturedUserID)
		assert.Equal(t, "user-123", *capturedUserID)
	})
}

func TestViewsHandler_GetVideoAnalytics_UnitBranches(t *testing.T) {
	videoID := uuid.NewString()

	t.Run("invalid start date", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/analytics?start_date=bad", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetVideoAnalytics(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid anonymous filter", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/analytics?is_anonymous=nope", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetVideoAnalytics(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service failure", func(t *testing.T) {
		repo := &unitViewsRepoStub{
			getVideoAnalyticsFn: func(context.Context, *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
				return nil, errors.New("query error")
			},
		}
		handler := newUnitViewsHandler(repo, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/analytics", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetVideoAnalytics(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("success", func(t *testing.T) {
		var captured *domain.ViewAnalyticsFilter
		repo := &unitViewsRepoStub{
			getVideoAnalyticsFn: func(_ context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
				captured = filter
				return &domain.ViewAnalyticsResponse{TotalViews: 10, UniqueViews: 5}, nil
			},
		}
		handler := newUnitViewsHandler(repo, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/analytics?device_type=mobile&country_code=US&is_anonymous=true", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetVideoAnalytics(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, captured)
		assert.Equal(t, videoID, captured.VideoID)
		assert.Equal(t, "mobile", captured.DeviceType)
		assert.Equal(t, "US", captured.CountryCode)
		require.NotNil(t, captured.IsAnonymous)
		assert.True(t, *captured.IsAnonymous)
	})
}

func TestViewsHandler_GetUserEngagement_UnitBranches(t *testing.T) {
	userID := uuid.NewString()

	t.Run("unauthenticated", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/engagement", nil)
		req = withChiURLParam(req, "userId", userID)
		rr := httptest.NewRecorder()
		handler.GetUserEngagement(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("forbidden for non-admin", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/engagement", nil)
		req = withChiURLParam(req, "userId", userID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
		rr := httptest.NewRecorder()
		handler.GetUserEngagement(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("invalid days", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/engagement?days=400", nil)
		req = withChiURLParam(req, "userId", userID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
		rr := httptest.NewRecorder()
		handler.GetUserEngagement(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service failure", func(t *testing.T) {
		repo := &unitViewsRepoStub{
			getUserEngagementStatsFn: func(context.Context, string, time.Time, time.Time) ([]domain.UserEngagementStats, error) {
				return nil, errors.New("select failed")
			},
		}
		handler := newUnitViewsHandler(repo, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/engagement", nil)
		req = withChiURLParam(req, "userId", userID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
		rr := httptest.NewRecorder()
		handler.GetUserEngagement(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("success owner", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/engagement?days=7", nil)
		req = withChiURLParam(req, "userId", userID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
		rr := httptest.NewRecorder()
		handler.GetUserEngagement(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.True(t, response.Success)
	})
}

func TestViewsHandler_GetDailyStats_UnitBranches(t *testing.T) {
	videoID := uuid.NewString()

	t.Run("invalid days", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stats/daily?days=0", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetDailyStats(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service failure", func(t *testing.T) {
		repo := &unitViewsRepoStub{
			getDailyVideoStatsFn: func(context.Context, string, time.Time, time.Time) ([]domain.DailyVideoStats, error) {
				return nil, errors.New("stats failure")
			},
		}
		handler := newUnitViewsHandler(repo, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stats/daily?days=7", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetDailyStats(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("success", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stats/daily?days=7", nil)
		req = withChiURLParam(req, "videoId", videoID)
		rr := httptest.NewRecorder()
		handler.GetDailyStats(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestViewsHandler_GetTrendingAndTopVideos_UnitBranches(t *testing.T) {
	t.Run("invalid trending limit", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/trending?limit=oops", nil)
		rr := httptest.NewRecorder()
		handler.GetTrendingVideos(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("trending include details success", func(t *testing.T) {
		videoID := uuid.NewString()
		repo := &unitViewsRepoStub{
			getTrendingVideosFn: func(context.Context, int) ([]domain.TrendingVideo, error) {
				return []domain.TrendingVideo{{VideoID: videoID, EngagementScore: 5}}, nil
			},
		}
		videoRepo := &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				return &domain.Video{ID: videoID, Title: "v1"}, nil
			},
		}
		handler := newUnitViewsHandler(repo, videoRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/trending?include_details=true&pageSize=5&page=1", nil)
		rr := httptest.NewRecorder()
		handler.GetTrendingVideos(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.True(t, response.Success)
	})

	t.Run("top videos invalid limit and success", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/top?limit=0", nil)
		rr := httptest.NewRecorder()
		handler.GetTopVideos(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		req = httptest.NewRequest(http.MethodGet, "/api/v1/videos/top?days=7&limit=2", nil)
		rr = httptest.NewRecorder()
		handler.GetTopVideos(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestViewsHandler_GetViewHistory_UnitBranches(t *testing.T) {
	targetUser := uuid.NewString()

	t.Run("unauthorized when filtering by user", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/views/history?user_id="+targetUser, nil)
		rr := httptest.NewRecorder()
		handler.GetViewHistory(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("forbidden for other user", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/views/history?user_id="+targetUser, nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
		rr := httptest.NewRecorder()
		handler.GetViewHistory(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("invalid date and offset", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/views/history?start_date=bad", nil)
		rr := httptest.NewRecorder()
		handler.GetViewHistory(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		req = httptest.NewRequest(http.MethodGet, "/api/v1/views/history?offset=-1", nil)
		rr = httptest.NewRecorder()
		handler.GetViewHistory(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service failure and success", func(t *testing.T) {
		repo := &unitViewsRepoStub{
			getViewsByDateRangeFn: func(context.Context, *domain.ViewAnalyticsFilter) ([]domain.UserView, error) {
				return nil, errors.New("history failure")
			},
		}
		handler := newUnitViewsHandler(repo, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/views/history", nil)
		rr := httptest.NewRecorder()
		handler.GetViewHistory(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)

		handler = newUnitViewsHandler(nil, nil)
		req = httptest.NewRequest(http.MethodGet, "/api/v1/views/history?limit=10&offset=0", nil)
		rr = httptest.NewRecorder()
		handler.GetViewHistory(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestViewsHandler_FingerprintAndAdminActions_UnitBranches(t *testing.T) {
	t.Run("generate fingerprint invalid and success", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		handler.GenerateFingerprint(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", strings.NewReader(`{"ip":"1.2.3.4","user_agent":"test-agent"}`))
		rr = httptest.NewRecorder()
		handler.GenerateFingerprint(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Contains(t, payload, "fingerprint_hash")
		assert.Len(t, payload["fingerprint_hash"].(string), 32)
	})

	t.Run("aggregate stats invalid and success", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/views/aggregate", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		handler.AdminAggregateStats(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		repo := &unitViewsRepoStub{
			aggregateDailyStatsFn: func(context.Context, time.Time) error {
				return errors.New("aggregate failed")
			},
		}
		handler = newUnitViewsHandler(repo, nil)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/views/aggregate", bytes.NewReader([]byte(`{}`)))
		rr = httptest.NewRecorder()
		handler.AdminAggregateStats(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)

		handler = newUnitViewsHandler(nil, nil)
		dateStr := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
		req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/views/aggregate", bytes.NewReader([]byte(`{"date":"`+dateStr+`"}`)))
		rr = httptest.NewRecorder()
		handler.AdminAggregateStats(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("cleanup invalid and success", func(t *testing.T) {
		handler := newUnitViewsHandler(nil, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/views/cleanup", strings.NewReader(`{"days_to_keep":0}`))
		rr := httptest.NewRecorder()
		handler.AdminCleanupOldData(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		repo := &unitViewsRepoStub{
			cleanupOldViewsFn: func(context.Context, int) error {
				return errors.New("cleanup failed")
			},
		}
		handler = newUnitViewsHandler(repo, nil)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/views/cleanup", strings.NewReader(`{"days_to_keep":30}`))
		rr = httptest.NewRecorder()
		handler.AdminCleanupOldData(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)

		handler = newUnitViewsHandler(nil, nil)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/views/cleanup", strings.NewReader(`{"days_to_keep":30}`))
		rr = httptest.NewRecorder()
		handler.AdminCleanupOldData(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}
