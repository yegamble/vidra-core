package admin

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// InstanceMediaHandlers provides instance avatar/banner upload endpoints.
type InstanceMediaHandlers struct {
	repo configResetRepo
}

// NewInstanceMediaHandlers creates a new InstanceMediaHandlers.
func NewInstanceMediaHandlers(repo configResetRepo) *InstanceMediaHandlers {
	return &InstanceMediaHandlers{repo: repo}
}

// UploadInstanceAvatar handles POST /api/v1/config/instance-avatar/pick.
func (h *InstanceMediaHandlers) UploadInstanceAvatar(w http.ResponseWriter, r *http.Request) {
	h.uploadMedia(w, r, "avatarfile", "instance_avatar_path", "avatars")
}

// DeleteInstanceAvatar handles DELETE /api/v1/config/instance-avatar/pick.
func (h *InstanceMediaHandlers) DeleteInstanceAvatar(w http.ResponseWriter, r *http.Request) {
	h.deleteMedia(w, r, "instance_avatar_path")
}

// UploadInstanceBanner handles POST /api/v1/config/instance-banner/pick.
func (h *InstanceMediaHandlers) UploadInstanceBanner(w http.ResponseWriter, r *http.Request) {
	h.uploadMedia(w, r, "bannerfile", "instance_banner_path", "banners")
}

// DeleteInstanceBanner handles DELETE /api/v1/config/instance-banner/pick.
func (h *InstanceMediaHandlers) DeleteInstanceBanner(w http.ResponseWriter, r *http.Request) {
	h.deleteMedia(w, r, "instance_banner_path")
}

func (h *InstanceMediaHandlers) uploadMedia(w http.ResponseWriter, r *http.Request, fieldName, configKey, pathPrefix string) {
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid multipart form"))
		return
	}

	_, header, err := r.FormFile(fieldName)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("missing file field %q", fieldName))
		return
	}

	safeFilename := filepath.Base(header.Filename)
	if safeFilename == "." || safeFilename == "" || strings.ContainsAny(safeFilename, `/\`) {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FILENAME", "Invalid filename"))
		return
	}

	path := fmt.Sprintf("/lazy-static/%s/%s", pathPrefix, safeFilename)
	if err := h.repo.SetConfigValue(r.Context(), configKey, path); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to store %s: %w", configKey, err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (h *InstanceMediaHandlers) deleteMedia(w http.ResponseWriter, r *http.Request, configKey string) {
	if err := h.repo.SetConfigValue(r.Context(), configKey, ""); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to clear %s: %w", configKey, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
