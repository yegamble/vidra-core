package httpapi

import (
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/repository"
)

type FederationHandlers struct {
	repo *repository.FederationRepository
}

func NewFederationHandlers(repo *repository.FederationRepository) *FederationHandlers {
	return &FederationHandlers{repo: repo}
}

// GetTimeline handles GET /api/v1/federation/timeline
func (h *FederationHandlers) GetTimeline(w http.ResponseWriter, r *http.Request) {
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
	posts, total, err := h.repo.ListTimeline(r.Context(), limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load timeline"))
		return
	}
	resp := domain.FederatedTimeline{Total: total, Page: page, PageSize: pageSize, Data: posts}
	WriteJSON(w, http.StatusOK, resp)
}
