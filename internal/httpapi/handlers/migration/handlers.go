package migration

import (
	"encoding/json"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase/migration_etl"
)

// MigrationHandlers handles PeerTube instance migration HTTP endpoints
type MigrationHandlers struct {
	service *migration_etl.ETLService
}

// NewMigrationHandlers creates a new MigrationHandlers
func NewMigrationHandlers(service *migration_etl.ETLService) *MigrationHandlers {
	return &MigrationHandlers{service: service}
}

// StartMigration handles POST /api/v1/admin/migrations/peertube
func (h *MigrationHandlers) StartMigration(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	var req domain.MigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "invalid request body"))
		return
	}

	job, err := h.service.StartMigration(r.Context(), userID.String(), &req)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, job)
}

// ListMigrations handles GET /api/v1/admin/migrations
func (h *MigrationHandlers) ListMigrations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("count"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("start"))

	if limit <= 0 {
		limit = 20
	}

	jobs, total, err := h.service.ListMigrations(r.Context(), limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "failed to list migrations"))
		return
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, jobs, &shared.Meta{
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// GetMigration handles GET /api/v1/admin/migrations/{id}
func (h *MigrationHandlers) GetMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	job, err := h.service.GetMigrationStatus(r.Context(), id)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

// CancelMigration handles DELETE /api/v1/admin/migrations/{id}
func (h *MigrationHandlers) CancelMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.CancelMigration(r.Context(), id); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ResumeMigration handles POST /api/v1/admin/migrations/{id}/resume
func (h *MigrationHandlers) ResumeMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	job, err := h.service.ResumeMigration(r.Context(), id)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

// DryRun handles POST /api/v1/admin/migrations/{id}/dry-run
func (h *MigrationHandlers) DryRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return
	}

	var req domain.MigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "invalid request body"))
		return
	}

	job, err := h.service.DryRun(r.Context(), userID.String(), &req)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, job)
}
