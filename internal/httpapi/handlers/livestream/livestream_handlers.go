package livestream

import (
	"athena/internal/config"
	"athena/internal/httpapi/shared"
	"encoding/json"
	"errors"
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

// LiveStreamHandlers handles live stream-related HTTP requests
type LiveStreamHandlers struct {
	streamRepo    repository.LiveStreamRepository
	streamKeyRepo repository.StreamKeyRepository
	viewerRepo    repository.ViewerSessionRepository
	channelRepo   repository.ChannelRepository
	streamManager *livestream.StreamManager
	config        *config.Config
}

// NewLiveStreamHandlers creates new live stream handlers
func NewLiveStreamHandlers(
	streamRepo repository.LiveStreamRepository,
	streamKeyRepo repository.StreamKeyRepository,
	viewerRepo repository.ViewerSessionRepository,
	channelRepo repository.ChannelRepository,
	streamManager *livestream.StreamManager,
	config *config.Config,
) *LiveStreamHandlers {
	return &LiveStreamHandlers{
		streamRepo:    streamRepo,
		streamKeyRepo: streamKeyRepo,
		viewerRepo:    viewerRepo,
		channelRepo:   channelRepo,
		streamManager: streamManager,
		config:        config,
	}
}

// CreateStreamRequest represents the request to create a live stream
type CreateStreamRequest struct {
	ChannelID   uuid.UUID `json:"channel_id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Privacy     string    `json:"privacy,omitempty"`
	SaveReplay  *bool     `json:"save_replay,omitempty"`
}

// UpdateStreamRequest represents the request to update a live stream
type UpdateStreamRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Privacy     *string `json:"privacy,omitempty"`
}

// StreamResponse represents a live stream response
type StreamResponse struct {
	ID              uuid.UUID  `json:"id"`
	ChannelID       uuid.UUID  `json:"channel_id"`
	UserID          uuid.UUID  `json:"user_id"`
	Status          string     `json:"status"`
	Title           string     `json:"title"`
	Description     string     `json:"description,omitempty"`
	ViewerCount     int        `json:"viewer_count"`
	PeakViewerCount int        `json:"peak_viewer_count"`
	Privacy         string     `json:"privacy"`
	SaveReplay      bool       `json:"save_replay"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	Duration        *int       `json:"duration_seconds,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	RTMPUrl         string     `json:"rtmp_url,omitempty"`
	StreamKey       string     `json:"stream_key,omitempty"` // Only included on creation
}

// StreamStatsResponse represents real-time stream statistics
type StreamStatsResponse struct {
	StreamID        uuid.UUID  `json:"stream_id"`
	Status          string     `json:"status"`
	ViewerCount     int        `json:"viewer_count"`
	PeakViewerCount int        `json:"peak_viewer_count"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	Duration        *int       `json:"duration_seconds,omitempty"`
	LastUpdate      time.Time  `json:"last_update"`
}

// CreateStream handles POST /api/v1/channels/{channelId}/streams
func (h *LiveStreamHandlers) CreateStream(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "channelId")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	var req CreateStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	// Validate channel ID matches request
	if req.ChannelID != channelID {
		shared.WriteError(w, http.StatusBadRequest, errors.New("channel ID mismatch"))
		return
	}

	// Verify user owns the channel
	channel, err := h.channelRepo.GetByID(r.Context(), channelID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("channel not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel"))
		return
	}

	// Check if user owns the channel
	if channel.AccountID != userID {
		shared.WriteError(w, http.StatusForbidden, errors.New("you do not own this channel"))
		return
	}

	// Generate stream key
	streamKeyPlaintext, err := domain.GenerateStreamKey()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to generate stream key"))
		return
	}

	// Create stream key in database
	streamKey, err := h.streamKeyRepo.Create(r.Context(), channelID, streamKeyPlaintext, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to create stream key"))
		return
	}

	// Set defaults
	privacy := req.Privacy
	if privacy == "" {
		privacy = "public"
	}
	saveReplay := true
	if req.SaveReplay != nil {
		saveReplay = *req.SaveReplay
	}

	// Create live stream
	stream := &domain.LiveStream{
		ID:          uuid.New(),
		ChannelID:   channelID,
		UserID:      userID,
		StreamKey:   streamKeyPlaintext, // Will not be returned in response
		Status:      domain.StreamStatusWaiting,
		Title:       req.Title,
		Description: req.Description,
		Privacy:     privacy,
		SaveReplay:  saveReplay,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := h.streamRepo.Create(r.Context(), stream); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to create stream"))
		return
	}

	// Build RTMP URL (without exposing full stream key in URL, key is separate)
	rtmpURL := fmt.Sprintf("rtmp://%s:%d/live", h.config.RTMPHost, h.config.RTMPPort)

	response := StreamResponse{
		ID:              stream.ID,
		ChannelID:       stream.ChannelID,
		UserID:          stream.UserID,
		Status:          stream.Status,
		Title:           stream.Title,
		Description:     stream.Description,
		ViewerCount:     0,
		PeakViewerCount: 0,
		Privacy:         stream.Privacy,
		SaveReplay:      stream.SaveReplay,
		CreatedAt:       stream.CreatedAt,
		UpdatedAt:       stream.UpdatedAt,
		RTMPUrl:         rtmpURL,
		StreamKey:       streamKeyPlaintext, // Only shown on creation
	}

	// Update last used for stream key
	_ = h.streamKeyRepo.MarkUsed(r.Context(), streamKey.ID)

	shared.WriteJSON(w, http.StatusCreated, response)
}

