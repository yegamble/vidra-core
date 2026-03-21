package livestream

import (
	"context"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// LiveStreamSessionRepository defines data access for live stream session history.
type LiveStreamSessionRepository interface {
	ListSessions(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.LiveStreamSession, error)
}

// SessionHistoryHandlers handles GET /streams/{id}/sessions.
type SessionHistoryHandlers struct {
	repo LiveStreamSessionRepository
}

// NewSessionHistoryHandlers creates handlers for session history.
func NewSessionHistoryHandlers(repo LiveStreamSessionRepository) *SessionHistoryHandlers {
	return &SessionHistoryHandlers{repo: repo}
}

// GetSessionHistory handles GET /api/v1/streams/{id}/sessions
func (h *SessionHistoryHandlers) GetSessionHistory(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	streamID, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid stream ID"))
		return
	}

	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	sessions, err := h.repo.ListSessions(r.Context(), streamID, limit, offset)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, sessions)
}
