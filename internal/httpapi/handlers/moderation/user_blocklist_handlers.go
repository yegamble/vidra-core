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

// UserBlockRepository is the minimal interface needed for per-user blocklist operations.
type UserBlockRepository interface {
	BlockAccount(ctx context.Context, userID, targetAccountID uuid.UUID) (*domain.UserBlock, error)
	UnblockAccount(ctx context.Context, userID uuid.UUID, targetAccountName string) error
	ListAccountBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.UserBlock, int64, error)
	BlockServer(ctx context.Context, userID uuid.UUID, host string) (*domain.UserBlock, error)
	UnblockServer(ctx context.Context, userID uuid.UUID, host string) error
	ListServerBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.UserBlock, int64, error)
}

// UserBlocklistHandlers handles per-user account and server blocklist endpoints.
type UserBlocklistHandlers struct {
	repo UserBlockRepository
}

// NewUserBlocklistHandlers creates a new UserBlocklistHandlers.
func NewUserBlocklistHandlers(repo UserBlockRepository) *UserBlocklistHandlers {
	return &UserBlocklistHandlers{repo: repo}
}

// ListAccountBlocks handles GET /api/v1/blocklist/accounts
func (h *UserBlocklistHandlers) ListAccountBlocks(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserUUID(r)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}
	blocks, total, err := h.repo.ListAccountBlocks(r.Context(), userID, 100, 0)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list account blocks"))
		return
	}
	shared.WriteJSONWithMeta(w, http.StatusOK, blocks, &shared.Meta{Total: total})
}

// BlockAccount handles POST /api/v1/blocklist/accounts
func (h *UserBlocklistHandlers) BlockAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserUUID(r)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	var req struct {
		AccountName string `json:"accountName"`
		AccountID   string `json:"accountId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	// Resolve target account ID — for now accept direct UUID
	targetID, err := uuid.Parse(req.AccountID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "accountId must be a valid UUID"))
		return
	}

	block, err := h.repo.BlockAccount(r.Context(), userID, targetID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to block account"))
		return
	}
	shared.WriteJSON(w, http.StatusCreated, block)
}

// UnblockAccount handles DELETE /api/v1/blocklist/accounts/{accountName}
func (h *UserBlocklistHandlers) UnblockAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserUUID(r)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}
	accountName := chi.URLParam(r, "accountName")
	if accountName == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "accountName is required"))
		return
	}
	if err := h.repo.UnblockAccount(r.Context(), userID, accountName); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Block not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to unblock account"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListServerBlocks handles GET /api/v1/blocklist/servers
func (h *UserBlocklistHandlers) ListServerBlocks(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserUUID(r)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}
	blocks, total, err := h.repo.ListServerBlocks(r.Context(), userID, 100, 0)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list server blocks"))
		return
	}
	shared.WriteJSONWithMeta(w, http.StatusOK, blocks, &shared.Meta{Total: total})
}

// BlockServer handles POST /api/v1/blocklist/servers
func (h *UserBlocklistHandlers) BlockServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserUUID(r)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}
	var req struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Host == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "host is required"))
		return
	}
	block, err := h.repo.BlockServer(r.Context(), userID, req.Host)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to block server"))
		return
	}
	shared.WriteJSON(w, http.StatusCreated, block)
}

// UnblockServer handles DELETE /api/v1/blocklist/servers/{host}
func (h *UserBlocklistHandlers) UnblockServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserUUID(r)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}
	host := chi.URLParam(r, "host")
	if host == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "host is required"))
		return
	}
	if err := h.repo.UnblockServer(r.Context(), userID, host); err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Block not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to unblock server"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// currentUserUUID extracts and parses the authenticated user's UUID from context.
func currentUserUUID(r *http.Request) (uuid.UUID, bool) {
	raw, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || raw == "" {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}
