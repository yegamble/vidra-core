package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// ChannelSyncRepository defines the storage interface for channel syncs.
type ChannelSyncRepository interface {
	CreateSync(ctx context.Context, sync *domain.ChannelSync) (*domain.ChannelSync, error)
	DeleteSync(ctx context.Context, id int64) error
	GetSync(ctx context.Context, id int64) (*domain.ChannelSync, error)
	TriggerSync(ctx context.Context, id int64) error
}

// SyncHandlers handles video channel sync endpoints.
type SyncHandlers struct {
	repo ChannelSyncRepository
}

// NewSyncHandlers returns a new SyncHandlers.
func NewSyncHandlers(repo ChannelSyncRepository) *SyncHandlers {
	return &SyncHandlers{repo: repo}
}

type createSyncRequest struct {
	ExternalChannelURL string `json:"externalChannelUrl"`
	ChannelID          string `json:"videoChannelId"`
}

// CreateSync handles POST /api/v1/video-channel-syncs.
func (h *SyncHandlers) CreateSync(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	var req createSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if req.ExternalChannelURL == "" || req.ChannelID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "externalChannelUrl and videoChannelId are required"))
		return
	}

	sync := &domain.ChannelSync{
		ChannelID:          req.ChannelID,
		ExternalChannelURL: req.ExternalChannelURL,
		State:              int(domain.ChannelSyncStateWaitingFirstRun),
	}

	created, err := h.repo.CreateSync(r.Context(), sync)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create sync"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, created)
}

// DeleteSync handles DELETE /api/v1/video-channel-syncs/{id}.
func (h *SyncHandlers) DeleteSync(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid sync ID"))
		return
	}

	if err := h.repo.DeleteSync(r.Context(), id); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TriggerSync handles POST /api/v1/video-channel-syncs/{id}/trigger-now.
func (h *SyncHandlers) TriggerSync(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid sync ID"))
		return
	}

	if err := h.repo.TriggerSync(r.Context(), id); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
