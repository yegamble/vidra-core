package channel

import (
	"context"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

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
