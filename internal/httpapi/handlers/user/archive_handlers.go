package user

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// ArchiveRepository defines the storage interface for user export/import.
type ArchiveRepository interface {
	CreateExport(ctx context.Context, userID string) (*domain.UserExport, error)
	ListExports(ctx context.Context, userID string) ([]*domain.UserExport, error)
	DeleteExport(ctx context.Context, id int64, userID string) error
	CreateImport(ctx context.Context, userID string) (*domain.UserImport, error)
	GetLatestImport(ctx context.Context, userID string) (*domain.UserImport, error)
	DeleteImport(ctx context.Context, userID string) error
}

// ArchiveHandlers handles user data import/export endpoints.
type ArchiveHandlers struct {
	repo ArchiveRepository
}

// NewArchiveHandlers returns a new ArchiveHandlers.
func NewArchiveHandlers(repo ArchiveRepository) *ArchiveHandlers {
	return &ArchiveHandlers{repo: repo}
}

// RequestExport handles POST /api/v1/users/{userId}/exports/request.
func (h *ArchiveHandlers) RequestExport(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot request export for another user"))
		return
	}

	export, err := h.repo.CreateExport(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create export"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, export)
}

// ListExports handles GET /api/v1/users/{userId}/exports.
func (h *ArchiveHandlers) ListExports(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot list exports for another user"))
		return
	}

	exports, err := h.repo.ListExports(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list exports"))
		return
	}
	if exports == nil {
		exports = []*domain.UserExport{}
	}

	shared.WriteJSON(w, http.StatusOK, exports)
}

// DeleteExport handles DELETE /api/v1/users/{userId}/exports/{id}.
func (h *ArchiveHandlers) DeleteExport(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot delete export for another user"))
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid export ID"))
		return
	}

	if err := h.repo.DeleteExport(r.Context(), id, userID); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// InitImportResumable handles POST /api/v1/users/{userId}/imports/import-resumable.
func (h *ArchiveHandlers) InitImportResumable(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot import for another user"))
		return
	}

	imp, err := h.repo.CreateImport(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create import"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, imp)
}

// UploadImportChunk handles PUT /api/v1/users/{userId}/imports/import-resumable.
func (h *ArchiveHandlers) UploadImportChunk(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot import for another user"))
		return
	}

	// Parse multipart form with 32MB limit
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid multipart form"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"uploaded": true,
	})
}

// CancelImportResumable handles DELETE /api/v1/users/{userId}/imports/import-resumable.
func (h *ArchiveHandlers) CancelImportResumable(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot cancel import for another user"))
		return
	}

	if err := h.repo.DeleteImport(r.Context(), userID); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetLatestImport handles GET /api/v1/users/{userId}/imports/latest.
func (h *ArchiveHandlers) GetLatestImport(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || callerID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}
	if callerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot view import for another user"))
		return
	}

	imp, err := h.repo.GetLatestImport(r.Context(), userID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, imp)
}

// updateImportStateRequest is the body for updating import state (unused externally but kept for JSON decode).
type updateImportStateRequest struct {
	State int `json:"state"`
}

// Ensure updateImportStateRequest is decodable.
var _ json.Unmarshaler = (*updateImportStateRequest)(nil)

func (u *updateImportStateRequest) UnmarshalJSON(data []byte) error {
	type alias updateImportStateRequest
	return json.Unmarshal(data, (*alias)(u))
}
