package channel

import (
	"context"
	"io"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var allowedImageMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// validateImageMIME reads the first 512 bytes of r and checks the detected
// content type against the allowed image MIME types. Returns "" on success or
// the detected type string if disallowed.
func validateImageMIME(r io.Reader) (string, bool) {
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	detected := http.DetectContentType(buf[:n])
	return detected, allowedImageMIMETypes[detected]
}

// ChannelMediaRepository defines data access needed for channel media operations.
type ChannelMediaRepository interface {
	GetOwnerID(ctx context.Context, channelID uuid.UUID) (string, error)
	SetAvatar(ctx context.Context, channelID uuid.UUID, filename, ipfsCID string) error
	ClearAvatar(ctx context.Context, channelID uuid.UUID) error
	SetBanner(ctx context.Context, channelID uuid.UUID, filename, ipfsCID string) error
	ClearBanner(ctx context.Context, channelID uuid.UUID) error
}

// ChannelMediaHandlers handles channel avatar/banner HTTP requests.
type ChannelMediaHandlers struct {
	repo ChannelMediaRepository
}

// NewChannelMediaHandlers creates handlers for channel media endpoints.
func NewChannelMediaHandlers(repo ChannelMediaRepository) *ChannelMediaHandlers {
	return &ChannelMediaHandlers{repo: repo}
}

// UploadAvatar handles POST /api/v1/channels/{id}/avatar.
func (h *ChannelMediaHandlers) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Failed to parse upload"))
		return
	}

	file, header, err := r.FormFile("avatarfile")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing avatarfile field"))
		return
	}
	defer file.Close()

	if detected, ok := validateImageMIME(file); !ok {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FILE_TYPE", "File type not allowed: "+detected))
		return
	}

	if err := h.repo.SetAvatar(r.Context(), channelID, header.Filename, ""); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to set avatar"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"avatars": []map[string]interface{}{
			{"path": "/lazy-static/avatars/" + header.Filename},
		},
	})
}

// UploadBanner handles POST /api/v1/channels/{id}/banner.
func (h *ChannelMediaHandlers) UploadBanner(w http.ResponseWriter, r *http.Request) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Failed to parse upload"))
		return
	}

	file, header, err := r.FormFile("bannerfile")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing bannerfile field"))
		return
	}
	defer file.Close()

	if detected, ok := validateImageMIME(file); !ok {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FILE_TYPE", "File type not allowed: "+detected))
		return
	}

	if err := h.repo.SetBanner(r.Context(), channelID, header.Filename, ""); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to set banner"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"banners": []map[string]interface{}{
			{"path": "/lazy-static/banners/" + header.Filename},
		},
	})
}

// DeleteAvatar handles DELETE /api/v1/channels/{id}/avatar.
func (h *ChannelMediaHandlers) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := h.repo.ClearAvatar(r.Context(), channelID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to clear avatar"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteBanner handles DELETE /api/v1/channels/{id}/banner.
func (h *ChannelMediaHandlers) DeleteBanner(w http.ResponseWriter, r *http.Request) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := h.repo.ClearBanner(r.Context(), channelID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to clear banner"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// extractIDs parses channelID from the URL and userID from context.
// Writes an error response and returns false on failure.
func (h *ChannelMediaHandlers) extractIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	idStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid channel ID"))
		return uuid.Nil, "", false
	}

	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return uuid.Nil, "", false
	}

	return channelID, userID, true
}
