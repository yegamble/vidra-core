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

type mockVideoAnalyticsService struct {
	trackEventFunc                    func(ctx context.Context, event *domain.AnalyticsEvent) error
	trackEventsBatchFunc              func(ctx context.Context, events []*domain.AnalyticsEvent) error
	trackViewerHeartbeatFunc          func(ctx context.Context, videoID uuid.UUID, sessionID string, userID *uuid.UUID) error
	getVideoAnalyticsSummaryFunc      func(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error)
	getDailyAnalyticsRangeFunc        func(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error)
	getRetentionCurveFunc             func(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error)
	getActiveViewersFunc              func(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error)
	getActiveViewerCountFunc          func(ctx context.Context, videoID uuid.UUID) (int, error)
	getChannelDailyAnalyticsRangeFunc func(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error)
	getChannelTotalViewsFunc          func(ctx context.Context, channelID uuid.UUID) (int, error)
}

func (m *mockVideoAnalyticsService) TrackEvent(ctx context.Context, event *domain.AnalyticsEvent) error {
	if m.trackEventFunc != nil {
		return m.trackEventFunc(ctx, event)
	}
	return nil
}

func (m *mockVideoAnalyticsService) TrackEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error {
	if m.trackEventsBatchFunc != nil {
		return m.trackEventsBatchFunc(ctx, events)
	}
	return nil
}

func (m *mockVideoAnalyticsService) TrackViewerHeartbeat(ctx context.Context, videoID uuid.UUID, sessionID string, userID *uuid.UUID) error {
	if m.trackViewerHeartbeatFunc != nil {
		return m.trackViewerHeartbeatFunc(ctx, videoID, sessionID, userID)
	}
	return nil
}

func (m *mockVideoAnalyticsService) GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
	if m.getVideoAnalyticsSummaryFunc != nil {
		return m.getVideoAnalyticsSummaryFunc(ctx, videoID, startDate, endDate)
	}
	return nil, nil
}

func (m *mockVideoAnalyticsService) GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
	if m.getDailyAnalyticsRangeFunc != nil {
		return m.getDailyAnalyticsRangeFunc(ctx, videoID, startDate, endDate)
	}
	return nil, nil
}

func (m *mockVideoAnalyticsService) GetRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
	if m.getRetentionCurveFunc != nil {
		return m.getRetentionCurveFunc(ctx, videoID, date)
	}
	return nil, nil
}

func (m *mockVideoAnalyticsService) GetActiveViewers(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error) {
	if m.getActiveViewersFunc != nil {
		return m.getActiveViewersFunc(ctx, videoID)
	}
	return nil, nil
}

func (m *mockVideoAnalyticsService) GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error) {
	if m.getActiveViewerCountFunc != nil {
		return m.getActiveViewerCountFunc(ctx, videoID)
	}
	return 0, nil
}

func (m *mockVideoAnalyticsService) GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
	if m.getChannelDailyAnalyticsRangeFunc != nil {
		return m.getChannelDailyAnalyticsRangeFunc(ctx, channelID, startDate, endDate)
	}
	return nil, nil
}

func (m *mockVideoAnalyticsService) GetChannelTotalViews(ctx context.Context, channelID uuid.UUID) (int, error) {
	if m.getChannelTotalViewsFunc != nil {
		return m.getChannelTotalViewsFunc(ctx, channelID)
	}
	return 0, nil
}

