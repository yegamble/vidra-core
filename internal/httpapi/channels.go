package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ChannelHandlers handles channel-related HTTP requests
type ChannelHandlers struct {
	channelService *usecase.ChannelService
}

// NewChannelHandlers creates new channel handlers
func NewChannelHandlers(channelService *usecase.ChannelService) *ChannelHandlers {
	return &ChannelHandlers{
		channelService: channelService,
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
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to list channels"))
		return
	}

	WriteJSON(w, http.StatusOK, response)
}

// GetChannel handles GET /api/v1/channels/{id}
func (h *ChannelHandlers) GetChannel(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")

	// Try to parse as UUID first
	if channelID, err := uuid.Parse(idParam); err == nil {
		channel, err := h.channelService.GetChannel(r.Context(), channelID)
		if err != nil {
			if err == domain.ErrNotFound {
				WriteError(w, http.StatusNotFound, domain.ErrNotFound)
				return
			}
			WriteError(w, http.StatusInternalServerError, errors.New("Failed to get channel"))
			return
		}
		WriteJSON(w, http.StatusOK, channel)
		return
	}

	// If not a UUID, try as handle
	channel, err := h.channelService.GetChannelByHandle(r.Context(), idParam)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to get channel"))
		return
	}

	WriteJSON(w, http.StatusOK, channel)
}

// CreateChannel handles POST /api/v1/channels
func (h *ChannelHandlers) CreateChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	var req domain.ChannelCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("Invalid request body"))
		return
	}

	channel, err := h.channelService.CreateChannel(r.Context(), userID, req)
	if err != nil {
		if err == domain.ErrDuplicateEntry {
			WriteError(w, http.StatusConflict, domain.ErrDuplicateEntry)
			return
		}
		if err == domain.ErrInvalidInput {
			WriteError(w, http.StatusBadRequest, domain.ErrInvalidInput)
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to create channel"))
		return
	}

	WriteJSON(w, http.StatusCreated, channel)
}

// UpdateChannel handles PUT /api/v1/channels/{id}
func (h *ChannelHandlers) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("Invalid channel ID"))
		return
	}

	var req domain.ChannelUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("Invalid request body"))
		return
	}

	channel, err := h.channelService.UpdateChannel(r.Context(), userID, channelID, req)
	if err != nil {
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, domain.ErrUnauthorized)
			return
		}
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		if err == domain.ErrInvalidInput {
			WriteError(w, http.StatusBadRequest, domain.ErrInvalidInput)
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to update channel"))
		return
	}

	WriteJSON(w, http.StatusOK, channel)
}

// DeleteChannel handles DELETE /api/v1/channels/{id}
func (h *ChannelHandlers) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Get channel ID from URL
	channelIDStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errors.New("Invalid channel ID"))
		return
	}

	err = h.channelService.DeleteChannel(r.Context(), userID, channelID)
	if err != nil {
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, domain.ErrUnauthorized)
			return
		}
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, domain.ErrNotFound)
			return
		}
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to delete channel"))
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
				WriteError(w, http.StatusNotFound, domain.ErrNotFound)
				return
			}
			WriteError(w, http.StatusInternalServerError, errors.New("Failed to get channel"))
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
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to get channel videos"))
		return
	}

	WriteJSON(w, http.StatusOK, response)
}

// GetMyChannels handles GET /api/v1/users/me/channels
func (h *ChannelHandlers) GetMyChannels(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	channels, err := h.channelService.GetUserChannels(r.Context(), userID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errors.New("Failed to get channels"))
		return
	}

	// Format as a list response
	response := map[string]interface{}{
		"total": len(channels),
		"data":  channels,
	}

	WriteJSON(w, http.StatusOK, response)
}

// SubscribeToChannel handles POST /api/v1/channels/{id}/subscribe
func (h *ChannelHandlers) SubscribeToChannel(w http.ResponseWriter, r *http.Request) {
	// This will be implemented when we update subscriptions to be channel-based
	// For now, return not implemented
	WriteError(w, http.StatusNotImplemented, errors.New("Channel subscriptions not yet implemented"))
}

// UnsubscribeFromChannel handles DELETE /api/v1/channels/{id}/subscribe
func (h *ChannelHandlers) UnsubscribeFromChannel(w http.ResponseWriter, r *http.Request) {
	// This will be implemented when we update subscriptions to be channel-based
	// For now, return not implemented
	WriteError(w, http.StatusNotImplemented, errors.New("Channel subscriptions not yet implemented"))
}
