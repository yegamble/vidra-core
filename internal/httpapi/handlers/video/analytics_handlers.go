package video

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/livestream"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type StreamRepositoryForAnalytics interface {
	GetByID(ctx context.Context, id string) (*domain.LiveStream, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
}

type AnalyticsCollectorInterface interface {
	TrackViewerJoin(ctx context.Context, req livestream.ViewerJoinRequest) error
	TrackViewerLeave(ctx context.Context, sessionID string) error
	TrackEngagement(ctx context.Context, sessionID string, messagesSent int, liked bool, shared bool) error
}

type AnalyticsHandler struct {
	streamRepo StreamRepositoryForAnalytics
	analytics  repository.AnalyticsRepository
	collector  AnalyticsCollectorInterface
}

func NewAnalyticsHandler(
	streamRepo StreamRepositoryForAnalytics,
	analytics repository.AnalyticsRepository,
	collector AnalyticsCollectorInterface,
) *AnalyticsHandler {
	return &AnalyticsHandler{
		streamRepo: streamRepo,
		analytics:  analytics,
		collector:  collector,
	}
}

func (h *AnalyticsHandler) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Route("/api/v1/streams/{streamId}/analytics", func(r chi.Router) {
		r.Use(middleware.Auth(jwtSecret))
		r.Get("/", h.GetStreamAnalytics)
		r.Get("/summary", h.GetStreamSummary)
		r.Get("/chart", h.GetAnalyticsChart)
		r.Get("/current", h.GetCurrentAnalytics)
	})

	r.Route("/api/v1/analytics", func(r chi.Router) {
		r.Post("/viewer/join", h.TrackViewerJoin)
		r.Post("/viewer/leave", h.TrackViewerLeave)
		r.Post("/engagement", h.TrackEngagement)
	})
}

func (h *AnalyticsHandler) GetStreamAnalytics(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID.String())
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you can only view analytics for your own streams"))
		return
	}

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

	analytics, err := h.analytics.GetAnalyticsByStream(r.Context(), streamID, timeRange)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get analytics"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, analytics)
}

func (h *AnalyticsHandler) GetStreamSummary(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID.String())
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you can only view analytics for your own streams"))
		return
	}

	summary, err := h.analytics.GetStreamSummary(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream summary"))
		return
	}

	if summary == nil {
		summary = &domain.StreamStatsSummary{
			StreamID: streamID,
		}
	}

	shared.WriteJSON(w, http.StatusOK, summary)
}

func (h *AnalyticsHandler) GetAnalyticsChart(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID.String())
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you can only view analytics for your own streams"))
		return
	}

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

	dataPoints, err := h.analytics.GetAnalyticsTimeSeries(r.Context(), streamID, timeRange)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get analytics chart data"))
		return
	}

	response := map[string]interface{}{
		"stream_id": streamID,
		"time_range": map[string]interface{}{
			"start":    startTime,
			"end":      endTime,
			"interval": interval,
		},
		"data": dataPoints,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (h *AnalyticsHandler) GetCurrentAnalytics(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	viewerCount, err := h.analytics.GetCurrentViewerCount(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get current analytics"))
		return
	}

	latest, err := h.analytics.GetLatestAnalytics(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get latest analytics"))
		return
	}

	response := map[string]interface{}{
		"stream_id":     streamID,
		"viewer_count":  viewerCount,
		"collected_at":  time.Now(),
		"is_live":       true,
		"latest_record": latest,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (h *AnalyticsHandler) TrackViewerJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamID  uuid.UUID  `json:"stream_id"`
		UserID    *uuid.UUID `json:"user_id,omitempty"`
		SessionID string     `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.StreamID == uuid.Nil || req.SessionID == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("stream_id and session_id are required"))
		return
	}

	ipAddress := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ipAddress = realIP
	} else if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ipAddress = forwarded
	}

	userAgent := r.UserAgent()

	joinReq := livestream.ViewerJoinRequest{
		StreamID:  req.StreamID,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		IPAddress: ipAddress,
		UserAgent: userAgent,
	}

	err := h.collector.TrackViewerJoin(r.Context(), joinReq)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to track viewer join"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status":     "success",
		"session_id": req.SessionID,
	})
}

func (h *AnalyticsHandler) TrackViewerLeave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.SessionID == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("session_id is required"))
		return
	}

	err := h.collector.TrackViewerLeave(r.Context(), req.SessionID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to track viewer leave"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AnalyticsHandler) TrackEngagement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID    string `json:"session_id"`
		MessagesSent int    `json:"messages_sent"`
		Liked        bool   `json:"liked"`
		Shared       bool   `json:"shared"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.SessionID == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("session_id is required"))
		return
	}

	err := h.collector.TrackEngagement(r.Context(), req.SessionID, req.MessagesSent, req.Liked, req.Shared)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to track engagement"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
