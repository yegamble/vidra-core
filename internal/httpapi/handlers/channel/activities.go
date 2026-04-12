package channel

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// ActivityRepository defines operations needed by activity handlers.
type ActivityRepository interface {
	ListByChannel(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]domain.ChannelActivity, int64, error)
}

// ChannelForActivity is a minimal interface to resolve channel by handle.
type ChannelForActivity interface {
	GetChannelByHandle(ctx context.Context, handle string) (*domain.Channel, error)
}

// ActivityHandlers handles channel activity endpoints.
type ActivityHandlers struct {
	activityRepo ActivityRepository
	channelSvc   ChannelForActivity
}

// NewActivityHandlers creates a new ActivityHandlers.
func NewActivityHandlers(activityRepo ActivityRepository, channelSvc ChannelForActivity) *ActivityHandlers {
	return &ActivityHandlers{activityRepo: activityRepo, channelSvc: channelSvc}
}

// ListActivities handles GET /api/v1/video-channels/{channelHandle}/activities
func (h *ActivityHandlers) ListActivities(w http.ResponseWriter, r *http.Request) {
	// Require authentication
	_, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	handle := chi.URLParam(r, "channelHandle")
	if handle == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_HANDLE", "Channel handle is required"))
		return
	}

	ch, err := h.channelSvc.GetChannelByHandle(r.Context(), handle)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "Channel not found"))
		return
	}

	page, limit, offset, pageSize := shared.ParsePagination(r, 20)

	activities, total, err := h.activityRepo.ListByChannel(r.Context(), ch.ID, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list activities"))
		return
	}

	meta := &shared.Meta{Total: total, Limit: limit, Offset: offset, Page: page, PageSize: pageSize}
	shared.WriteJSONWithMeta(w, http.StatusOK, activities, meta)
}
