package video

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// EmbedPrivacyVideoRepository is the subset of video repo needed for ownership checks.
type EmbedPrivacyVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// EmbedPrivacyRepository defines data operations for video embed privacy.
type EmbedPrivacyRepository interface {
	Get(ctx context.Context, videoID string) (*domain.VideoEmbedPrivacy, error)
	Upsert(ctx context.Context, privacy *domain.VideoEmbedPrivacy) error
	IsDomainAllowed(ctx context.Context, videoID string, domainName string) (bool, error)
}

// EmbedPrivacyHandlers handles video embed privacy endpoints.
type EmbedPrivacyHandlers struct {
	embedRepo EmbedPrivacyRepository
	videoRepo EmbedPrivacyVideoRepository
}

// NewEmbedPrivacyHandlers creates a new EmbedPrivacyHandlers.
func NewEmbedPrivacyHandlers(embedRepo EmbedPrivacyRepository, videoRepo EmbedPrivacyVideoRepository) *EmbedPrivacyHandlers {
	return &EmbedPrivacyHandlers{embedRepo: embedRepo, videoRepo: videoRepo}
}

// GetEmbedPrivacy handles GET /api/v1/videos/{id}/embed-privacy.
func (h *EmbedPrivacyHandlers) GetEmbedPrivacy(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	privacy, err := h.embedRepo.Get(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get embed privacy"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, privacy)
}

// UpdateEmbedPrivacy handles PUT /api/v1/videos/{id}/embed-privacy.
func (h *EmbedPrivacyHandlers) UpdateEmbedPrivacy(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can update embed privacy"))
		return
	}

	var req struct {
		Status         int      `json:"status"`
		AllowedDomains []string `json:"allowedDomains"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if req.Status < domain.EmbedEnabled || req.Status > domain.EmbedWhitelist {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STATUS", "Status must be 1 (enabled), 2 (disabled), or 3 (whitelist)"))
		return
	}

	privacy := &domain.VideoEmbedPrivacy{
		VideoID:        videoID,
		Status:         req.Status,
		AllowedDomains: req.AllowedDomains,
	}
	if privacy.AllowedDomains == nil {
		privacy.AllowedDomains = []string{}
	}

	if err := h.embedRepo.Upsert(r.Context(), privacy); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update embed privacy"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, privacy)
}

// CheckDomainAllowed handles GET /api/v1/videos/{id}/embed-privacy/allowed.
func (h *EmbedPrivacyHandlers) CheckDomainAllowed(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	domainName := r.URL.Query().Get("domain")
	if domainName == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_DOMAIN", "Domain query parameter is required"))
		return
	}

	allowed, err := h.embedRepo.IsDomainAllowed(r.Context(), videoID, domainName)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to check domain"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]bool{"allowed": allowed})
}
