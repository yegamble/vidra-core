package auth

import (
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
)

// TokenSessionHandlers handles token session endpoints.
type TokenSessionHandlers struct {
	repo port.TokenSessionRepository
}

// NewTokenSessionHandlers returns a new TokenSessionHandlers.
func NewTokenSessionHandlers(repo port.TokenSessionRepository) *TokenSessionHandlers {
	return &TokenSessionHandlers{repo: repo}
}

// ListTokenSessions handles GET /users/{id}/token-sessions
func (h *TokenSessionHandlers) ListTokenSessions(w http.ResponseWriter, r *http.Request) {
	callerID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	userID := chi.URLParam(r, "id")
	callerRole, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if callerID != userID && callerRole != string(domain.RoleAdmin) {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
		return
	}

	sessions, err := h.repo.ListUserTokenSessions(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list token sessions"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, sessions)
}

// RevokeTokenSession handles POST /users/{id}/token-sessions/{tokenSessionId}/revoke
func (h *TokenSessionHandlers) RevokeTokenSession(w http.ResponseWriter, r *http.Request) {
	callerID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	userID := chi.URLParam(r, "id")
	callerRole, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if callerID != userID && callerRole != string(domain.RoleAdmin) {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
		return
	}

	tokenSessionID := chi.URLParam(r, "tokenSessionId")
	if err := h.repo.RevokeTokenSession(r.Context(), tokenSessionID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to revoke token session"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
