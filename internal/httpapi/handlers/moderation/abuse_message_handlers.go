package moderation

import (
	"context"
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// AbuseMessageRepository defines data access for abuse report messages.
type AbuseMessageRepository interface {
	GetAbuseReportOwner(ctx context.Context, reportID uuid.UUID) (string, error)
	ListAbuseMessages(ctx context.Context, reportID uuid.UUID) ([]*domain.AbuseMessage, error)
	CreateAbuseMessage(ctx context.Context, msg *domain.AbuseMessage) error
	DeleteAbuseMessage(ctx context.Context, reportID, msgID uuid.UUID) error
}

// AbuseMessageHandlers handles discussion threads on abuse reports.
type AbuseMessageHandlers struct {
	repo AbuseMessageRepository
}

// NewAbuseMessageHandlers creates handlers for abuse report message endpoints.
func NewAbuseMessageHandlers(repo AbuseMessageRepository) *AbuseMessageHandlers {
	return &AbuseMessageHandlers{repo: repo}
}

// ListMessages handles GET /admin/abuse-reports/{id}/messages
func (h *AbuseMessageHandlers) ListMessages(w http.ResponseWriter, r *http.Request) {
	reportID, ok := parseReportID(w, r)
	if !ok {
		return
	}

	msgs, err := h.repo.ListAbuseMessages(r.Context(), reportID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, msgs)
}

// CreateMessage handles POST /admin/abuse-reports/{id}/messages
func (h *AbuseMessageHandlers) CreateMessage(w http.ResponseWriter, r *http.Request) {
	reportID, ok := parseReportID(w, r)
	if !ok {
		return
	}

	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	// Allow: report owner OR admin/mod
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != string(domain.RoleAdmin) && role != string(domain.RoleMod) {
		ownerID, err := h.repo.GetAbuseReportOwner(r.Context(), reportID)
		if err != nil || ownerID != userID {
			shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "message is required"))
		return
	}

	msg := &domain.AbuseMessage{
		AbuseReportID: reportID,
		SenderID:      userID,
		Message:       req.Message,
	}
	if err := h.repo.CreateAbuseMessage(r.Context(), msg); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create message"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, msg)
}

// DeleteMessage handles DELETE /admin/abuse-reports/{id}/messages/{messageId}
func (h *AbuseMessageHandlers) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	reportID, ok := parseReportID(w, r)
	if !ok {
		return
	}

	msgIDStr := chi.URLParam(r, "messageId")
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid message ID"))
		return
	}

	if err := h.repo.DeleteAbuseMessage(r.Context(), reportID, msgID); err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func parseReportID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	idStr := chi.URLParam(r, "id")
	reportID, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid report ID"))
		return uuid.Nil, false
	}
	return reportID, true
}
