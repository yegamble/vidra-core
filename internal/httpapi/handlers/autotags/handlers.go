package autotags

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	ucat "athena/internal/usecase/auto_tags"
)

// Handlers handles HTTP requests for automatic tag policies.
type Handlers struct {
	service *ucat.Service
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(service *ucat.Service) *Handlers {
	return &Handlers{service: service}
}

// GetAccountAutoTagPolicies handles GET /api/v1/auto-tags/accounts/{accountName}/policies
func (h *Handlers) GetAccountAutoTagPolicies(w http.ResponseWriter, r *http.Request) {
	accountName := chi.URLParam(r, "accountName")
	if accountName == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("account name required"))
		return
	}

	policies, err := h.service.GetPolicies(r.Context(), &accountName)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get auto-tag policies"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, policies)
}

// UpdateAccountAutoTagPolicies handles PUT /api/v1/auto-tags/accounts/{accountName}/policies
func (h *Handlers) UpdateAccountAutoTagPolicies(w http.ResponseWriter, r *http.Request) {
	accountName := chi.URLParam(r, "accountName")
	if accountName == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("account name required"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.UpdateAutoTagPoliciesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	policies, err := h.service.UpdatePolicies(r.Context(), &accountName, &req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update auto-tag policies"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, policies)
}

// GetAccountAvailableTags handles GET /api/v1/auto-tags/accounts/{accountName}/available
func (h *Handlers) GetAccountAvailableTags(w http.ResponseWriter, r *http.Request) {
	accountName := chi.URLParam(r, "accountName")
	if accountName == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("account name required"))
		return
	}

	tags, err := h.service.GetAvailableTags(r.Context(), &accountName)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get available tags"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, tags)
}

// GetServerAvailableTags handles GET /api/v1/auto-tags/server/available
func (h *Handlers) GetServerAvailableTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.service.GetAvailableTags(r.Context(), nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get server available tags"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, tags)
}

// GetServerAutoTagPolicies handles GET /api/v1/auto-tags/server/policies
func (h *Handlers) GetServerAutoTagPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.service.GetPolicies(r.Context(), nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get server auto-tag policies"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, policies)
}

// UpdateServerAutoTagPolicies handles PUT /api/v1/auto-tags/server/policies
func (h *Handlers) UpdateServerAutoTagPolicies(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.UpdateAutoTagPoliciesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	policies, err := h.service.UpdatePolicies(r.Context(), nil, &req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update server auto-tag policies"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, policies)
}
