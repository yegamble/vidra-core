package player

import (
	"context"
	"encoding/json"
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// PlayerSettingsRepository defines the storage interface for player settings.
type PlayerSettingsRepository interface {
	GetByVideoID(ctx context.Context, videoID string) (*domain.PlayerSettings, error)
	UpsertByVideoID(ctx context.Context, videoID string, settings *domain.PlayerSettings) (*domain.PlayerSettings, error)
	GetByChannelHandle(ctx context.Context, handle string) (*domain.PlayerSettings, error)
	UpsertByChannelHandle(ctx context.Context, handle string, settings *domain.PlayerSettings) (*domain.PlayerSettings, error)
}

// SettingsHandlers handles player settings endpoints.
type SettingsHandlers struct {
	repo PlayerSettingsRepository
}

// NewSettingsHandlers returns a new SettingsHandlers.
func NewSettingsHandlers(repo PlayerSettingsRepository) *SettingsHandlers {
	return &SettingsHandlers{repo: repo}
}

// GetVideoSettings handles GET /api/v1/player-settings/videos/{videoId}.
func (h *SettingsHandlers) GetVideoSettings(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoId")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Video ID is required"))
		return
	}

	settings, err := h.repo.GetByVideoID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, settings)
}

// UpdateVideoSettings handles PUT /api/v1/player-settings/videos/{videoId}.
func (h *SettingsHandlers) UpdateVideoSettings(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoId")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Video ID is required"))
		return
	}

	var settings domain.PlayerSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	updated, err := h.repo.UpsertByVideoID(r.Context(), videoID, &settings)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update settings"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, updated)
}

// GetChannelSettings handles GET /api/v1/player-settings/video-channels/{handle}.
func (h *SettingsHandlers) GetChannelSettings(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	if handle == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Channel handle is required"))
		return
	}

	settings, err := h.repo.GetByChannelHandle(r.Context(), handle)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, settings)
}

// UpdateChannelSettings handles PUT /api/v1/player-settings/video-channels/{handle}.
func (h *SettingsHandlers) UpdateChannelSettings(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	if handle == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Channel handle is required"))
		return
	}

	var settings domain.PlayerSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	updated, err := h.repo.UpsertByChannelHandle(r.Context(), handle, &settings)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update settings"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, updated)
}