// ListChannelStreams handles GET /api/v1/channels/{channelId}/streams
func (h *LiveStreamHandlers) ListChannelStreams(w http.ResponseWriter, r *http.Request) {
	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "channelId")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	// Parse pagination
	page := 1
	pageSize := 20
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := r.URL.Query().Get("pageSize"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	offset := (page - 1) * pageSize

	// Get streams for channel
	streams, err := h.streamRepo.GetByChannelID(r.Context(), channelID, pageSize, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get streams"))
		return
	}

	// Get total count
	total, err := h.streamRepo.CountByChannelID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to count streams"))
		return
	}

	// Convert to response format (without stream keys)
	responseStreams := make([]StreamResponse, len(streams))
	for i, stream := range streams {
		var duration *int
		if stream.IsLive() || stream.Status == domain.StreamStatusEnded {
			d := int(stream.Duration().Seconds())
			duration = &d
		}

		responseStreams[i] = StreamResponse{
			ID:              stream.ID,
			ChannelID:       stream.ChannelID,
			UserID:          stream.UserID,
			Status:          stream.Status,
			Title:           stream.Title,
			Description:     stream.Description,
			ViewerCount:     stream.ViewerCount,
			PeakViewerCount: stream.PeakViewerCount,
			Privacy:         stream.Privacy,
			SaveReplay:      stream.SaveReplay,
			StartedAt:       stream.StartedAt,
			EndedAt:         stream.EndedAt,
			Duration:        duration,
			CreatedAt:       stream.CreatedAt,
			UpdatedAt:       stream.UpdatedAt,
		}
	}

	response := map[string]interface{}{
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
		"data":     responseStreams,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetStream handles GET /api/v1/streams/{id}
func (h *LiveStreamHandlers) GetStream(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "id")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	var duration *int
	if stream.IsLive() || stream.Status == domain.StreamStatusEnded {
		d := int(stream.Duration().Seconds())
		duration = &d
	}

	response := StreamResponse{
		ID:              stream.ID,
		ChannelID:       stream.ChannelID,
		UserID:          stream.UserID,
		Status:          stream.Status,
		Title:           stream.Title,
		Description:     stream.Description,
		ViewerCount:     stream.ViewerCount,
		PeakViewerCount: stream.PeakViewerCount,
		Privacy:         stream.Privacy,
		SaveReplay:      stream.SaveReplay,
		StartedAt:       stream.StartedAt,
		EndedAt:         stream.EndedAt,
		Duration:        duration,
		CreatedAt:       stream.CreatedAt,
		UpdatedAt:       stream.UpdatedAt,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// UpdateStream handles PUT /api/v1/streams/{id}
func (h *LiveStreamHandlers) UpdateStream(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	streamIDStr := chi.URLParam(r, "id")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	var req UpdateStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	// Get existing stream
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	// Verify ownership
	if stream.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrUnauthorized)
		return
	}

	// Update fields
	if req.Title != nil {
		stream.Title = *req.Title
	}
	if req.Description != nil {
		stream.Description = *req.Description
	}
	if req.Privacy != nil {
		stream.Privacy = *req.Privacy
	}
	stream.UpdatedAt = time.Now()

	if err := h.streamRepo.Update(r.Context(), stream); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to update stream"))
		return
	}

	var duration *int
	if stream.IsLive() || stream.Status == domain.StreamStatusEnded {
		d := int(stream.Duration().Seconds())
		duration = &d
	}

	response := StreamResponse{
		ID:              stream.ID,
		ChannelID:       stream.ChannelID,
		UserID:          stream.UserID,
		Status:          stream.Status,
		Title:           stream.Title,
		Description:     stream.Description,
		ViewerCount:     stream.ViewerCount,
		PeakViewerCount: stream.PeakViewerCount,
		Privacy:         stream.Privacy,
		SaveReplay:      stream.SaveReplay,
		StartedAt:       stream.StartedAt,
		EndedAt:         stream.EndedAt,
		Duration:        duration,
		CreatedAt:       stream.CreatedAt,
		UpdatedAt:       stream.UpdatedAt,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// EndStream handles POST /api/v1/streams/{id}/end
func (h *LiveStreamHandlers) EndStream(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	streamIDStr := chi.URLParam(r, "id")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Get stream to verify ownership
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	// Verify ownership
	if stream.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrUnauthorized)
		return
	}

	// Check if stream is live
	if !stream.IsLive() {
		shared.WriteError(w, http.StatusBadRequest, errors.New("stream is not live"))
		return
	}

	// End the stream via stream manager
	if h.streamManager != nil {
		if err := h.streamManager.EndStream(r.Context(), streamID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to end stream"))
			return
		}
	}

	// Get updated stream
	stream, err = h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get updated stream"))
		return
	}

	var duration *int
	if stream.EndedAt != nil {
		d := int(stream.Duration().Seconds())
		duration = &d
	}

	response := StreamResponse{
		ID:              stream.ID,
		ChannelID:       stream.ChannelID,
		UserID:          stream.UserID,
		Status:          stream.Status,
		Title:           stream.Title,
		Description:     stream.Description,
		ViewerCount:     stream.ViewerCount,
		PeakViewerCount: stream.PeakViewerCount,
		Privacy:         stream.Privacy,
		SaveReplay:      stream.SaveReplay,
		StartedAt:       stream.StartedAt,
		EndedAt:         stream.EndedAt,
		Duration:        duration,
		CreatedAt:       stream.CreatedAt,
		UpdatedAt:       stream.UpdatedAt,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetStreamStats handles GET /api/v1/streams/{id}/stats
func (h *LiveStreamHandlers) GetStreamStats(w http.ResponseWriter, r *http.Request) {
	streamIDStr := chi.URLParam(r, "id")
	streamID, err := uuid.Parse(streamIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid stream ID"))
		return
	}

	// Check if stream is active in stream manager (real-time data)
	if h.streamManager != nil {
		if state, exists := h.streamManager.GetStreamState(streamID); exists {
			var duration *int
			if state.Status == domain.StreamStatusLive {
				d := int(time.Since(state.StartedAt).Seconds())
				duration = &d
			}

			response := StreamStatsResponse{
				StreamID:        state.StreamID,
				Status:          state.Status,
				ViewerCount:     state.ViewerCount,
				PeakViewerCount: state.PeakViewers,
				StartedAt:       &state.StartedAt,
				Duration:        duration,
				LastUpdate:      state.LastUpdate,
			}

			shared.WriteJSON(w, http.StatusOK, response)
			return
		}
	}

	// Fall back to database if not in memory
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream"))
		return
	}

	var duration *int
	if stream.IsLive() || stream.Status == domain.StreamStatusEnded {
		d := int(stream.Duration().Seconds())
		duration = &d
	}

	response := StreamStatsResponse{
		StreamID:        stream.ID,
		Status:          stream.Status,
		ViewerCount:     stream.ViewerCount,
		PeakViewerCount: stream.PeakViewerCount,
		StartedAt:       stream.StartedAt,
		Duration:        duration,
		LastUpdate:      stream.UpdatedAt,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// RotateStreamKey handles POST /api/v1/channels/{channelId}/stream-keys/rotate
func (h *LiveStreamHandlers) RotateStreamKey(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "channelId")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	// Verify user owns the channel
	channel, err := h.channelRepo.GetByID(r.Context(), channelID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("channel not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel"))
		return
	}

	// Check if user owns the channel
	if channel.AccountID != userID {
		shared.WriteError(w, http.StatusForbidden, errors.New("you do not own this channel"))
		return
	}

	// Get existing active key
	existingKey, err := h.streamKeyRepo.GetActiveByChannelID(r.Context(), channelID)
	if err != nil && err != domain.ErrNotFound {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get stream key"))
		return
	}

	// Deactivate existing key if it exists
	if existingKey != nil {
		if err := h.streamKeyRepo.DeactivateAllForChannel(r.Context(), channelID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to deactivate old keys"))
			return
		}
	}

	// Generate new stream key
	newKeyPlaintext, err := domain.GenerateStreamKey()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to generate stream key"))
		return
	}

	// Create new stream key
	streamKey, err := h.streamKeyRepo.Create(r.Context(), channelID, newKeyPlaintext, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to create stream key"))
		return
	}

	response := map[string]interface{}{
		"message":    "Stream key rotated successfully",
		"channel_id": channelID,
		"key_id":     streamKey.ID,
		"stream_key": newKeyPlaintext, // Only shown on rotation
		"created_at": streamKey.CreatedAt,
		"expires_at": streamKey.ExpiresAt,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetActiveStreams handles GET /api/v1/streams/active
func (h *LiveStreamHandlers) GetActiveStreams(w http.ResponseWriter, r *http.Request) {
	// Parse pagination
	page := 1
	pageSize := 20
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := r.URL.Query().Get("pageSize"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	offset := (page - 1) * pageSize

	// Get active streams from database
	streams, err := h.streamRepo.GetActiveStreams(r.Context(), pageSize, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get active streams"))
		return
	}

	// Enhance with real-time data from stream manager
	responseStreams := make([]StreamResponse, len(streams))
	for i, stream := range streams {
		var duration *int
		if stream.IsLive() {
			d := int(stream.Duration().Seconds())
			duration = &d
		}

		// Try to get real-time viewer count from stream manager
		viewerCount := stream.ViewerCount
		peakViewers := stream.PeakViewerCount
		if h.streamManager != nil {
			if state, exists := h.streamManager.GetStreamState(stream.ID); exists {
				viewerCount = state.ViewerCount
				peakViewers = state.PeakViewers
			}
		}

		responseStreams[i] = StreamResponse{
			ID:              stream.ID,
			ChannelID:       stream.ChannelID,
			UserID:          stream.UserID,
			Status:          stream.Status,
			Title:           stream.Title,
			Description:     stream.Description,
			ViewerCount:     viewerCount,
			PeakViewerCount: peakViewers,
			Privacy:         stream.Privacy,
			SaveReplay:      stream.SaveReplay,
			StartedAt:       stream.StartedAt,
			EndedAt:         stream.EndedAt,
			Duration:        duration,
			CreatedAt:       stream.CreatedAt,
			UpdatedAt:       stream.UpdatedAt,
		}
	}

	// Get total count of active streams
	total := len(streams)
	if h.streamManager != nil {
		total = h.streamManager.GetActiveStreamCount()
	}

	response := map[string]interface{}{
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
		"data":     responseStreams,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}
