package federation

import (
	"athena/internal/httpapi/shared"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"athena/internal/domain"

	"github.com/go-chi/chi/v5"
)

type AdminFederationActorsHandlers struct {
	repo FederationRepositoryInterface
}

func NewAdminFederationActorsHandlers(repo FederationRepositoryInterface) *AdminFederationActorsHandlers {
	return &AdminFederationActorsHandlers{repo: repo}
}

func (h *AdminFederationActorsHandlers) ListActors(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	actors, total, err := h.repo.ListActors(r.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list actors"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"total": total, "page": page, "pageSize": pageSize, "data": actors})
}

func (h *AdminFederationActorsHandlers) UpsertActor(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		Actor            string `json:"actor"`
		Enabled          bool   `json:"enabled"`
		RateLimitSeconds int    `json:"rate_limit_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Actor == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid body"))
		return
	}
	if body.RateLimitSeconds <= 0 {
		body.RateLimitSeconds = 60
	}
	if err := h.repo.UpsertActor(r.Context(), body.Actor, body.Enabled, body.RateLimitSeconds); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to upsert actor"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *AdminFederationActorsHandlers) UpdateActor(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	actor := chi.URLParam(r, "actor")
	var body struct {
		Enabled          *bool   `json:"enabled"`
		RateLimitSeconds *int    `json:"rate_limit_seconds"`
		Cursor           *string `json:"cursor"`
		NextAt           *string `json:"next_at"`
		Attempts         *int    `json:"attempts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid body"))
		return
	}
	var nextAtPtr *time.Time
	if body.NextAt != nil && *body.NextAt != "" {
		if t, err := time.Parse(time.RFC3339, *body.NextAt); err == nil {
			nextAtPtr = &t
		}
	}
	if err := h.repo.UpdateActor(r.Context(), actor, body.Enabled, body.RateLimitSeconds, body.Cursor, nextAtPtr, body.Attempts); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update actor"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *AdminFederationActorsHandlers) DeleteActor(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	actor := chi.URLParam(r, "actor")
	if err := h.repo.DeleteActor(r.Context(), actor); err != nil {
		if de, ok := err.(domain.DomainError); ok && de.Code == "NOT_FOUND" {
			shared.WriteError(w, http.StatusNotFound, de)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to delete actor"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
