package livestream

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/middleware"
)

// WaitingRoomHandler handles waiting room operations
type WaitingRoomHandler struct {
	streamRepo StreamRepository
	userRepo   UserRepository
}

// NewWaitingRoomHandler creates a new waiting room handler
func NewWaitingRoomHandler(streamRepo StreamRepository, userRepo UserRepository) *WaitingRoomHandler {
	return &WaitingRoomHandler{
		streamRepo: streamRepo,
		userRepo:   userRepo,
	}
}

// WaitingRoomInfo represents waiting room information
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

// ScheduleStreamRequest represents a request to schedule a stream
type ScheduleStreamRequest struct {
	ScheduledStart     time.Time  `json:"scheduled_start" validate:"required"`
	ScheduledEnd       *time.Time `json:"scheduled_end,omitempty"`
	WaitingRoomEnabled bool       `json:"waiting_room_enabled"`
	WaitingRoomMessage string     `json:"waiting_room_message,omitempty" validate:"max=500"`
}

// UpdateWaitingRoomRequest represents a request to update waiting room settings
type UpdateWaitingRoomRequest struct {
	WaitingRoomEnabled bool   `json:"waiting_room_enabled"`
	WaitingRoomMessage string `json:"waiting_room_message,omitempty" validate:"max=500"`
}

// RegisterWaitingRoomRoutes registers waiting room routes
func (h *WaitingRoomHandler) RegisterWaitingRoomRoutes(r chi.Router) {
	r.Route("/api/v1/streams/{streamId}/waiting-room", func(r chi.Router) {
		r.Get("/", h.GetWaitingRoom)
		r.With(middleware.RequireAuth).Put("/", h.UpdateWaitingRoom)
	})

	r.Route("/api/v1/streams/{streamId}/schedule", func(r chi.Router) {
		r.With(middleware.RequireAuth).Post("/", h.ScheduleStream)
		r.With(middleware.RequireAuth).Delete("/", h.CancelSchedule)
	})

	r.Get("/api/v1/streams/scheduled", h.GetScheduledStreams)
	r.Get("/api/v1/streams/upcoming", h.GetUpcomingStreams)
}

// GetWaitingRoom gets waiting room information for a stream
func (h *WaitingRoomHandler) GetWaitingRoom(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get stream information
	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	// Check if stream is in waiting room or scheduled status
	if stream.Status != "waiting_room" && stream.Status != "scheduled" {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("stream is not in waiting room or scheduled status"))
		return
	}

	// Get channel information
	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel information"))
		return
	}

	// Calculate time until start
	var timeUntilStart *string
	if stream.ScheduledStart != nil && stream.ScheduledStart.After(time.Now()) {
		duration := time.Until(*stream.ScheduledStart)
		formatted := formatDuration(duration)
		timeUntilStart = &formatted
	}

	// Get viewer count if stream is in waiting room
	viewerCount := 0
	if stream.Status == "waiting_room" {
		// This would typically come from a WebSocket server or cache
		// For now, we'll return 0
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

	WriteJSON(w, http.StatusOK, response)
}

// UpdateWaitingRoom updates waiting room settings for a stream
func (h *WaitingRoomHandler) UpdateWaitingRoom(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Get stream to verify ownership
	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	// Verify the user owns the channel
	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		WriteError(w, http.StatusForbidden, fmt.Errorf("you can only update your own stream's waiting room"))
		return
	}

	// Parse request body
	var req UpdateWaitingRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	// Update waiting room settings
	err = h.streamRepo.UpdateWaitingRoom(r.Context(), streamID, req.WaitingRoomEnabled, req.WaitingRoomMessage)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update waiting room settings"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ScheduleStream schedules a stream
func (h *WaitingRoomHandler) ScheduleStream(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Parse request body
	var req ScheduleStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	// Validate scheduled times
	if req.ScheduledStart.Before(time.Now()) {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("scheduled start time must be in the future"))
		return
	}

	if req.ScheduledEnd != nil && req.ScheduledEnd.Before(req.ScheduledStart) {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("scheduled end time must be after start time"))
		return
	}

	// Get stream to verify ownership
	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	// Verify the user owns the channel
	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		WriteError(w, http.StatusForbidden, fmt.Errorf("you can only schedule your own streams"))
		return
	}

	// Update stream scheduling
	err = h.streamRepo.ScheduleStream(r.Context(), streamID, &req.ScheduledStart, req.ScheduledEnd,
		req.WaitingRoomEnabled, req.WaitingRoomMessage)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to schedule stream"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"stream_id":            streamID,
		"scheduled_start":      req.ScheduledStart,
		"scheduled_end":        req.ScheduledEnd,
		"waiting_room_enabled": req.WaitingRoomEnabled,
		"status":               "scheduled",
	})
}

// CancelSchedule cancels a scheduled stream
func (h *WaitingRoomHandler) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "streamId")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid stream ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok || userID == uuid.Nil {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Get stream to verify ownership
	stream, err := h.streamRepo.GetByID(r.Context(), streamIDStr)
	if err != nil {
		if err == sql.ErrNoRows {
			WriteError(w, http.StatusNotFound, fmt.Errorf("stream not found"))
			return
		}
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stream"))
		return
	}

	// Verify the user owns the channel
	channel, err := h.streamRepo.GetChannelByID(r.Context(), stream.ChannelID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get channel"))
		return
	}

	if channel.UserID != userID {
		WriteError(w, http.StatusForbidden, fmt.Errorf("you can only cancel your own scheduled streams"))
		return
	}

	// Cancel the schedule
	err = h.streamRepo.CancelSchedule(r.Context(), streamID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to cancel scheduled stream"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetScheduledStreams gets all scheduled streams
func (h *WaitingRoomHandler) GetScheduledStreams(w http.ResponseWriter, r *http.Request) {
	// Get pagination parameters
	limit := 20
	offset := 0

	// Get scheduled streams
	streams, err := h.streamRepo.GetScheduledStreams(r.Context(), limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get scheduled streams"))
		return
	}

	WriteJSON(w, http.StatusOK, streams)
}

// GetUpcomingStreams gets upcoming streams for the authenticated user
func (h *WaitingRoomHandler) GetUpcomingStreams(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (optional - can work without auth)
	userID, _ := middleware.GetUserIDFromContext(r.Context())

	// Get upcoming streams
	streams, err := h.streamRepo.GetUpcomingStreams(r.Context(), userID, 10)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get upcoming streams"))
		return
	}

	WriteJSON(w, http.StatusOK, streams)
}

// StreamRepository interface extension for waiting room operations
type StreamRepository interface {
	GetByID(ctx context.Context, id string) (*domain.LiveStream, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
	UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error
	ScheduleStream(ctx context.Context, streamID uuid.UUID, scheduledStart *time.Time, scheduledEnd *time.Time, waitingRoomEnabled bool, waitingRoomMessage string) error
	CancelSchedule(ctx context.Context, streamID uuid.UUID) error
	GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error)
	GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error)
}

// UserRepository interface for user operations
type UserRepository interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// formatDuration formats a duration into a human-readable string
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
