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
	analyticsService := analytics.NewService(analyticsRepo)
	handler := NewVideoAnalyticsHandler(analyticsService)

	// Create test video
	userID := uuid.New().String()
	// createTestViewsVideo is defined in views_handlers_test.go which is in the same package
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

		// Add user to context
		userUUID := uuid.MustParse(userID)
		// We can inject the user directly via middleware.UserIDKey as a string because
		// middleware.GetUserIDFromContext expects the raw value from context to be string
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/analytics/events", handler.TrackEvent)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		// Verify event was created with correct UserID
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

		// No user in context

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/analytics/events", handler.TrackEvent)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		// Verify event was created with nil UserID
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

		// Add user to context
		userUUID := uuid.MustParse(userID)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/analytics/events/batch", handler.TrackEventsBatch)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		// Verify events were created with correct UserID
		events, err := analyticsRepo.GetEventsBySessionID(context.Background(), sessionID)
		require.NoError(t, err)
		require.Len(t, events, 2)
		for _, event := range events {
			assert.NotNil(t, event.UserID)
			assert.Equal(t, userUUID, *event.UserID)
		}
	})
}
