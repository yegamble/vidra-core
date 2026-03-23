package video

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type VideoAnalyticsHandler struct {
	analyticsService VideoAnalyticsService
}

func NewVideoAnalyticsHandler(analyticsService VideoAnalyticsService) *VideoAnalyticsHandler {
	return &VideoAnalyticsHandler{
		analyticsService: analyticsService,
	}
}

type TrackEventRequest struct {
	VideoID           string `json:"video_id"`
	EventType         string `json:"event_type"`
	SessionID         string `json:"session_id"`
	TimestampSeconds  *int   `json:"timestamp_seconds,omitempty"`
	WatchDurationSecs *int   `json:"watch_duration_seconds,omitempty"`
	Quality           string `json:"quality,omitempty"`
	PlayerVersion     string `json:"player_version,omitempty"`
	Referrer          string `json:"referrer,omitempty"`
}

type TrackBatchRequest struct {
	Events []TrackEventRequest `json:"events"`
}

func (h *VideoAnalyticsHandler) TrackEvent(w http.ResponseWriter, r *http.Request) {
	var req TrackEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", nil)
		return
	}

	videoID, err := uuid.Parse(req.VideoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", nil)
		return
	}

	event := &domain.AnalyticsEvent{
		VideoID:           videoID,
		EventType:         domain.EventType(req.EventType),
		SessionID:         req.SessionID,
		TimestampSeconds:  req.TimestampSeconds,
		WatchDurationSecs: req.WatchDurationSecs,
		Quality:           req.Quality,
		PlayerVersion:     req.PlayerVersion,
		Referrer:          req.Referrer,
		UserAgent:         r.Header.Get("User-Agent"),
	}

	ipAddr := r.RemoteAddr
	event.IPAddress = &ipAddr

	if userID := getUserIDFromContext(r.Context()); userID != nil {
		event.UserID = userID
	}

	if err := h.analyticsService.TrackEvent(r.Context(), event); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to track event", nil)
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *VideoAnalyticsHandler) TrackEventsBatch(w http.ResponseWriter, r *http.Request) {
	var req TrackBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", nil)
		return
	}

	if len(req.Events) == 0 {
		respondWithError(w, http.StatusBadRequest, "No events provided", nil)
		return
	}

	if len(req.Events) > 100 {
		respondWithError(w, http.StatusBadRequest, "Maximum 100 events per batch", nil)
		return
	}

	events := make([]*domain.AnalyticsEvent, len(req.Events))
	for i, eventReq := range req.Events {
		videoID, err := uuid.Parse(eventReq.VideoID)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid video ID in event "+strconv.Itoa(i), nil)
			return
		}

		ipAddr := r.RemoteAddr
		events[i] = &domain.AnalyticsEvent{
			VideoID:           videoID,
			EventType:         domain.EventType(eventReq.EventType),
			SessionID:         eventReq.SessionID,
			TimestampSeconds:  eventReq.TimestampSeconds,
			WatchDurationSecs: eventReq.WatchDurationSecs,
			Quality:           eventReq.Quality,
			PlayerVersion:     eventReq.PlayerVersion,
			Referrer:          eventReq.Referrer,
			UserAgent:         r.Header.Get("User-Agent"),
			IPAddress:         &ipAddr,
		}

		if userID := getUserIDFromContext(r.Context()); userID != nil {
			events[i].UserID = userID
		}
	}

	if err := h.analyticsService.TrackEventsBatch(r.Context(), events); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to track events", nil)
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]string{"status": "ok", "count": strconv.Itoa(len(events))})
}

func (h *VideoAnalyticsHandler) TrackHeartbeat(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoID")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", nil)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", nil)
		return
	}

	if req.SessionID == "" {
		respondWithError(w, http.StatusBadRequest, "Session ID is required", nil)
		return
	}

	userID := getUserIDFromContext(r.Context())

	if err := h.analyticsService.TrackViewerHeartbeat(r.Context(), videoID, req.SessionID, userID); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to track heartbeat", nil)
		return
	}

	count, _ := h.analyticsService.GetActiveViewerCount(r.Context(), videoID)

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "ok",
		"active_count": count,
	})
}

func (h *VideoAnalyticsHandler) GetVideoAnalytics(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoID")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", nil)
		return
	}

	startDate, endDate, err := parseDateRange(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	summary, err := h.analyticsService.GetVideoAnalyticsSummary(r.Context(), videoID, startDate, endDate)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get analytics summary", nil)
		return
	}

	respondWithJSON(w, http.StatusOK, summary)
}

func (h *VideoAnalyticsHandler) GetDailyAnalytics(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoID")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", nil)
		return
	}

	startDate, endDate, err := parseDateRange(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	dailyAnalytics, err := h.analyticsService.GetDailyAnalyticsRange(r.Context(), videoID, startDate, endDate)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get daily analytics", nil)
		return
	}

	respondWithJSON(w, http.StatusOK, dailyAnalytics)
}

func (h *VideoAnalyticsHandler) GetRetentionCurve(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoID")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", nil)
		return
	}

	dateStr := r.URL.Query().Get("date")
	var date time.Time
	if dateStr != "" {
		date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid date format (use YYYY-MM-DD)", nil)
			return
		}
	} else {
		date = time.Now()
	}

	retention, err := h.analyticsService.GetRetentionCurve(r.Context(), videoID, date)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get retention curve", nil)
		return
	}

	respondWithJSON(w, http.StatusOK, retention)
}

func (h *VideoAnalyticsHandler) GetActiveViewers(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoID")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", nil)
		return
	}

	count, err := h.analyticsService.GetActiveViewerCount(r.Context(), videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get active viewer count", nil)
		return
	}

	viewers, err := h.analyticsService.GetActiveViewers(r.Context(), videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get active viewers", nil)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"count":   count,
		"viewers": viewers,
	})
}

func (h *VideoAnalyticsHandler) GetChannelAnalytics(w http.ResponseWriter, r *http.Request) {
	channelIDStr := chi.URLParam(r, "channelID")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid channel ID", nil)
		return
	}

	startDate, endDate, err := parseDateRange(r)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	analytics, err := h.analyticsService.GetChannelDailyAnalyticsRange(r.Context(), channelID, startDate, endDate)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get channel analytics", nil)
		return
	}

	totalViews, _ := h.analyticsService.GetChannelTotalViews(r.Context(), channelID)

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"total_views": totalViews,
		"daily":       analytics,
	})
}

func getUserIDFromContext(ctx context.Context) *uuid.UUID {
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		return nil
	}
	return &userID
}

func parseDateRange(r *http.Request) (startDate, endDate time.Time, err error) {
	startDateStr := r.URL.Query().Get("start_date")
	endDateStr := r.URL.Query().Get("end_date")

	if startDateStr != "" {
		startDate, err = time.Parse("2006-01-02", startDateStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date format (use YYYY-MM-DD)")
		}
	} else {
		startDate = time.Now().AddDate(0, 0, -30)
	}

	if endDateStr != "" {
		endDate, err = time.Parse("2006-01-02", endDateStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date format (use YYYY-MM-DD)")
		}
	} else {
		endDate = time.Now()
	}

	return startDate, endDate, nil
}
