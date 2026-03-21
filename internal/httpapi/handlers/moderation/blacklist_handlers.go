package moderation

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
)

// BlacklistRepository defines the data operations for video blacklisting.
type BlacklistRepository interface {
	AddToBlacklist(ctx context.Context, entry *domain.VideoBlacklist) error
	RemoveFromBlacklist(ctx context.Context, videoID uuid.UUID) error
	GetByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.VideoBlacklist, error)
	List(ctx context.Context, limit, offset int) ([]*domain.VideoBlacklist, int, error)
}

// BlacklistHandlers handles video blacklist operations.
type BlacklistHandlers struct {
	repo BlacklistRepository
}

// NewBlacklistHandlers creates a new BlacklistHandlers.
func NewBlacklistHandlers(repo BlacklistRepository) *BlacklistHandlers {
	return &BlacklistHandlers{repo: repo}
}

type addBlacklistRequest struct {
	Reason      string `json:"reason"`
	Unfederated bool   `json:"unfederated"`
}

// AddToBlacklist handles POST /api/v1/videos/{id}/blacklist.
func (h *BlacklistHandlers) AddToBlacklist(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid video ID"))
		return
	}

	// Check if already blacklisted
	existing, err := h.repo.GetByVideoID(r.Context(), videoID)
	if err != nil && err != domain.ErrNotFound {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("BLACKLIST_ERROR", "Failed to check blacklist"))
		return
	}
	if existing != nil {
		shared.WriteError(w, http.StatusConflict, domain.NewDomainError("ALREADY_BLACKLISTED", "Video is already blacklisted"))
		return
	}

	var req addBlacklistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_BODY", "Invalid request body"))
		return
	}

	entry := &domain.VideoBlacklist{
		ID:          uuid.New(),
		VideoID:     videoID,
		Reason:      req.Reason,
		Unfederated: req.Unfederated,
		CreatedAt:   time.Now(),
	}

	if err := h.repo.AddToBlacklist(r.Context(), entry); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("BLACKLIST_FAILED", "Failed to blacklist video"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RemoveFromBlacklist handles DELETE /api/v1/videos/{id}/blacklist.
func (h *BlacklistHandlers) RemoveFromBlacklist(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid video ID"))
		return
	}

	if err := h.repo.RemoveFromBlacklist(r.Context(), videoID); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not in blacklist"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("BLACKLIST_FAILED", "Failed to remove from blacklist"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListBlacklist handles GET /api/v1/videos/blacklist.
func (h *BlacklistHandlers) ListBlacklist(w http.ResponseWriter, r *http.Request) {
	_, limit, offset, pageSize := shared.ParsePagination(r, 20)
	page := offset/limit + 1
	if limit == 0 {
		page = 1
	}

	entries, total, err := h.repo.List(r.Context(), limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list blacklisted videos"))
		return
	}

	meta := &shared.Meta{
		Total:    int64(total),
		Limit:    limit,
		Offset:   offset,
		Page:     page,
		PageSize: pageSize,
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, entries, meta)
}
