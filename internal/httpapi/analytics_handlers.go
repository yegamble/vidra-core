package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"athena/internal/domain"
	"athena/internal/livestream"
	"athena/internal/middleware"
	"athena/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// StreamRepositoryForAnalytics interface for analytics handlers
type StreamRepositoryForAnalytics interface {
	GetByID(ctx context.Context, id string) (*domain.LiveStream, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
}

// AnalyticsHandler handles analytics-related HTTP requests
type AnalyticsHandler struct {
	streamRepo StreamRepositoryForAnalytics
	analytics  repository.AnalyticsRepository
	collector  *livestream.AnalyticsCollector
}

// NewAnalyticsHandler creates a new analytics handler
func NewAnalyticsHandler(
	streamRepo StreamRepositoryForAnalytics,
	analytics repository.AnalyticsRepository,
	collector *livestream.AnalyticsCollector,
) *AnalyticsHandler {
	return &AnalyticsHandler{
		streamRepo: streamRepo,
		analytics:  analytics,
		collector:  collector,
	}
}

// RegisterRoutes registers analytics routes
func (h *AnalyticsHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/streams/{streamId}/analytics", func(r chi.Router) {
		r.Use(middleware.RequireAuth)
		r.Get("/", h.GetStreamAnalytics)
		r.Get("/summary", h.GetStreamSummary)
		r.Get("/chart", h.GetAnalyticsChart)
		r.Get("/current", h.GetCurrentAnalytics)
	})

	// Viewer tracking endpoints
	r.Route("/api/v1/analytics", func(r chi.Router) {
		r.Post("/viewer/join", h.TrackViewerJoin)
		r.Post("/viewer/leave", h.TrackViewerLeave)
		r.Post("/engagement", h.TrackEngagement)
	})
}

// GetStreamAnalytics returns detailed analytics for a stream
func (h *AnalyticsHandler) GetStreamAnalytics(w http.ResponseWriter, r *http.Request) {
	// Parse stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Verify user has access to this stream's analytics
	stream, err := h.streamRepo.GetByID(r.Context(), streamID.String())
	if err != nil {
		WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
		return
	}

	// Check if user owns the channel
	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		WriteError(w, http.StatusForbidden, fmt.Errorf("you can only view analytics for your own streams"))
		return
	}

	// Parse time range from query params
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	intervalStr := r.URL.Query().Get("interval")

	// Default to last 24 hours if not specified
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)
	interval := 5 // Default 5 minute intervals

	if startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = parsed
		}
	}

	if endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = parsed
		}
	}

	if intervalStr != "" {
		if parsed, err := strconv.Atoi(intervalStr); err == nil && parsed > 0 {
			interval = parsed
		}
	}

	timeRange := &domain.AnalyticsTimeRange{
		StartTime: startTime,
		EndTime:   endTime,
		Interval:  interval,
	}

	// Get analytics data
	analytics, err := h.analytics.GetAnalyticsByStream(r.Context(), streamID, timeRange)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get analytics"))
		return
	}

	WriteJSON(w, http.StatusOK, analytics)
}

// GetStreamSummary returns summary statistics for a stream
func (h *AnalyticsHandler) GetStreamSummary(w http.ResponseWriter, r *http.Request) {
	// Parse stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Verify user has access
	stream, err := h.streamRepo.GetByID(r.Context(), streamID.String())
	if err != nil {
		WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		WriteError(w, http.StatusForbidden, fmt.Errorf("you can only view analytics for your own streams"))
		return
	}

	// Get summary statistics
	summary, err := h.analytics.GetStreamSummary(r.Context(), streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream summary"))
		return
	}

	if summary == nil {
		// Create empty summary if none exists
		summary = &domain.StreamStatsSummary{
			StreamID: streamID,
		}
	}

	WriteJSON(w, http.StatusOK, summary)
}

// GetAnalyticsChart returns time-series data formatted for charting
func (h *AnalyticsHandler) GetAnalyticsChart(w http.ResponseWriter, r *http.Request) {
	// Parse stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Verify user has access
	stream, err := h.streamRepo.GetByID(r.Context(), streamID.String())
	if err != nil {
		WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		WriteError(w, http.StatusForbidden, fmt.Errorf("you can only view analytics for your own streams"))
		return
	}

	// Parse time range
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	intervalStr := r.URL.Query().Get("interval")

	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)
	interval := 5

	if startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = parsed
		}
	}

	if endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = parsed
		}
	}

	if intervalStr != "" {
		if parsed, err := strconv.Atoi(intervalStr); err == nil && parsed > 0 {
			interval = parsed
		}
	}

	timeRange := &domain.AnalyticsTimeRange{
		StartTime: startTime,
		EndTime:   endTime,
		Interval:  interval,
	}

	// Get time-series data
	dataPoints, err := h.analytics.GetAnalyticsTimeSeries(r.Context(), streamID, timeRange)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get analytics chart data"))
		return
	}

	// Format response for charting libraries
	response := map[string]interface{}{
		"stream_id": streamID,
		"time_range": map[string]interface{}{
			"start":    startTime,
			"end":      endTime,
			"interval": interval,
		},
		"data": dataPoints,
	}

	WriteJSON(w, http.StatusOK, response)
}

// GetCurrentAnalytics returns real-time analytics for a stream
func (h *AnalyticsHandler) GetCurrentAnalytics(w http.ResponseWriter, r *http.Request) {
	// Parse stream ID
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get current viewer count
	viewerCount, err := h.analytics.GetCurrentViewerCount(r.Context(), streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get current analytics"))
		return
	}

	// Get latest analytics record
	latest, err := h.analytics.GetLatestAnalytics(r.Context(), streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get latest analytics"))
		return
	}

	response := map[string]interface{}{
		"stream_id":     streamID,
		"viewer_count":  viewerCount,
		"collected_at":  time.Now(),
		"is_live":       true,
		"latest_record": latest,
	}

	WriteJSON(w, http.StatusOK, response)
}

// TrackViewerJoin tracks when a viewer joins a stream
func (h *AnalyticsHandler) TrackViewerJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamID  uuid.UUID  `json:"stream_id"`
		UserID    *uuid.UUID `json:"user_id,omitempty"`
		SessionID string     `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	// Validate required fields
	if req.StreamID == uuid.Nil || req.SessionID == "" {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("stream_id and session_id are required"))
		return
	}

	// Get IP address and user agent
	ipAddress := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ipAddress = realIP
	} else if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ipAddress = forwarded
	}

	userAgent := r.UserAgent()

	// Track the viewer join
	err := h.collector.TrackViewerJoin(r.Context(), req.StreamID, req.UserID, req.SessionID, ipAddress, userAgent)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to track viewer join"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status":     "success",
		"session_id": req.SessionID,
	})
}

// TrackViewerLeave tracks when a viewer leaves a stream
func (h *AnalyticsHandler) TrackViewerLeave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.SessionID == "" {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("session_id is required"))
		return
	}

	// Track the viewer leave
	err := h.collector.TrackViewerLeave(r.Context(), req.SessionID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to track viewer leave"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TrackEngagement tracks viewer engagement activities
func (h *AnalyticsHandler) TrackEngagement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID    string `json:"session_id"`
		MessagesSent int    `json:"messages_sent"`
		Liked        bool   `json:"liked"`
		Shared       bool   `json:"shared"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.SessionID == "" {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("session_id is required"))
		return
	}

	// Track the engagement
	err := h.collector.TrackEngagement(r.Context(), req.SessionID, req.MessagesSent, req.Liked, req.Shared)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to track engagement"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
