package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// channelHandleResolver resolves a channel handle to a domain.Channel.
type channelHandleResolver interface {
	GetChannelByHandle(ctx context.Context, handle string) (*domain.Channel, error)
}

type PlaylistHandlers struct {
	playlistService PlaylistServiceInterface
}

func NewPlaylistHandlers(playlistService PlaylistServiceInterface) *PlaylistHandlers {
	return &PlaylistHandlers{
		playlistService: playlistService,
	}
}

func (h *PlaylistHandlers) CreatePlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	var req domain.CreatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	playlist, err := h.playlistService.CreatePlaylist(r.Context(), userID, &req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, playlist)
}

func (h *PlaylistHandlers) GetPlaylist(w http.ResponseWriter, r *http.Request) {
	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

	var userID *uuid.UUID
	if uid, ok := middleware.GetUserIDFromContext(r.Context()); ok {
		userID = &uid
	}

	playlist, err := h.playlistService.GetPlaylist(r.Context(), playlistID, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, playlist)
}

func (h *PlaylistHandlers) UpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

	var req domain.UpdatePlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if err := h.playlistService.UpdatePlaylist(r.Context(), userID, playlistID, req); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (h *PlaylistHandlers) DeletePlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

	if err := h.playlistService.DeletePlaylist(r.Context(), userID, playlistID); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (h *PlaylistHandlers) ListPlaylists(w http.ResponseWriter, r *http.Request) {
	opts := domain.PlaylistListOptions{
		Limit:  20,
		Offset: 0,
	}

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
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (h *PlaylistHandlers) AddVideoToPlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

	var req domain.AddToPlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if err := h.playlistService.AddVideoToPlaylist(r.Context(), userID, playlistID, req.VideoID, req.Position); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]string{
		"status": "ok",
	})
}

func (h *PlaylistHandlers) RemoveVideoFromPlaylist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

	itemIDStr := chi.URLParam(r, "itemId")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid item ID"))
		return
	}

	if err := h.playlistService.RemoveVideoFromPlaylist(r.Context(), userID, playlistID, itemID); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (h *PlaylistHandlers) GetPlaylistItems(w http.ResponseWriter, r *http.Request) {
	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

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
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"items":  items,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *PlaylistHandlers) ReorderPlaylistItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	playlistIDStr := chi.URLParam(r, "id")
	playlistID, err := uuid.Parse(playlistIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid playlist ID"))
		return
	}

	itemIDStr := chi.URLParam(r, "itemId")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid item ID"))
		return
	}

	var req struct {
		NewPosition int `json:"new_position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if err := h.playlistService.ReorderPlaylistItem(r.Context(), userID, playlistID, itemID, req.NewPosition); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (h *PlaylistHandlers) GetWatchLater(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	playlist, err := h.playlistService.GetOrCreateWatchLater(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, playlist)
}

func (h *PlaylistHandlers) AddToWatchLater(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	if err := h.playlistService.AddToWatchLater(r.Context(), userID, videoID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// GetPrivacies handles GET /api/v1/video-playlists/privacies.
// Returns the map of numeric privacy IDs to labels, matching PeerTube's response.
func (h *PlaylistHandlers) GetPrivacies(w http.ResponseWriter, r *http.Request) {
	privacies := map[string]string{
		"1": "Public",
		"2": "Unlisted",
		"3": "Private",
	}
	shared.WriteJSON(w, http.StatusOK, privacies)
}

// GetChannelPlaylistsHandler handles GET /video-channels/{channelHandle}/video-playlists.
func GetChannelPlaylistsHandler(channelSvc channelHandleResolver, playlistSvc PlaylistServiceInterface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle := chi.URLParam(r, "channelHandle")

		channel, err := channelSvc.GetChannelByHandle(r.Context(), handle)
		if err != nil {
			if err == domain.ErrNotFound {
				shared.WriteError(w, http.StatusNotFound, fmt.Errorf("channel not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		opts := domain.PlaylistListOptions{
			UserID: &channel.UserID,
			Limit:  20,
			Offset: 0,
		}

		resp, err := playlistSvc.ListPlaylists(r.Context(), opts)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		shared.WriteJSON(w, http.StatusOK, resp)
	}
}
