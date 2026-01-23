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
	"athena/internal/usecase/analytics"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoAnalyticsHandler_TrackEvent_WithUser_Mock(t *testing.T) {
	// Setup sqlmock
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")

	// Setup layers
	analyticsRepo := repository.NewVideoAnalyticsRepository(sqlxDB)
	analyticsService := analytics.NewService(analyticsRepo)
	handler := NewVideoAnalyticsHandler(analyticsService)

	// Create test data
	userIDStr := uuid.New().String()
	userUUID := uuid.MustParse(userIDStr)
	videoID := uuid.New()
	sessionID := uuid.New().String()

	reqBody := TrackEventRequest{
		VideoID:   videoID.String(),
		EventType: "play",
		SessionID: sessionID,
	}

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Add user to context
	// We inject the user directly via middleware.UserIDKey as a string
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userIDStr)
	req = req.WithContext(ctx)

	// Expect Exec
	mock.ExpectExec("INSERT INTO video_analytics_events").
		WithArgs(
			sqlmock.AnyArg(), // id
			videoID,          // video_id
			"play",           // event_type
			userUUID,         // user_id - THIS IS WHAT WE ARE VERIFYING
			sessionID,        // session_id
			sqlmock.AnyArg(), // timestamp_seconds
			sqlmock.AnyArg(), // watch_duration_seconds
			sqlmock.AnyArg(), // ip_address
			sqlmock.AnyArg(), // user_agent
			sqlmock.AnyArg(), // country_code
			sqlmock.AnyArg(), // region
			sqlmock.AnyArg(), // city
			sqlmock.AnyArg(), // device_type
			sqlmock.AnyArg(), // browser
			sqlmock.AnyArg(), // os
			sqlmock.AnyArg(), // referrer
			sqlmock.AnyArg(), // quality
			sqlmock.AnyArg(), // player_version
			sqlmock.AnyArg(), // created_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Post("/api/v1/analytics/events", handler.TrackEvent)
	router.ServeHTTP(rr, req)

	// Check response
	assert.Equal(t, http.StatusCreated, rr.Code)

	// Verify mock
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}
