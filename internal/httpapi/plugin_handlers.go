package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"athena/internal/domain"
	"athena/internal/plugin"
	"athena/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PluginHandler handles plugin-related HTTP requests
type PluginHandler struct {
	pluginRepo    *repository.PluginRepository
	pluginManager *plugin.Manager
}

// NewPluginHandler creates a new plugin handler
func NewPluginHandler(pluginRepo *repository.PluginRepository, pluginManager *plugin.Manager) *PluginHandler {
	return &PluginHandler{
		pluginRepo:    pluginRepo,
		pluginManager: pluginManager,
	}
}

// ======================================================================
// Plugin Management Endpoints
// ======================================================================

// ListPlugins handles GET /api/v1/admin/plugins
func (h *PluginHandler) ListPlugins(w http.ResponseWriter, r *http.Request) {
	// Optional status filter
	statusParam := r.URL.Query().Get("status")

	var status *domain.PluginStatus
	if statusParam != "" {
		s := domain.PluginStatus(statusParam)
		status = &s
	}

	// Get plugins from database
	plugins, err := h.pluginRepo.List(r.Context(), status)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to list plugins", err)
		return
	}

	// Also get statistics for each plugin
	result := make([]map[string]any, len(plugins))
	for i, p := range plugins {
		stats, _ := h.pluginRepo.GetStatistics(r.Context(), p.ID)

		result[i] = map[string]any{
			"id":           p.ID,
			"name":         p.Name,
			"version":      p.Version,
			"author":       p.Author,
			"description":  p.Description,
			"status":       p.Status,
			"permissions":  p.Permissions,
			"hooks":        p.Hooks,
			"enabled_at":   p.EnabledAt,
			"disabled_at":  p.DisabledAt,
			"installed_at": p.InstalledAt,
			"updated_at":   p.UpdatedAt,
			"last_error":   p.LastError,
		}

		if stats != nil {
			result[i]["statistics"] = map[string]any{
				"total_executions": stats.TotalExecutions,
				"success_count":    stats.SuccessCount,
				"failure_count":    stats.FailureCount,
				"success_rate":     stats.SuccessRate(),
				"avg_duration_ms":  stats.AvgDuration,
				"last_executed_at": stats.LastExecutedAt,
			}
		}
	}

	respondWithJSON(w, http.StatusOK, result)
}

// GetPlugin handles GET /api/v1/admin/plugins/:id
func (h *PluginHandler) GetPlugin(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	plugin, err := h.pluginRepo.GetByID(r.Context(), pluginID)
	if err == domain.ErrPluginNotFound {
		respondWithError(w, http.StatusNotFound, "Plugin not found", nil)
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get plugin", err)
		return
	}

	// Get statistics
	stats, _ := h.pluginRepo.GetStatistics(r.Context(), plugin.ID)

	// Get health
	health, _ := h.pluginRepo.GetPluginHealth(r.Context(), plugin.ID)

	result := map[string]any{
		"id":           plugin.ID,
		"name":         plugin.Name,
		"version":      plugin.Version,
		"author":       plugin.Author,
		"description":  plugin.Description,
		"status":       plugin.Status,
		"config":       plugin.Config,
		"permissions":  plugin.Permissions,
		"hooks":        plugin.Hooks,
		"install_path": plugin.InstallPath,
		"enabled_at":   plugin.EnabledAt,
		"disabled_at":  plugin.DisabledAt,
		"installed_at": plugin.InstalledAt,
		"updated_at":   plugin.UpdatedAt,
		"last_error":   plugin.LastError,
	}

	if stats != nil {
		result["statistics"] = map[string]any{
			"total_executions": stats.TotalExecutions,
			"success_count":    stats.SuccessCount,
			"failure_count":    stats.FailureCount,
			"success_rate":     stats.SuccessRate(),
			"avg_duration_ms":  stats.AvgDuration,
			"last_executed_at": stats.LastExecutedAt,
		}
	}

	if health != nil {
		result["health"] = health
	}

	respondWithJSON(w, http.StatusOK, result)
}

