package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAnalyticsStreamRepo struct {
	getByIDFunc        func(ctx context.Context, id string) (*domain.LiveStream, error)
	getChannelByIDFunc func(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
}

func (m *mockAnalyticsStreamRepo) GetByID(ctx context.Context, id string) (*domain.LiveStream, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockAnalyticsStreamRepo) GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	if m.getChannelByIDFunc != nil {
		return m.getChannelByIDFunc(ctx, id)
	}
	return nil, errors.New("not implemented")
}

type mockAnalyticsRepo struct {
	getAnalyticsByStreamFunc   func(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error)
	getStreamSummaryFunc       func(ctx context.Context, streamID uuid.UUID) (*domain.StreamStatsSummary, error)
	getAnalyticsTimeSeriesFunc func(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error)
	getLatestAnalyticsFunc     func(ctx context.Context, streamID uuid.UUID) (*domain.StreamAnalytics, error)
	getCurrentViewerCountFunc  func(ctx context.Context, streamID uuid.UUID) (int, error)
}

func (m *mockAnalyticsRepo) GetAnalyticsByStream(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error) {
	if m.getAnalyticsByStreamFunc != nil {
		return m.getAnalyticsByStreamFunc(ctx, streamID, timeRange)
	}
	return nil, errors.New("not implemented")
}

