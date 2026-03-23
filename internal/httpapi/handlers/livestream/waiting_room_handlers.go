package livestream

import (
	"vidra-core/internal/httpapi/shared"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

type WaitingRoomHandler struct {
	streamRepo StreamRepository
	userRepo   UserRepository
}

func NewWaitingRoomHandler(streamRepo StreamRepository, userRepo UserRepository) *WaitingRoomHandler {
	return &WaitingRoomHandler{
		streamRepo: streamRepo,
		userRepo:   userRepo,
	}
}

type WaitingRoomInfo struct {
	StreamID           uuid.UUID  `json:"stream_id"`
	Title              string     `json:"title"`
	ChannelID          uuid.UUID  `json:"channel_id"`
	ChannelName        string     `json:"channel_name"`
	ScheduledStart     *time.Time `json:"scheduled_start,omitempty"`
	ScheduledEnd       *time.Time `json:"scheduled_end,omitempty"`
	WaitingRoomEnabled bool       `json:"waiting_room_enabled"`
	WaitingRoomMessage string     `json:"waiting_room_message,omitempty"`
	Status             string     `json:"status"`
	TimeUntilStart     *string    `json:"time_until_start,omitempty"`
	ViewerCount        int        `json:"viewer_count"`
}

type ScheduleStreamRequest struct {
	ScheduledStart     time.Time  `json:"scheduled_start" validate:"required"`
	ScheduledEnd       *time.Time `json:"scheduled_end,omitempty"`
	WaitingRoomEnabled bool       `json:"waiting_room_enabled"`
	WaitingRoomMessage string     `json:"waiting_room_message,omitempty" validate:"max=500"`
}

type UpdateWaitingRoomRequest struct {
	WaitingRoomEnabled bool   `json:"waiting_room_enabled"`
	WaitingRoomMessage string `json:"waiting_room_message,omitempty" validate:"max=500"`
}

func (h *WaitingRoomHandler) RegisterWaitingRoomRoutes(r chi.Router, jwtSecret string) {
	r.Route("/api/v1/streams/{streamId}/waiting-room", func(r chi.Router) {
		r.Get("/", h.GetWaitingRoom)
		r.With(middleware.Auth(jwtSecret)).Put("/", h.UpdateWaitingRoom)
	})

	r.Route("/api/v1/streams/{streamId}/schedule", func(r chi.Router) {
		r.With(middleware.Auth(jwtSecret)).Post("/", h.ScheduleStream)
		r.With(middleware.Auth(jwtSecret)).Delete("/", h.CancelSchedule)
	})

	r.Get("/api/v1/streams/scheduled", h.GetScheduledStreams)
	r.Get("/api/v1/streams/upcoming", h.GetUpcomingStreams)
}

func (h *WaitingRoomHandler) GetWaitingRoom(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	if stream.Status != "waiting_room" && stream.Status != "scheduled" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("stream is not in waiting room or scheduled status"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel information"))
		return
	}

	var timeUntilStart *string
	if stream.ScheduledStart != nil && stream.ScheduledStart.After(time.Now()) {
		duration := time.Until(*stream.ScheduledStart)
		formatted := formatDuration(duration)
		timeUntilStart = &formatted
	}

	viewerCount := 0
	if stream.Status == "waiting_room" {
		viewerCount = 0
	}

	response := WaitingRoomInfo{
		StreamID:           streamID,
		Title:              stream.Title,
		ChannelID:          stream.ChannelID,
		ChannelName:        channel.Name,
		ScheduledStart:     stream.ScheduledStart,
		ScheduledEnd:       stream.ScheduledEnd,
		WaitingRoomEnabled: stream.WaitingRoomEnabled,
		WaitingRoomMessage: stream.WaitingRoomMessage,
		Status:             stream.Status,
		TimeUntilStart:     timeUntilStart,
		ViewerCount:        viewerCount,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (h *WaitingRoomHandler) UpdateWaitingRoom(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you can only update your own stream's waiting room"))
		return
	}

	var req UpdateWaitingRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	err = h.streamRepo.UpdateWaitingRoom(r.Context(), streamID, req.WaitingRoomEnabled, req.WaitingRoomMessage)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update waiting room settings"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *WaitingRoomHandler) ScheduleStream(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	var req ScheduleStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.ScheduledStart.Before(time.Now()) {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("scheduled start time must be in the future"))
		return
	}

	if req.ScheduledEnd != nil && req.ScheduledEnd.Before(req.ScheduledStart) {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("scheduled end time must be after start time"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you can only schedule your own streams"))
		return
	}

	err = h.streamRepo.ScheduleStream(r.Context(), streamID, &req.ScheduledStart, req.ScheduledEnd,
		req.WaitingRoomEnabled, req.WaitingRoomMessage)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to schedule stream"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"stream_id":            streamID,
		"scheduled_start":      req.ScheduledStart,
		"scheduled_end":        req.ScheduledEnd,
		"waiting_room_enabled": req.WaitingRoomEnabled,
		"status":               "scheduled",
	})
}

func (h *WaitingRoomHandler) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you can only cancel your own scheduled streams"))
		return
	}

	err = h.streamRepo.CancelSchedule(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to cancel scheduled stream"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *WaitingRoomHandler) GetScheduledStreams(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0

	streams, err := h.streamRepo.GetScheduledStreams(r.Context(), limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get scheduled streams"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, streams)
}

func (h *WaitingRoomHandler) GetUpcomingStreams(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.GetUserIDFromContext(r.Context())

	streams, err := h.streamRepo.GetUpcomingStreams(r.Context(), userID, 10)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get upcoming streams"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, streams)
}

type StreamRepository interface {
	GetByID(ctx context.Context, id string) (*domain.LiveStream, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
	UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error
	ScheduleStream(ctx context.Context, streamID uuid.UUID, scheduledStart *time.Time, scheduledEnd *time.Time, waitingRoomEnabled bool, waitingRoomMessage string) error
	CancelSchedule(ctx context.Context, streamID uuid.UUID) error
	GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error)
	GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error)
}

type UserRepository interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour

	hours := d / time.Hour
	d -= hours * time.Hour

	minutes := d / time.Minute
	d -= minutes * time.Minute

	seconds := d / time.Second

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