// EnablePlugin handles PUT /api/v1/admin/plugins/:id/enable
func (h *PluginHandler) EnablePlugin(w http.ResponseWriter, r *http.Request) {
	h.togglePluginStatus(w, r, true)
}

// DisablePlugin handles PUT /api/v1/admin/plugins/:id/disable
func (h *PluginHandler) DisablePlugin(w http.ResponseWriter, r *http.Request) {
	h.togglePluginStatus(w, r, false)
}

// togglePluginStatus is a helper function to enable or disable a plugin
func (h *PluginHandler) togglePluginStatus(w http.ResponseWriter, r *http.Request, enable bool) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	// Get plugin from database
	plugin, err := h.pluginRepo.GetByID(r.Context(), pluginID)
	if err == domain.ErrPluginNotFound {
		respondWithError(w, http.StatusNotFound, "Plugin not found", nil)
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get plugin", err)
		return
	}

	// Check current state
	if enable {
		if plugin.IsEnabled() {
			respondWithError(w, http.StatusBadRequest, "Plugin already enabled", nil)
			return
		}
	} else {
		if plugin.IsDisabled() {
			respondWithError(w, http.StatusBadRequest, "Plugin already disabled", nil)
			return
		}
	}

	// Toggle plugin in manager
	var managerErr error
	if enable {
		managerErr = h.pluginManager.EnablePlugin(r.Context(), plugin.Name)
	} else {
		managerErr = h.pluginManager.DisablePlugin(r.Context(), plugin.Name)
	}
	if managerErr != nil {
		action := "enable"
		if !enable {
			action = "disable"
		}
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to %s plugin: %v", action, managerErr), managerErr)
		return
	}

	// Update status in database
	var statusErr error
	if enable {
		statusErr = plugin.Enable()
	} else {
		statusErr = plugin.Disable()
	}
	if statusErr != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update plugin status", statusErr)
		return
	}

	if err := h.pluginRepo.Update(r.Context(), plugin); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to save plugin status", err)
		return
	}

	action := "enabled"
	if !enable {
		action = "disabled"
	}
	respondWithJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Plugin %s %s successfully", plugin.Name, action),
	})
}

