package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// AuditLog represents an audit log entry.
type AuditLog struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	UserID    string    `json:"userId"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"createdAt"`
}

// LogRepository defines the storage interface for server logs.
type LogRepository interface {
	GetRecentLogs(ctx context.Context, limit int) ([]map[string]interface{}, error)
	GetAuditLogs(ctx context.Context, limit, offset int) ([]*AuditLog, int64, error)
	CreateClientLog(ctx context.Context, level, message, userAgent, meta string) error
}

// LogHandlers handles server log endpoints.
type LogHandlers struct {
	repo LogRepository
}

// NewLogHandlers returns a new LogHandlers.
func NewLogHandlers(repo LogRepository) *LogHandlers {
	return &LogHandlers{repo: repo}
}

// GetServerLogs handles GET /api/v1/server/logs.
func (h *LogHandlers) GetServerLogs(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "admin" {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
		return
	}

	logs, err := h.repo.GetRecentLogs(r.Context(), 100)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to retrieve logs"))
		return
	}

	if logs == nil {
		logs = []map[string]interface{}{}
	}

	shared.WriteJSON(w, http.StatusOK, logs)
}

// GetAuditLogs handles GET /api/v1/server/audit-logs.
func (h *LogHandlers) GetAuditLogs(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.UserRoleKey).(string)
	if role != "admin" {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Admin access required"))
		return
	}

	logs, total, err := h.repo.GetAuditLogs(r.Context(), 50, 0)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to retrieve audit logs"))
		return
	}

	if logs == nil {
		logs = []*AuditLog{}
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, logs, &shared.Meta{Total: total, Limit: 50, Offset: 0})
}

type createClientLogRequest struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Meta    string `json:"meta"`
}

// CreateClientLog handles POST /api/v1/server/logs/client.
func (h *LogHandlers) CreateClientLog(w http.ResponseWriter, r *http.Request) {
	var req createClientLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if req.Message == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Message is required"))
		return
	}

	// Default log level
	if req.Level == "" {
		req.Level = "info"
	}

	// Validate log level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[req.Level] {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid log level"))
		return
	}

	userAgent := r.Header.Get("User-Agent")
	if err := h.repo.CreateClientLog(r.Context(), req.Level, req.Message, userAgent, req.Meta); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create log entry"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