func (m *mockAnalyticsRepo) GetStreamSummary(ctx context.Context, streamID uuid.UUID) (*domain.StreamStatsSummary, error) {
	if m.getStreamSummaryFunc != nil {
		return m.getStreamSummaryFunc(ctx, streamID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockAnalyticsRepo) GetAnalyticsTimeSeries(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
	if m.getAnalyticsTimeSeriesFunc != nil {
		return m.getAnalyticsTimeSeriesFunc(ctx, streamID, timeRange)
	}
	return nil, errors.New("not implemented")
}

func (m *mockAnalyticsRepo) GetLatestAnalytics(ctx context.Context, streamID uuid.UUID) (*domain.StreamAnalytics, error) {
	if m.getLatestAnalyticsFunc != nil {
		return m.getLatestAnalyticsFunc(ctx, streamID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockAnalyticsRepo) GetCurrentViewerCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	if m.getCurrentViewerCountFunc != nil {
		return m.getCurrentViewerCountFunc(ctx, streamID)
	}
	return 0, errors.New("not implemented")
}

func (m *mockAnalyticsRepo) CreateAnalytics(ctx context.Context, analytics *domain.StreamAnalytics) error {
	return nil
}
func (m *mockAnalyticsRepo) BatchCreateAnalytics(ctx context.Context, analytics []*domain.StreamAnalytics) error {
	return nil
}
func (m *mockAnalyticsRepo) UpdateStreamSummary(ctx context.Context, streamID uuid.UUID) error {
	return nil
}
func (m *mockAnalyticsRepo) CreateOrUpdateSummary(ctx context.Context, summary *domain.StreamStatsSummary) error {
	return nil
}
func (m *mockAnalyticsRepo) CreateViewerSession(ctx context.Context, session *domain.AnalyticsViewerSession) error {
	return nil
}
func (m *mockAnalyticsRepo) EndViewerSession(ctx context.Context, sessionID string) error {
	return nil
}
func (m *mockAnalyticsRepo) GetActiveViewers(ctx context.Context, streamID uuid.UUID) ([]*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) GetViewerSession(ctx context.Context, sessionID string) (*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) UpdateSessionEngagement(ctx context.Context, sessionID string, messagesSent int, liked, shared bool) error {
	return nil
}
func (m *mockAnalyticsRepo) CleanupOldAnalytics(ctx context.Context, retentionDays int) error {
	return nil
}
func (m *mockAnalyticsRepo) BatchUpdateStreamSummaries(ctx context.Context, streamIDs []uuid.UUID) error {
	return nil
}
func (m *mockAnalyticsRepo) GetActiveViewersForStreams(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID][]*domain.AnalyticsViewerSession, error) {
	return nil, nil
}
func (m *mockAnalyticsRepo) GetCurrentViewerCounts(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	return nil, nil
}

type mockAnalyticsCollector struct {
	trackViewerJoinFunc  func(ctx context.Context, streamID uuid.UUID, userID *uuid.UUID, sessionID string, ipAddress string, userAgent string) error
	trackViewerLeaveFunc func(ctx context.Context, sessionID string) error
	trackEngagementFunc  func(ctx context.Context, sessionID string, messagesSent int, liked bool, shared bool) error
}

func (m *mockAnalyticsCollector) TrackViewerJoin(ctx context.Context, streamID uuid.UUID, userID *uuid.UUID, sessionID string, ipAddress string, userAgent string) error {
	if m.trackViewerJoinFunc != nil {
		return m.trackViewerJoinFunc(ctx, streamID, userID, sessionID, ipAddress, userAgent)
	}
	return nil
}

func (m *mockAnalyticsCollector) TrackViewerLeave(ctx context.Context, sessionID string) error {
	if m.trackViewerLeaveFunc != nil {
		return m.trackViewerLeaveFunc(ctx, sessionID)
	}
	return nil
}

func (m *mockAnalyticsCollector) TrackEngagement(ctx context.Context, sessionID string, messagesSent int, liked bool, shared bool) error {
	if m.trackEngagementFunc != nil {
		return m.trackEngagementFunc(ctx, sessionID, messagesSent, liked, shared)
	}
	return nil
}

func newAuthRequest(method, url string, userID uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	return req.WithContext(ctx)
}

func TestGetStreamAnalytics(t *testing.T) {
	userID := uuid.New()
	streamID := uuid.New()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getAnalyticsByStreamFunc: func(ctx context.Context, sid uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error) {
				assert.Equal(t, streamID, sid)
				return []*domain.StreamAnalytics{{StreamID: streamID}}, nil
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid stream ID", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/invalid/analytics", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", "invalid-uuid")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unauthorized", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("stream not found", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return nil, errors.New("not found")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("forbidden - not owner", func(t *testing.T) {
		otherUserID := uuid.New()
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: otherUserID,
				}, nil
			},
		}

		handler := NewAnalyticsHandler(streamRepo, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("failed to get channel", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "failed to get channel")
	})

	t.Run("failed to get analytics", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getAnalyticsByStreamFunc: func(ctx context.Context, sid uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamAnalytics(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "failed to get analytics")
	})
}

func TestGetStreamSummary(t *testing.T) {
	userID := uuid.New()
	streamID := uuid.New()
	channelID := uuid.New()

	t.Run("success with summary", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getStreamSummaryFunc: func(ctx context.Context, sid uuid.UUID) (*domain.StreamStatsSummary, error) {
				return &domain.StreamStatsSummary{
					StreamID:     sid,
					TotalViewers: 100,
				}, nil
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/summary", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamSummary(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data    domain.StreamStatsSummary `json:"data"`
			Success bool                      `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, streamID, resp.Data.StreamID)
		assert.Equal(t, 100, resp.Data.TotalViewers)
	})

	t.Run("success with nil summary", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getStreamSummaryFunc: func(ctx context.Context, sid uuid.UUID) (*domain.StreamStatsSummary, error) {
				return nil, nil
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/summary", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamSummary(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data    domain.StreamStatsSummary `json:"data"`
			Success bool                      `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, streamID, resp.Data.StreamID)
	})

	t.Run("invalid stream ID", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/invalid/analytics/summary", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", "invalid")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamSummary(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unauthorized", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/summary", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamSummary(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("stream not found", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return nil, errors.New("not found")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/summary", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamSummary(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("failed to get summary", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getStreamSummaryFunc: func(ctx context.Context, sid uuid.UUID) (*domain.StreamStatsSummary, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/summary", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetStreamSummary(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "failed to get stream summary")
	})
}

func TestGetAnalyticsChart(t *testing.T) {
	userID := uuid.New()
	streamID := uuid.New()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		now := time.Now()
		dataPoints := []*domain.AnalyticsDataPoint{
			{Time: now, AvgViewers: 10, MaxViewers: 15},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getAnalyticsTimeSeriesFunc: func(ctx context.Context, sid uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
				assert.Equal(t, streamID, sid)
				assert.NotNil(t, timeRange)
				return dataPoints, nil
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/chart", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetAnalyticsChart(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				StreamID  uuid.UUID                    `json:"stream_id"`
				TimeRange map[string]interface{}       `json:"time_range"`
				Data      []*domain.AnalyticsDataPoint `json:"data"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, streamID, resp.Data.StreamID)
		assert.Len(t, resp.Data.Data, 1)
	})

	t.Run("invalid stream ID", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/invalid/analytics/chart", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", "invalid")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetAnalyticsChart(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unauthorized", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/chart", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetAnalyticsChart(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("stream not found", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return nil, errors.New("not found")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, nil, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/chart", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetAnalyticsChart(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("failed to get chart data", func(t *testing.T) {
		streamRepo := &mockAnalyticsStreamRepo{
			getByIDFunc: func(ctx context.Context, id string) (*domain.LiveStream, error) {
				return &domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil
			},
			getChannelByIDFunc: func(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
				return &domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil
			},
		}

		analyticsRepo := &mockAnalyticsRepo{
			getAnalyticsTimeSeriesFunc: func(ctx context.Context, sid uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(streamRepo, analyticsRepo, nil)

		req := newAuthRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/chart", userID)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetAnalyticsChart(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "failed to get analytics chart data")
	})
}

func TestGetCurrentAnalytics(t *testing.T) {
	streamID := uuid.New()

	t.Run("success", func(t *testing.T) {
		now := time.Now()
		latestAnalytics := &domain.StreamAnalytics{
			StreamID:    streamID,
			ViewerCount: 42,
			CollectedAt: now,
		}

		analyticsRepo := &mockAnalyticsRepo{
			getCurrentViewerCountFunc: func(ctx context.Context, sid uuid.UUID) (int, error) {
				assert.Equal(t, streamID, sid)
				return 42, nil
			},
			getLatestAnalyticsFunc: func(ctx context.Context, sid uuid.UUID) (*domain.StreamAnalytics, error) {
				assert.Equal(t, streamID, sid)
				return latestAnalytics, nil
			},
		}

		handler := NewAnalyticsHandler(nil, analyticsRepo, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/current", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetCurrentAnalytics(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data struct {
				StreamID     uuid.UUID               `json:"stream_id"`
				ViewerCount  int                     `json:"viewer_count"`
				LatestRecord *domain.StreamAnalytics `json:"latest_record"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, streamID, resp.Data.StreamID)
		assert.Equal(t, 42, resp.Data.ViewerCount)
		assert.Equal(t, streamID, resp.Data.LatestRecord.StreamID)
	})

	t.Run("invalid stream ID", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/invalid/analytics/current", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", "invalid")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetCurrentAnalytics(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("viewer count error", func(t *testing.T) {
		analyticsRepo := &mockAnalyticsRepo{
			getCurrentViewerCountFunc: func(ctx context.Context, sid uuid.UUID) (int, error) {
				return 0, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(nil, analyticsRepo, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/analytics/current", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("streamId", streamID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetCurrentAnalytics(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestTrackViewerJoin(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	sessionID := "test-session-123"

	t.Run("success with user ID", func(t *testing.T) {
		var capturedStreamID uuid.UUID
		var capturedUserID *uuid.UUID
		var capturedSessionID string
		var capturedIP string
		var capturedUA string

		collector := &mockAnalyticsCollector{
			trackViewerJoinFunc: func(ctx context.Context, sid uuid.UUID, uid *uuid.UUID, sessID string, ip string, ua string) error {
				capturedStreamID = sid
				capturedUserID = uid
				capturedSessionID = sessID
				capturedIP = ip
				capturedUA = ua
				return nil
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"stream_id":  streamID.String(),
			"user_id":    userID.String(),
			"session_id": sessionID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/join", bytes.NewReader(bodyBytes))
		req.Header.Set("User-Agent", "Test-Agent")

		w := httptest.NewRecorder()
		handler.TrackViewerJoin(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, streamID, capturedStreamID)
		assert.Equal(t, userID, *capturedUserID)
		assert.Equal(t, sessionID, capturedSessionID)
		assert.NotEmpty(t, capturedIP)
		assert.Equal(t, "Test-Agent", capturedUA)

		var resp struct {
			Data struct {
				Status    string `json:"status"`
				SessionID string `json:"session_id"`
			} `json:"data"`
			Success bool `json:"success"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "success", resp.Data.Status)
		assert.Equal(t, sessionID, resp.Data.SessionID)
	})

	t.Run("success without user ID", func(t *testing.T) {
		collector := &mockAnalyticsCollector{
			trackViewerJoinFunc: func(ctx context.Context, sid uuid.UUID, uid *uuid.UUID, sessID string, ip string, ua string) error {
				assert.Nil(t, uid)
				return nil
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"stream_id":  streamID.String(),
			"session_id": sessionID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/join", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerJoin(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/join", bytes.NewReader([]byte("invalid json")))

		w := httptest.NewRecorder()
		handler.TrackViewerJoin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing stream_id", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		reqBody := map[string]interface{}{
			"session_id": sessionID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/join", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerJoin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing session_id", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		reqBody := map[string]interface{}{
			"stream_id": streamID.String(),
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/join", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerJoin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("collector error", func(t *testing.T) {
		collector := &mockAnalyticsCollector{
			trackViewerJoinFunc: func(ctx context.Context, sid uuid.UUID, uid *uuid.UUID, sessID string, ip string, ua string) error {
				return errors.New("collector error")
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"stream_id":  streamID.String(),
			"session_id": sessionID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/join", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerJoin(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestTrackViewerLeave(t *testing.T) {
	sessionID := "test-session-123"

	t.Run("success", func(t *testing.T) {
		var capturedSessionID string

		collector := &mockAnalyticsCollector{
			trackViewerLeaveFunc: func(ctx context.Context, sessID string) error {
				capturedSessionID = sessID
				return nil
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"session_id": sessionID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/leave", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerLeave(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, sessionID, capturedSessionID)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/leave", bytes.NewReader([]byte("invalid")))

		w := httptest.NewRecorder()
		handler.TrackViewerLeave(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing session_id", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		reqBody := map[string]interface{}{}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/leave", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerLeave(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("collector error", func(t *testing.T) {
		collector := &mockAnalyticsCollector{
			trackViewerLeaveFunc: func(ctx context.Context, sessID string) error {
				return errors.New("collector error")
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"session_id": sessionID,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/viewer/leave", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackViewerLeave(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestTrackEngagement(t *testing.T) {
	sessionID := "test-session-123"

	t.Run("success", func(t *testing.T) {
		var capturedSessionID string
		var capturedMessages int
		var capturedLiked bool
		var capturedShared bool

		collector := &mockAnalyticsCollector{
			trackEngagementFunc: func(ctx context.Context, sessID string, messages int, liked bool, shared bool) error {
				capturedSessionID = sessID
				capturedMessages = messages
				capturedLiked = liked
				capturedShared = shared
				return nil
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"session_id":    sessionID,
			"messages_sent": 5,
			"liked":         true,
			"shared":        false,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/engagement", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackEngagement(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, sessionID, capturedSessionID)
		assert.Equal(t, 5, capturedMessages)
		assert.True(t, capturedLiked)
		assert.False(t, capturedShared)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/engagement", bytes.NewReader([]byte("invalid")))

		w := httptest.NewRecorder()
		handler.TrackEngagement(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing session_id", func(t *testing.T) {
		handler := NewAnalyticsHandler(nil, nil, nil)

		reqBody := map[string]interface{}{
			"messages_sent": 5,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/engagement", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackEngagement(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("collector error", func(t *testing.T) {
		collector := &mockAnalyticsCollector{
			trackEngagementFunc: func(ctx context.Context, sessID string, messages int, liked bool, shared bool) error {
				return errors.New("collector error")
			},
		}

		handler := NewAnalyticsHandler(nil, nil, collector)

		reqBody := map[string]interface{}{
			"session_id":    sessionID,
			"messages_sent": 5,
			"liked":         true,
			"shared":        false,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/engagement", bytes.NewReader(bodyBytes))

		w := httptest.NewRecorder()
		handler.TrackEngagement(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
