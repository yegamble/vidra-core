package federation

import (
	"vidra-core/internal/httpapi/shared"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
)

type AdminFederationHandlers struct {
	repo FederationRepositoryInterface
}

func NewAdminFederationHandlers(repo FederationRepositoryInterface) *AdminFederationHandlers {
	return &AdminFederationHandlers{repo: repo}
}

func (h *AdminFederationHandlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	limit := pageSize
	offset := (page - 1) * pageSize
	jobs, total, err := h.repo.ListJobs(r.Context(), status, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list jobs"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"total": total, "page": page, "pageSize": pageSize, "data": jobs})
}

func (h *AdminFederationHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	job, err := h.repo.GetJob(r.Context(), id)
	if err != nil {
		if de, ok := err.(domain.DomainError); ok && de.Code == "NOT_FOUND" {
			shared.WriteError(w, http.StatusNotFound, de)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get job"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, job)
}

func (h *AdminFederationHandlers) RetryJob(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		DelaySeconds int `json:"delaySeconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.DelaySeconds <= 0 {
		req.DelaySeconds = 30
	}
	when := time.Now().Add(time.Duration(req.DelaySeconds) * time.Second)
	if err := h.repo.RetryJob(r.Context(), id, when); err != nil {
		if de, ok := err.(domain.DomainError); ok && de.Code == "NOT_FOUND" {
			shared.WriteError(w, http.StatusNotFound, de)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to retry job"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *AdminFederationHandlers) DeleteJob(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.repo.DeleteJob(r.Context(), id); err != nil {
		if de, ok := err.(domain.DomainError); ok && de.Code == "NOT_FOUND" {
			shared.WriteError(w, http.StatusNotFound, de)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to delete job"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if role, ok := r.Context().Value(middleware.UserRoleKey).(string); ok {
		if role == string(domain.RoleAdmin) {
			return true
		}
	}
	shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
	return false
}
