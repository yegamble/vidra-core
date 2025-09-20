package httpapi

import (
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type PlaylistHandlers struct {
	playlistService *usecase.PlaylistService
}

func NewPlaylistHandlers(playlistService *usecase.PlaylistService) *PlaylistHandlers {
	return &PlaylistHandlers{
		playlistService: playlistService,
	}
}

// CreatePlaylist handles POST /api/v1/playlists
func (h *PlaylistHandlers) CreatePlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	var req domain.CreatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid request body"))
		return
	}

	playlist, err := h.playlistService.CreatePlaylist(r.Context(), userID, &req)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusCreated, playlist)
}

// GetPlaylist handles GET /api/v1/playlists/{id}
func (h *PlaylistHandlers) GetPlaylist(w http.ResponseWriter, r *http.Request) {
	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	// Get current user if authenticated
	var userID *uuid.UUID
	if uid, ok := middleware.GetUserIDFromContext(r.Context()); ok {
		userID = &uid
	}

	playlist, err := h.playlistService.GetPlaylist(r.Context(), playlistID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, playlist)
}

// UpdatePlaylist handles PUT /api/v1/playlists/{id}
func (h *PlaylistHandlers) UpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	var req domain.UpdatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid request body"))
		return
	}

	if err := h.playlistService.UpdatePlaylist(r.Context(), userID, playlistID, req); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// DeletePlaylist handles DELETE /api/v1/playlists/{id}
func (h *PlaylistHandlers) DeletePlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	if err := h.playlistService.DeletePlaylist(r.Context(), userID, playlistID); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// ListPlaylists handles GET /api/v1/playlists
func (h *PlaylistHandlers) ListPlaylists(w http.ResponseWriter, r *http.Request) {
	opts := domain.PlaylistListOptions{
		Limit:  20,
		Offset: 0,
	}

	// Parse query params
	if userIDStr := r.URL.Query().Get("user_id"); userIDStr != "" {
		if uid, err := uuid.Parse(userIDStr); err == nil {
			opts.UserID = &uid
		}
	}

	if privacy := r.URL.Query().Get("privacy"); privacy != "" {
		p := domain.Privacy(privacy)
		if p == domain.PrivacyPublic || p == domain.PrivacyUnlisted || p == domain.PrivacyPrivate {
			opts.Privacy = &p
		}
	}

	if limit := r.URL.Query().Get("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil {
			opts.Limit = l
		}
	}

	if offset := r.URL.Query().Get("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil {
			opts.Offset = o
		}
	}

	if orderBy := r.URL.Query().Get("order_by"); orderBy != "" {
		opts.OrderBy = orderBy
	}

	response, err := h.playlistService.ListPlaylists(r.Context(), opts)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, response)
}

// AddVideoToPlaylist handles POST /api/v1/playlists/{id}/items
func (h *PlaylistHandlers) AddVideoToPlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	var req domain.AddToPlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid request body"))
		return
	}

	if err := h.playlistService.AddVideoToPlaylist(r.Context(), userID, playlistID, req.VideoID, req.Position); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]string{
		"status": "ok",
	})
}

// RemoveVideoFromPlaylist handles DELETE /api/v1/playlists/{id}/items/{itemId}
func (h *PlaylistHandlers) RemoveVideoFromPlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	itemIDStr := chi.URLParam(r, "itemId")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid item ID"))
		return
	}

	if err := h.playlistService.RemoveVideoFromPlaylist(r.Context(), userID, playlistID, itemID); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// GetPlaylistItems handles GET /api/v1/playlists/{id}/items
func (h *PlaylistHandlers) GetPlaylistItems(w http.ResponseWriter, r *http.Request) {
	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	// Get current user if authenticated
	var userID *uuid.UUID
	if uid, ok := middleware.GetUserIDFromContext(r.Context()); ok {
		userID = &uid
	}

	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil {
			limit = val
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil {
			offset = val
		}
	}

	items, err := h.playlistService.GetPlaylistItems(r.Context(), playlistID, userID, limit, offset)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"items":  items,
		"limit":  limit,
		"offset": offset,
	})
}

// ReorderPlaylistItem handles PUT /api/v1/playlists/{id}/items/{itemId}/reorder
func (h *PlaylistHandlers) ReorderPlaylistItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid playlist ID"))
		return
	}

	itemIDStr := chi.URLParam(r, "itemId")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid item ID"))
		return
	}

	var req struct {
		NewPosition int `json:"new_position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid request body"))
		return
	}

	if err := h.playlistService.ReorderPlaylistItem(r.Context(), userID, playlistID, itemID, req.NewPosition); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			WriteError(w, http.StatusForbidden, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// GetWatchLater handles GET /api/v1/users/me/watch-later
func (h *PlaylistHandlers) GetWatchLater(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	playlist, err := h.playlistService.GetOrCreateWatchLater(r.Context(), userID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, playlist)
}

// AddToWatchLater handles POST /api/v1/videos/{id}/watch-later
func (h *PlaylistHandlers) AddToWatchLater(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid video ID"))
		return
	}

	if err := h.playlistService.AddToWatchLater(r.Context(), userID, videoID); err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}