// UpdatePluginConfig handles PUT /api/v1/admin/plugins/:id/config
func (h *PluginHandler) UpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	var req struct {
		Config map[string]any `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", nil)
		return
	}

	if req.Config == nil {
		respondWithError(w, http.StatusBadRequest, "Config is required", nil)
		return
	}

	// Get plugin from database
	plugin, err := h.pluginRepo.GetByID(r.Context(), pluginID)
	if err == domain.ErrPluginNotFound {
		respondWithError(w, http.StatusNotFound, "Plugin not found", nil)
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get plugin", err)
		return
	}

	// Update config in manager (will reinitialize if enabled)
	if err := h.pluginManager.UpdatePluginConfig(r.Context(), plugin.Name, req.Config); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update plugin config", err)
		return
	}

	// Update config in database
	if err := plugin.UpdateConfig(req.Config); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update config", err)
		return
	}

	if err := h.pluginRepo.Update(r.Context(), plugin); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to save config", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "Plugin configuration updated successfully",
	})
}

// UninstallPlugin handles DELETE /api/v1/admin/plugins/:id
func (h *PluginHandler) UninstallPlugin(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	// Get plugin from database
	plugin, err := h.pluginRepo.GetByID(r.Context(), pluginID)
	if err == domain.ErrPluginNotFound {
		respondWithError(w, http.StatusNotFound, "Plugin not found", nil)
		return
	}
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get plugin", err)
		return
	}

	// Disable plugin first if enabled
	if plugin.IsEnabled() {
		if err := h.pluginManager.DisablePlugin(r.Context(), plugin.Name); err != nil {
			respondWithError(w, http.StatusInternalServerError, "Failed to disable plugin before uninstall", err)
			return
		}
	}

	// Delete from database
	if err := h.pluginRepo.Delete(r.Context(), pluginID); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to uninstall plugin", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Plugin %s uninstalled successfully", plugin.Name),
	})
}

// ======================================================================
// Plugin Statistics & Monitoring Endpoints
// ======================================================================

// GetPluginStatistics handles GET /api/v1/admin/plugins/:id/statistics
func (h *PluginHandler) GetPluginStatistics(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	stats, err := h.pluginRepo.GetStatistics(r.Context(), pluginID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get statistics", err)
		return
	}

	result := map[string]any{
		"plugin_id":        stats.PluginID,
		"plugin_name":      stats.PluginName,
		"total_executions": stats.TotalExecutions,
		"success_count":    stats.SuccessCount,
		"failure_count":    stats.FailureCount,
		"success_rate":     stats.SuccessRate(),
		"failure_rate":     stats.FailureRate(),
		"avg_duration_ms":  stats.AvgDuration,
		"last_executed_at": stats.LastExecutedAt,
	}

	respondWithJSON(w, http.StatusOK, result)
}

// GetAllStatistics handles GET /api/v1/admin/plugins/statistics
func (h *PluginHandler) GetAllStatistics(w http.ResponseWriter, r *http.Request) {
	statistics, err := h.pluginRepo.GetAllStatistics(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get statistics", err)
		return
	}

	result := make([]map[string]any, len(statistics))
	for i, stats := range statistics {
		result[i] = map[string]any{
			"plugin_id":        stats.PluginID,
			"plugin_name":      stats.PluginName,
			"total_executions": stats.TotalExecutions,
			"success_count":    stats.SuccessCount,
			"failure_count":    stats.FailureCount,
			"success_rate":     stats.SuccessRate(),
			"failure_rate":     stats.FailureRate(),
			"avg_duration_ms":  stats.AvgDuration,
			"last_executed_at": stats.LastExecutedAt,
		}
	}

	respondWithJSON(w, http.StatusOK, result)
}

// GetExecutionHistory handles GET /api/v1/admin/plugins/:id/executions
func (h *PluginHandler) GetExecutionHistory(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	// Default limit
	limit := 100

	executions, err := h.pluginRepo.GetExecutionHistory(r.Context(), pluginID, limit)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get execution history", err)
		return
	}

	respondWithJSON(w, http.StatusOK, executions)
}

// GetPluginHealth handles GET /api/v1/admin/plugins/:id/health
func (h *PluginHandler) GetPluginHealth(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid plugin ID", nil)
		return
	}

	health, err := h.pluginRepo.GetPluginHealth(r.Context(), pluginID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get plugin health", err)
		return
	}

	respondWithJSON(w, http.StatusOK, health)
}

// ======================================================================
// Hook Management Endpoints
// ======================================================================

// GetHooks handles GET /api/v1/admin/plugins/hooks
func (h *PluginHandler) GetHooks(w http.ResponseWriter, r *http.Request) {
	hookManager := h.pluginManager.GetHookManager()

	eventTypes := hookManager.GetAllEventTypes()
	result := make([]map[string]any, len(eventTypes))

	for i, eventType := range eventTypes {
		plugins := hookManager.GetRegisteredHooks(eventType)
		result[i] = map[string]any{
			"event_type":   eventType,
			"plugin_count": len(plugins),
			"plugins":      plugins,
		}
	}

	respondWithJSON(w, http.StatusOK, result)
}

// TriggerHook handles POST /api/v1/admin/plugins/hooks/trigger
func (h *PluginHandler) TriggerHook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventType plugin.EventType `json:"event_type"`
		Data      any              `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", nil)
		return
	}

	if req.EventType == "" {
		respondWithError(w, http.StatusBadRequest, "Event type is required", nil)
		return
	}

	// Trigger the hook
	if err := h.pluginManager.TriggerEvent(context.Background(), req.EventType, req.Data); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to trigger hook", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Hook %s triggered successfully", req.EventType),
	})
}

// ======================================================================
// Utility Endpoints
// ======================================================================

// CleanupExecutions handles POST /api/v1/admin/plugins/cleanup
func (h *PluginHandler) CleanupExecutions(w http.ResponseWriter, r *http.Request) {
	count, err := h.pluginRepo.CleanupOldExecutions(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to cleanup executions", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": fmt.Sprintf("Cleaned up %d old execution records", count),
		"count":   count,
	})
}
