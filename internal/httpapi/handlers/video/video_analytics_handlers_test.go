package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase/analytics"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoAnalyticsHandler_TrackEvent_WithUser(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	analyticsRepo := repository.NewVideoAnalyticsRepository(testDB.DB)
	analyticsService := analytics.NewService(analyticsRepo, nil)
	handler := NewVideoAnalyticsHandler(analyticsService)

	userID := uuid.New().String()
	video := createTestViewsVideo(t, testDB, userID)

	t.Run("successful event tracking with authenticated user", func(t *testing.T) {
		reqBody := TrackEventRequest{
			VideoID:   video.ID,
			EventType: "view",
			SessionID: uuid.New().String(),
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		userUUID := uuid.MustParse(userID)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/analytics/events", handler.TrackEvent)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		events, err := analyticsRepo.GetEventsBySessionID(context.Background(), reqBody.SessionID)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.NotNil(t, events[0].UserID)
		assert.Equal(t, userUUID, *events[0].UserID)
	})

	t.Run("successful event tracking without user (anonymous)", func(t *testing.T) {
		reqBody := TrackEventRequest{
			VideoID:   video.ID,
			EventType: "view",
			SessionID: uuid.New().String(),
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/analytics/events", handler.TrackEvent)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		events, err := analyticsRepo.GetEventsBySessionID(context.Background(), reqBody.SessionID)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Nil(t, events[0].UserID)
	})

	t.Run("batch event tracking with authenticated user", func(t *testing.T) {
		sessionID := uuid.New().String()
		reqBody := TrackBatchRequest{
			Events: []TrackEventRequest{
				{
					VideoID:   video.ID,
					EventType: "view",
					SessionID: sessionID,
				},
				{
					VideoID:   video.ID,
					EventType: "play",
					SessionID: sessionID,
				},
			},
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events/batch", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		userUUID := uuid.MustParse(userID)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/analytics/events/batch", handler.TrackEventsBatch)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		events, err := analyticsRepo.GetEventsBySessionID(context.Background(), sessionID)
		require.NoError(t, err)
		require.Len(t, events, 2)
		for _, event := range events {
			assert.NotNil(t, event.UserID)
			assert.Equal(t, userUUID, *event.UserID)
		}
	})
}