func TestTrackEvent_Success(t *testing.T) {
	videoID := uuid.New()
	tracked := false

	mockService := &mockVideoAnalyticsService{
		trackEventFunc: func(ctx context.Context, event *domain.AnalyticsEvent) error {
			tracked = true
			assert.Equal(t, videoID, event.VideoID)
			assert.Equal(t, domain.EventType("play"), event.EventType)
			return nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	reqBody := TrackEventRequest{
		VideoID:   videoID.String(),
		EventType: "play",
		SessionID: "session123",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader(body))
	req.Header.Set("User-Agent", "TestAgent/1.0")
	w := httptest.NewRecorder()

	handler.TrackEvent(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, tracked)
}

func TestTrackEvent_InvalidJSON(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	handler.TrackEvent(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid request body")
}

func TestTrackEvent_InvalidVideoID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	reqBody := TrackEventRequest{
		VideoID:   "invalid-uuid",
		EventType: "play",
		SessionID: "session123",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEvent(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTrackEvent_ServiceError(t *testing.T) {
	mockService := &mockVideoAnalyticsService{
		trackEventFunc: func(ctx context.Context, event *domain.AnalyticsEvent) error {
			return errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	reqBody := TrackEventRequest{
		VideoID:   uuid.New().String(),
		EventType: "play",
		SessionID: "session123",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEvent(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestTrackEventsBatch_Success(t *testing.T) {
	videoID := uuid.New()
	tracked := false

	mockService := &mockVideoAnalyticsService{
		trackEventsBatchFunc: func(ctx context.Context, events []*domain.AnalyticsEvent) error {
			tracked = true
			assert.Len(t, events, 2)
			assert.Equal(t, videoID, events[0].VideoID)
			return nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	reqBody := TrackBatchRequest{
		Events: []TrackEventRequest{
			{VideoID: videoID.String(), EventType: "play", SessionID: "session1"},
			{VideoID: videoID.String(), EventType: "pause", SessionID: "session1"},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEventsBatch(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, tracked)
}

func TestTrackEventsBatch_InvalidJSON(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	handler.TrackEventsBatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid request body")
}

func TestTrackEventsBatch_EmptyEvents(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	reqBody := TrackBatchRequest{Events: []TrackEventRequest{}}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEventsBatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTrackEventsBatch_TooManyEvents(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	events := make([]TrackEventRequest, 101)
	for i := range events {
		events[i] = TrackEventRequest{
			VideoID:   uuid.New().String(),
			EventType: "play",
			SessionID: "session1",
		}
	}

	reqBody := TrackBatchRequest{Events: events}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEventsBatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTrackEventsBatch_InvalidVideoIDInBatch(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	events := []TrackEventRequest{
		{
			VideoID:   uuid.New().String(),
			EventType: "play",
			SessionID: "session1",
		},
		{
			VideoID:   "invalid-uuid",
			EventType: "pause",
			SessionID: "session1",
		},
	}

	reqBody := TrackBatchRequest{Events: events}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEventsBatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid video ID in event 1")
}

func TestTrackEventsBatch_ServiceError(t *testing.T) {
	mockService := &mockVideoAnalyticsService{
		trackEventsBatchFunc: func(ctx context.Context, events []*domain.AnalyticsEvent) error {
			return errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	events := []TrackEventRequest{
		{
			VideoID:   uuid.New().String(),
			EventType: "play",
			SessionID: "session1",
		},
	}

	reqBody := TrackBatchRequest{Events: events}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.TrackEventsBatch(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to track events")
}

func TestTrackHeartbeat_Success(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	tracked := false

	mockService := &mockVideoAnalyticsService{
		trackViewerHeartbeatFunc: func(ctx context.Context, vid uuid.UUID, sessionID string, uid *uuid.UUID) error {
			tracked = true
			assert.Equal(t, videoID, vid)
			assert.Equal(t, "session123", sessionID)
			assert.Equal(t, userID, *uid)
			return nil
		},
		getActiveViewerCountFunc: func(ctx context.Context, vid uuid.UUID) (int, error) {
			return 42, nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	reqBody := map[string]string{"session_id": "session123"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/analytics/heartbeat", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())

	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	handler.TrackHeartbeat(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, tracked)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp["status"])
	assert.Equal(t, float64(42), resp["active_count"])
}

func TestTrackHeartbeat_InvalidVideoID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	reqBody := map[string]string{"session_id": "session123"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/invalid-uuid/analytics/heartbeat", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.TrackHeartbeat(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid video ID")
}

func TestTrackHeartbeat_EmptySessionID(t *testing.T) {
	videoID := uuid.New()
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	reqBody := map[string]string{"session_id": ""}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/analytics/heartbeat", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.TrackHeartbeat(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Session ID is required")
}

func TestTrackHeartbeat_InvalidJSON(t *testing.T) {
	videoID := uuid.New()
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/analytics/heartbeat", bytes.NewReader([]byte("invalid json")))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.TrackHeartbeat(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid request body")
}

func TestTrackHeartbeat_ServiceError(t *testing.T) {
	videoID := uuid.New()
	mockService := &mockVideoAnalyticsService{
		trackViewerHeartbeatFunc: func(ctx context.Context, vid uuid.UUID, sessionID string, userID *uuid.UUID) error {
			return errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	reqBody := map[string]string{"session_id": "session123"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/analytics/heartbeat", bytes.NewReader(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.TrackHeartbeat(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to track heartbeat")
}

func TestGetVideoAnalytics_Success(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getVideoAnalyticsSummaryFunc: func(ctx context.Context, vid uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
			assert.Equal(t, videoID, vid)
			return &domain.AnalyticsSummary{
				VideoID:    videoID,
				TotalViews: 1000,
			}, nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.GetVideoAnalytics(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summary domain.AnalyticsSummary
	err := json.NewDecoder(w.Body).Decode(&summary)
	require.NoError(t, err)
	assert.Equal(t, videoID, summary.VideoID)
	assert.Equal(t, int(1000), summary.TotalViews)
}

func TestGetVideoAnalytics_InvalidVideoID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid-uuid/analytics", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetVideoAnalytics(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid video ID")
}

func TestGetVideoAnalytics_ServiceError(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getVideoAnalyticsSummaryFunc: func(ctx context.Context, vid uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetVideoAnalytics(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get analytics summary")
}

func TestGetActiveViewers_Success(t *testing.T) {
	videoID := uuid.New()
	viewer1 := &domain.ActiveViewer{VideoID: videoID, SessionID: "s1"}
	viewer2 := &domain.ActiveViewer{VideoID: videoID, SessionID: "s2"}

	mockService := &mockVideoAnalyticsService{
		getActiveViewerCountFunc: func(ctx context.Context, vid uuid.UUID) (int, error) {
			return 2, nil
		},
		getActiveViewersFunc: func(ctx context.Context, vid uuid.UUID) ([]*domain.ActiveViewer, error) {
			return []*domain.ActiveViewer{viewer1, viewer2}, nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/active-viewers", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.GetActiveViewers(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, float64(2), resp["count"])
}

func TestGetActiveViewers_InvalidVideoID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid-uuid/analytics/active-viewers", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetActiveViewers(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid video ID")
}

func TestGetActiveViewers_CountError(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getActiveViewerCountFunc: func(ctx context.Context, vid uuid.UUID) (int, error) {
			return 0, errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/active-viewers", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetActiveViewers(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get active viewer count")
}

func TestGetActiveViewers_ViewersError(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getActiveViewerCountFunc: func(ctx context.Context, vid uuid.UUID) (int, error) {
			return 2, nil
		},
		getActiveViewersFunc: func(ctx context.Context, vid uuid.UUID) ([]*domain.ActiveViewer, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/active-viewers", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetActiveViewers(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get active viewers")
}

func TestGetDailyAnalytics_Success(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getDailyAnalyticsRangeFunc: func(ctx context.Context, vid uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
			return []*domain.DailyAnalytics{
				{VideoID: vid, Date: time.Now(), Views: 50, UniqueViewers: 30},
			}, nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/daily", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.GetDailyAnalytics(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var dailyAnalytics []*domain.DailyAnalytics
	err := json.NewDecoder(w.Body).Decode(&dailyAnalytics)
	require.NoError(t, err)
	assert.Len(t, dailyAnalytics, 1)
	assert.Equal(t, videoID, dailyAnalytics[0].VideoID)
}

func TestGetDailyAnalytics_InvalidVideoID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid-uuid/analytics/daily", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetDailyAnalytics(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid video ID")
}

func TestGetDailyAnalytics_ServiceError(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getDailyAnalyticsRangeFunc: func(ctx context.Context, vid uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/daily", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetDailyAnalytics(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get daily analytics")
}

func TestGetRetentionCurve_Success(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getRetentionCurveFunc: func(ctx context.Context, vid uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
			return []*domain.RetentionData{
				{TimestampSeconds: 0, ViewerCount: 100},
				{TimestampSeconds: 30, ViewerCount: 80},
			}, nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/retention?date=2024-01-01", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.GetRetentionCurve(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var retention []*domain.RetentionData
	err := json.NewDecoder(w.Body).Decode(&retention)
	require.NoError(t, err)
	assert.Len(t, retention, 2)
	assert.Equal(t, 100, retention[0].ViewerCount)
}

func TestGetRetentionCurve_InvalidVideoID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid-uuid/analytics/retention?date=2024-01-01", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetRetentionCurve(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid video ID")
}

func TestGetRetentionCurve_ServiceError(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getRetentionCurveFunc: func(ctx context.Context, vid uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/analytics/retention?date=2024-01-01", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoID", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetRetentionCurve(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get retention curve")
}

func TestGetChannelAnalytics_Success(t *testing.T) {
	channelID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getChannelDailyAnalyticsRangeFunc: func(ctx context.Context, cid uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
			return []*domain.ChannelDailyAnalytics{
				{ChannelID: cid, Date: time.Now(), Views: 100},
			}, nil
		},
		getChannelTotalViewsFunc: func(ctx context.Context, cid uuid.UUID) (int, error) {
			return 5000, nil
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channelID.String()+"/analytics", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelID", channelID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.GetChannelAnalytics(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, float64(5000), resp["total_views"])
}

func TestGetChannelAnalytics_InvalidChannelID(t *testing.T) {
	handler := NewVideoAnalyticsHandler(&mockVideoAnalyticsService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/invalid-uuid/analytics", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelID", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetChannelAnalytics(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid channel ID")
}

func TestGetChannelAnalytics_ServiceError(t *testing.T) {
	channelID := uuid.New()

	mockService := &mockVideoAnalyticsService{
		getChannelDailyAnalyticsRangeFunc: func(ctx context.Context, cid uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewVideoAnalyticsHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channelID.String()+"/analytics", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelID", channelID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetChannelAnalytics(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get channel analytics")
}

func TestParseDateRange_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics", nil)

	startDate, endDate, err := parseDateRange(req)

	require.NoError(t, err)
	assert.True(t, startDate.Before(endDate))
	assert.True(t, time.Until(endDate) < time.Hour)
}

func TestParseDateRange_ValidDates(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics?start_date=2024-01-01&end_date=2024-01-31", nil)

	startDate, endDate, err := parseDateRange(req)

	require.NoError(t, err)
	assert.Equal(t, 2024, startDate.Year())
	assert.Equal(t, time.Month(1), startDate.Month())
	assert.Equal(t, 1, startDate.Day())
	assert.Equal(t, 31, endDate.Day())
}

func TestParseDateRange_InvalidStartDate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics?start_date=invalid", nil)

	_, _, err := parseDateRange(req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start_date format")
}

func TestParseDateRange_InvalidEndDate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics?end_date=invalid", nil)

	_, _, err := parseDateRange(req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid end_date format")
}

func TestParseDateRange_OnlyStartDate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics?start_date=2024-01-01", nil)

	startDate, endDate, err := parseDateRange(req)

	require.NoError(t, err)
	assert.Equal(t, 2024, startDate.Year())
	assert.Equal(t, time.Month(1), startDate.Month())
	assert.Equal(t, 1, startDate.Day())
	assert.True(t, time.Until(endDate) < time.Hour)
}

func TestParseDateRange_OnlyEndDate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics?end_date=2024-12-31", nil)

	startDate, endDate, err := parseDateRange(req)

	require.NoError(t, err)
	assert.True(t, startDate.Sub(time.Now().AddDate(0, 0, -30)) < time.Hour)
	assert.Equal(t, 2024, endDate.Year())
	assert.Equal(t, time.Month(12), endDate.Month())
	assert.Equal(t, 31, endDate.Day())
}
