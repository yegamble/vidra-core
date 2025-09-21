package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"github.com/go-chi/chi/v5"
)

// ModerationHandlers handles moderation-related HTTP requests
type ModerationHandlers struct {
	repo *repository.ModerationRepository
}

// NewModerationHandlers creates a new instance of ModerationHandlers
func NewModerationHandlers(repo *repository.ModerationRepository) *ModerationHandlers {
	return &ModerationHandlers{repo: repo}
}

// CreateAbuseReport handles POST /api/v1/abuse-reports
func (h *ModerationHandlers) CreateAbuseReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	var req domain.CreateAbuseReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	// Create the report
	report := &domain.AbuseReport{
		ReporterID: userID.String(),
		Reason:     req.Reason,
		Details:    sql.NullString{String: req.Details, Valid: req.Details != ""},
		Status:     domain.AbuseReportStatusPending,
		EntityType: req.EntityType,
	}

	// Set the appropriate entity ID
	switch req.EntityType {
	case domain.ReportedEntityVideo:
		report.VideoID = sql.NullString{String: req.EntityID, Valid: true}
	case domain.ReportedEntityComment:
		report.CommentID = sql.NullString{String: req.EntityID, Valid: true}
	case domain.ReportedEntityUser:
		report.UserID = sql.NullString{String: req.EntityID, Valid: true}
	case domain.ReportedEntityChannel:
		report.ChannelID = sql.NullString{String: req.EntityID, Valid: true}
	}

	if err := h.repo.CreateAbuseReport(r.Context(), report); err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create abuse report"))
		return
	}

	WriteJSON(w, http.StatusCreated, report)
}

// ListAbuseReports handles GET /api/v1/admin/abuse-reports (admin only)
func (h *ModerationHandlers) ListAbuseReports(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	status := r.URL.Query().Get("status")
	limit := 20
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	reports, total, err := h.repo.ListAbuseReports(r.Context(), status, limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list abuse reports"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data":    reports,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
		"success": true,
	})
}

// GetAbuseReport handles GET /api/v1/admin/abuse-reports/{id} (admin only)
func (h *ModerationHandlers) GetAbuseReport(w http.ResponseWriter, r *http.Request) {
	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing report ID"))
		return
	}

	report, err := h.repo.GetAbuseReport(r.Context(), reportID)
	if err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, err)
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get abuse report"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, report)
}

// UpdateAbuseReport handles PUT /api/v1/admin/abuse-reports/{id} (admin only)
func (h *ModerationHandlers) UpdateAbuseReport(w http.ResponseWriter, r *http.Request) {
	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing report ID"))
		return
	}

	moderatorID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	var req domain.UpdateAbuseReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if err := h.repo.UpdateAbuseReport(r.Context(), reportID, moderatorID.String(), req.Status, req.ModeratorNotes); err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, err)
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update abuse report"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Abuse report updated successfully",
		"success": true,
	})
}

// DeleteAbuseReport handles DELETE /api/v1/admin/abuse-reports/{id} (admin only)
func (h *ModerationHandlers) DeleteAbuseReport(w http.ResponseWriter, r *http.Request) {
	reportID := chi.URLParam(r, "id")
	if reportID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing report ID"))
		return
	}

	if err := h.repo.DeleteAbuseReport(r.Context(), reportID); err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, err)
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to delete abuse report"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Abuse report deleted successfully",
		"success": true,
	})
}

// CreateBlocklistEntry handles POST /api/v1/admin/blocklist (admin only)
func (h *ModerationHandlers) CreateBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	blockedBy, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	var req domain.CreateBlocklistEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	entry := &domain.BlocklistEntry{
		BlockType:    req.BlockType,
		BlockedValue: req.BlockedValue,
		Reason:       sql.NullString{String: req.Reason, Valid: req.Reason != ""},
		BlockedBy:    blockedBy.String(),
		IsActive:     true,
	}

	if req.ExpiresAt != nil {
		entry.ExpiresAt = sql.NullTime{Time: *req.ExpiresAt, Valid: true}
	}

	if err := h.repo.CreateBlocklistEntry(r.Context(), entry); err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create blocklist entry"))
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"data":    entry,
		"success": true,
	})
}

// ListBlocklistEntries handles GET /api/v1/admin/blocklist (admin only)
func (h *ModerationHandlers) ListBlocklistEntries(w http.ResponseWriter, r *http.Request) {
	blockType := r.URL.Query().Get("type")
	activeOnly := r.URL.Query().Get("active_only") == "true"
	limit := 20
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	entries, total, err := h.repo.ListBlocklistEntries(r.Context(), blockType, activeOnly, limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list blocklist entries"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data":    entries,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
		"success": true,
	})
}

// UpdateBlocklistEntry handles PUT /api/v1/admin/blocklist/{id} (admin only)
func (h *ModerationHandlers) UpdateBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	entryID := chi.URLParam(r, "id")
	if entryID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing entry ID"))
		return
	}

	var req struct {
		IsActive  bool       `json:"is_active"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	var expiresAt sql.NullTime
	if req.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *req.ExpiresAt, Valid: true}
	}

	if err := h.repo.UpdateBlocklistEntry(r.Context(), entryID, req.IsActive, expiresAt); err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, err)
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update blocklist entry"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Blocklist entry updated successfully",
		"success": true,
	})
}

// DeleteBlocklistEntry handles DELETE /api/v1/admin/blocklist/{id} (admin only)
func (h *ModerationHandlers) DeleteBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	entryID := chi.URLParam(r, "id")
	if entryID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing entry ID"))
		return
	}

	if err := h.repo.DeleteBlocklistEntry(r.Context(), entryID); err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, err)
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to delete blocklist entry"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Blocklist entry deleted successfully",
		"success": true,
	})
}
