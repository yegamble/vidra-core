package channel

import (
	"athena/internal/httpapi/shared"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
	ucchannel "athena/internal/usecase/channel"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ChannelHandlers handles channel-related HTTP requests
type ChannelHandlers struct {
	channelService *ucchannel.Service
	subRepo        usecase.SubscriptionRepository
}

// NewChannelHandlers creates new channel handlers
func NewChannelHandlers(channelService *ucchannel.Service, subRepo usecase.SubscriptionRepository) *ChannelHandlers {
	return &ChannelHandlers{
		channelService: channelService,
		subRepo:        subRepo,
	}
}

// ListChannels handles GET /api/v1/channels
func (h *ChannelHandlers) ListChannels(w http.ResponseWriter, r *http.Request) {
	params := domain.ChannelListParams{
		Search: r.URL.Query().Get("search"),
		Sort:   r.URL.Query().Get("sort"),
	}

	// Parse pagination
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if page, err := strconv.Atoi(pageStr); err == nil {
			params.Page = page
		}
	}
	if pageSizeStr := r.URL.Query().Get("pageSize"); pageSizeStr != "" {
		if pageSize, err := strconv.Atoi(pageSizeStr); err == nil {
			params.PageSize = pageSize
		}
	}

	// Parse filters
	if accountIDStr := r.URL.Query().Get("accountId"); accountIDStr != "" {
		if accountID, err := uuid.Parse(accountIDStr); err == nil {
			params.AccountID = &accountID
		}
	}
	if isLocalStr := r.URL.Query().Get("isLocal"); isLocalStr != "" {
		isLocal := isLocalStr == "true"
		params.IsLocal = &isLocal
	}

	response, err := h.channelService.ListChannels(r.Context(), params)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to list channels"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetChannel handles GET /api/v1/channels/{id}
func (h *ChannelHandlers) GetChannel(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	// Try to parse as UUID first
	if channelID, err := uuid.Parse(idParam); err == nil {
		channel, err := h.channelService.GetChannel(r.Context(), channelID)
		if err != nil {
			if err == domain.ErrNotFound {
				shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel"))
			return
		}
		shared.WriteJSON(w, http.StatusOK, channel)
		return
	}

	// If not a UUID, try as handle
	channel, err := h.channelService.GetChannelByHandle(r.Context(), idParam)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, channel)
}

// CreateChannel handles POST /api/v1/channels
func (h *ChannelHandlers) CreateChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	var req domain.ChannelCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	channel, err := h.channelService.CreateChannel(r.Context(), userID, req)
	if err != nil {
		if err == domain.ErrDuplicateEntry {
			shared.WriteError(w, http.StatusConflict, domain.ErrDuplicateEntry)
			return
		}
		if err == domain.ErrInvalidInput {
			shared.WriteError(w, http.StatusBadRequest, domain.ErrInvalidInput)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to create channel"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, channel)
}

// UpdateChannel handles PUT /api/v1/channels/{id}
func (h *ChannelHandlers) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	var req domain.ChannelUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}

	channel, err := h.channelService.UpdateChannel(r.Context(), userID, channelID, req)
	if err != nil {
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, domain.ErrUnauthorized)
			return
		}
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		if err == domain.ErrInvalidInput {
			shared.WriteError(w, http.StatusBadRequest, domain.ErrInvalidInput)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to update channel"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, channel)
}

// DeleteChannel handles DELETE /api/v1/channels/{id}
func (h *ChannelHandlers) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	err = h.channelService.DeleteChannel(r.Context(), userID, channelID)
	if err != nil {
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, domain.ErrUnauthorized)
			return
		}
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to delete channel"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetChannelVideos handles GET /api/v1/channels/{id}/videos
func (h *ChannelHandlers) GetChannelVideos(w http.ResponseWriter, r *http.Request) {
	// Get channel ID from URL
	idParam := chi.URLParam(r, "id")

	var channelID uuid.UUID
	var err error

	// Try to parse as UUID first
	channelID, err = uuid.Parse(idParam)
	if err != nil {
		// If not a UUID, try to get channel by handle
		channel, err := h.channelService.GetChannelByHandle(r.Context(), idParam)
		if err != nil {
			if err == domain.ErrNotFound {
				shared.WriteError(w, http.StatusNotFound, domain.ErrNotFound)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel"))
			return
		}
		channelID = channel.ID
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

	response, err := h.channelService.GetChannelVideos(r.Context(), channelID, page, pageSize)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel videos"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetMyChannels handles GET /api/v1/users/me/channels
func (h *ChannelHandlers) GetMyChannels(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	channels, err := h.channelService.GetUserChannels(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channels"))
		return
	}

	// Format as a list response
	response := map[string]interface{}{
		"total": len(channels),
		"data":  channels,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// SubscribeToChannel handles POST /api/v1/channels/{id}/subscribe
func (h *ChannelHandlers) SubscribeToChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	// Subscribe to channel
	err = h.subRepo.SubscribeToChannel(r.Context(), userID, channelID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, errors.New("channel not found"))
			return
		}
		if err.Error() == "cannot subscribe to your own channel" {
			shared.WriteError(w, http.StatusBadRequest, errors.New("cannot subscribe to your own channel"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to subscribe to channel"))
		return
	}

	// Return success response
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Successfully subscribed to channel",
		"channel_id": channelID,
	})
}

// UnsubscribeFromChannel handles DELETE /api/v1/channels/{id}/subscribe
func (h *ChannelHandlers) UnsubscribeFromChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid channel ID"))
		return
	}

	// Unsubscribe from channel
	err = h.subRepo.UnsubscribeFromChannel(r.Context(), userID, channelID)
	if err != nil {
		if err == domain.ErrNotFound {
			// Already unsubscribed or channel doesn't exist
			shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
				"message":    "Not subscribed to channel",
				"channel_id": channelID,
			})
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to unsubscribe from channel"))
		return
	}

	// Return success response
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Successfully unsubscribed from channel",
		"channel_id": channelID,
	})
}

// GetChannelSubscribers handles GET /api/v1/channels/{id}/subscribers
func (h *ChannelHandlers) GetChannelSubscribers(w http.ResponseWriter, r *http.Request) {
	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
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

	// Get subscribers
	response, err := h.subRepo.ListChannelSubscribers(r.Context(), channelID, pageSize, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channel subscribers"))
		return
	}

	// Format response
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"total":    response.Total,
		"page":     page,
		"pageSize": pageSize,
		"data":     response.Data,
	})
}
