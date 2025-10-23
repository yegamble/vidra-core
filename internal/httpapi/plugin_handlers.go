package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"athena/internal/domain"
	"athena/internal/plugin"
	"athena/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PluginHandler handles plugin-related HTTP requests
type PluginHandler struct {
	pluginRepo        *repository.PluginRepository
	pluginManager     *plugin.Manager
	signatureVerifier *plugin.SignatureVerifier
	requireSignatures bool // Whether to require signatures for all plugins
}

// NewPluginHandler creates a new plugin handler
func NewPluginHandler(pluginRepo *repository.PluginRepository, pluginManager *plugin.Manager, signatureVerifier *plugin.SignatureVerifier, requireSignatures bool) *PluginHandler {
	return &PluginHandler{
		pluginRepo:        pluginRepo,
		pluginManager:     pluginManager,
		signatureVerifier: signatureVerifier,
		requireSignatures: requireSignatures,
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
// Plugin Upload & Installation
// ======================================================================

// UploadPlugin handles POST /api/v1/admin/plugins
func (h *PluginHandler) UploadPlugin(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (max 50MB)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to parse multipart form", err)
		return
	}

	// Get file from form
	file, header, err := r.FormFile("plugin")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Plugin file is required", err)
		return
	}
	defer func() { _ = file.Close() }()

	// Validate file extension
	if !strings.HasSuffix(header.Filename, ".zip") {
		respondWithError(w, http.StatusBadRequest, "Plugin must be a ZIP file", nil)
		return
	}

	// Read file content
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to read plugin file", err)
		return
	}

	// Extract and validate manifest
	manifest, err := h.extractManifest(fileBytes)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid plugin manifest: %v", err), err)
		return
	}

	// Verify signature if provided or required
	signatureFile, _, err := r.FormFile("signature")
	var signatureBytes []byte
	if err == nil {
		defer func() { _ = signatureFile.Close() }()
		signatureBytes, err = io.ReadAll(signatureFile)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Failed to read signature file", err)
			return
		}
	}

	// Check signature verification
	if h.signatureVerifier != nil {
		if len(signatureBytes) > 0 {
			// Signature provided - verify it
			if err := h.signatureVerifier.VerifySignature(fileBytes, signatureBytes, manifest.Author); err != nil {
				respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Invalid signature: %v", err), err)
				return
			}
		} else if h.requireSignatures {
			// No signature but signatures are required
			respondWithError(w, http.StatusBadRequest, "Plugin signature is required", nil)
			return
		} else if !h.signatureVerifier.IsAuthorTrusted(manifest.Author) {
			// Author not trusted and no signature
			respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("Author %s is not trusted. Please provide a valid signature or add author to trusted list.", manifest.Author), nil)
			return
		}
	}

	// Validate permissions
	if err := plugin.ValidatePermissions(manifest.Permissions); err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid permissions: %v", err), err)
		return
	}

	// Check if plugin already exists
	existing, err := h.pluginRepo.GetByName(r.Context(), manifest.Name)
	if err == nil && existing != nil {
		respondWithError(w, http.StatusConflict, fmt.Sprintf("Plugin %s is already installed", manifest.Name), nil)
		return
	}

	// Create temp directory for extraction
	tempDir, err := os.MkdirTemp("", "plugin-install-*")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temp directory", err)
		return
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Extract plugin files
	pluginDir, err := h.extractPlugin(fileBytes, tempDir, manifest.Name)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to extract plugin: %v", err), err)
		return
	}

	// Move to plugins directory
	finalPath := filepath.Join(h.pluginManager.GetPluginDir(), manifest.Name)
	if err := os.RemoveAll(finalPath); err != nil && !os.IsNotExist(err) {
		respondWithError(w, http.StatusInternalServerError, "Failed to prepare installation path", err)
		return
	}
	if err := os.Rename(pluginDir, finalPath); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to install plugin", err)
		return
	}

	// Create plugin record in database
	pluginRecord := &domain.PluginRecord{
		ID:          uuid.New(),
		Name:        manifest.Name,
		Version:     manifest.Version,
		Author:      manifest.Author,
		Description: manifest.Description,
		Status:      domain.PluginStatusInstalled,
		Config:      manifest.Config,
		Permissions: manifest.Permissions,
		Hooks:       convertEventTypesToStrings(manifest.Hooks),
		InstallPath: finalPath,
	}

	if err := h.pluginRepo.Create(r.Context(), pluginRecord); err != nil {
		// Rollback: remove installed files
		_ = os.RemoveAll(finalPath)
		respondWithError(w, http.StatusInternalServerError, "Failed to register plugin", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]any{
		"id":          pluginRecord.ID,
		"name":        pluginRecord.Name,
		"version":     pluginRecord.Version,
		"status":      pluginRecord.Status,
		"message":     fmt.Sprintf("Plugin %s installed successfully", pluginRecord.Name),
		"permissions": pluginRecord.Permissions,
		"hooks":       pluginRecord.Hooks,
	})
}

// extractManifest extracts and parses plugin.json from the ZIP file
func (h *PluginHandler) extractManifest(zipData []byte) (*plugin.PluginInfo, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read ZIP: %w", err)
	}

	// Find plugin.json
	for _, f := range zipReader.File {
		if f.Name == "plugin.json" || strings.HasSuffix(f.Name, "/plugin.json") {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open plugin.json: %w", err)
			}
			defer func() { _ = rc.Close() }()

			var manifest plugin.PluginInfo
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				return nil, fmt.Errorf("failed to parse plugin.json: %w", err)
			}

			// Validate required fields
			if manifest.Name == "" {
				return nil, fmt.Errorf("plugin name is required")
			}
			if manifest.Version == "" {
				return nil, fmt.Errorf("plugin version is required")
			}
			if manifest.Author == "" {
				return nil, fmt.Errorf("plugin author is required")
			}

			return &manifest, nil
		}
	}

	return nil, fmt.Errorf("plugin.json not found in ZIP")
}

// extractPlugin extracts all files from the ZIP to the specified directory
func (h *PluginHandler) extractPlugin(zipData []byte, destDir, pluginName string) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("failed to read ZIP: %w", err)
	}

	pluginDir := filepath.Join(destDir, pluginName)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create plugin directory: %w", err)
	}

	for _, f := range zipReader.File {
		// Security: prevent path traversal
		if strings.Contains(f.Name, "..") {
			return "", fmt.Errorf("invalid file path: %s", f.Name)
		}

		destPath := filepath.Join(pluginDir, f.Name)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, f.Mode()); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create parent directory if needed
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return "", fmt.Errorf("failed to create parent directory: %w", err)
		}

		// Extract file
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open file in ZIP: %w", err)
		}

		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()
			return "", fmt.Errorf("failed to create file: %w", err)
		}

		if _, err := io.Copy(destFile, rc); err != nil {
			_ = destFile.Close()
			_ = rc.Close()
			return "", fmt.Errorf("failed to write file: %w", err)
		}

		_ = destFile.Close()
		_ = rc.Close()
	}

	return pluginDir, nil
}

// convertEventTypesToStrings converts EventType slice to string slice
func convertEventTypesToStrings(events []plugin.EventType) []string {
	result := make([]string, len(events))
	for i, e := range events {
		result[i] = string(e)
	}
	return result
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
