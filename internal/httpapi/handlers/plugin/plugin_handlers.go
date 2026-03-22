package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/plugin"
	"athena/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type PluginHandler struct {
	pluginRepo        *repository.PluginRepository
	pluginManager     *plugin.Manager
	signatureVerifier *plugin.SignatureVerifier
	requireSignatures bool
}

func NewPluginHandler(pluginRepo *repository.PluginRepository, pluginManager *plugin.Manager, signatureVerifier *plugin.SignatureVerifier, requireSignatures bool) *PluginHandler {
	return &PluginHandler{
		pluginRepo:        pluginRepo,
		pluginManager:     pluginManager,
		signatureVerifier: signatureVerifier,
		requireSignatures: requireSignatures,
	}
}

func (h *PluginHandler) ListPlugins(w http.ResponseWriter, r *http.Request) {
	statusParam := r.URL.Query().Get("status")

	var status *domain.PluginStatus
	if statusParam != "" {
		s := domain.PluginStatus(statusParam)
		status = &s
	}

	plugins, err := h.pluginRepo.List(r.Context(), status)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list plugins: %w", err))
		return
	}

	result := make([]map[string]any, len(plugins))

	pluginIDs := make([]uuid.UUID, len(plugins))
	for i, p := range plugins {
		pluginIDs[i] = p.ID
	}

	statsMap, _ := h.pluginRepo.GetStatisticsForPlugins(r.Context(), pluginIDs)
	if statsMap == nil {
		statsMap = make(map[uuid.UUID]*domain.PluginStatistics)
	}

	for i, p := range plugins {
		stats := statsMap[p.ID]

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

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *PluginHandler) GetPlugin(w http.ResponseWriter, r *http.Request) {
	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	stats, _ := h.pluginRepo.GetStatistics(r.Context(), plugin.ID)

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

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *PluginHandler) EnablePlugin(w http.ResponseWriter, r *http.Request) {
	h.togglePluginStatus(w, r, true)
}

func (h *PluginHandler) DisablePlugin(w http.ResponseWriter, r *http.Request) {
	h.togglePluginStatus(w, r, false)
}

func (h *PluginHandler) togglePluginStatus(w http.ResponseWriter, r *http.Request, enable bool) {
	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	if enable {
		if plugin.IsEnabled() {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ALREADY_ENABLED", "Plugin already enabled"))
			return
		}
	} else {
		if plugin.IsDisabled() {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ALREADY_DISABLED", "Plugin already disabled"))
			return
		}
	}

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
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to %s plugin: %v", action, managerErr))
		return
	}

	var statusErr error
	if enable {
		statusErr = plugin.Enable()
	} else {
		statusErr = plugin.Disable()
	}
	if statusErr != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update plugin status: %w", statusErr))
		return
	}

	if err := h.pluginRepo.Update(r.Context(), plugin); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to save plugin status: %w", err))
		return
	}

	action := "enabled"
	if !enable {
		action = "disabled"
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Plugin %s %s successfully", plugin.Name, action),
	})
}

func (h *PluginHandler) UpdatePluginConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config map[string]any `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Invalid request body"))
		return
	}

	if req.Config == nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Config is required"))
		return
	}

	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	if err := h.updatePluginConfigData(r.Context(), plugin, req.Config); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update plugin config: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "Plugin configuration updated successfully",
	})
}

func (h *PluginHandler) UninstallPlugin(w http.ResponseWriter, r *http.Request) {
	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	if plugin.IsEnabled() {
		if err := h.pluginManager.DisablePlugin(r.Context(), plugin.Name); err != nil && !isMissingRuntimePluginError(err) {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to disable plugin before uninstall: %w", err))
			return
		}
	}

	if err := h.pluginRepo.Delete(r.Context(), plugin.ID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to uninstall plugin: %w", err))
		return
	}
	_ = os.RemoveAll(plugin.InstallPath)

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Plugin %s uninstalled successfully", plugin.Name),
	})
}

func (h *PluginHandler) GetPluginStatistics(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid plugin ID"))
		return
	}

	stats, err := h.pluginRepo.GetStatistics(r.Context(), pluginID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get statistics: %w", err))
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

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *PluginHandler) GetRegisteredSettings(w http.ResponseWriter, r *http.Request) {
	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"npmName":            plugin.Name,
		"settings":           plugin.Config,
		"registeredSettings": plugin.Config,
	})
}

func (h *PluginHandler) GetPublicSettings(w http.ResponseWriter, r *http.Request) {
	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"npmName":        plugin.Name,
		"publicSettings": plugin.Config,
	})
}

func (h *PluginHandler) UpdateCanonicalSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config   map[string]any `json:"config"`
		Settings map[string]any `json:"settings"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Invalid request body"))
		return
	}

	config := req.Config
	if config == nil {
		config = req.Settings
	}
	if config == nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Settings are required"))
		return
	}

	plugin, ok := h.resolvePluginRecord(w, r)
	if !ok {
		return
	}

	if err := h.updatePluginConfigData(r.Context(), plugin, config); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update plugin settings: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"npmName":  plugin.Name,
		"settings": plugin.Config,
		"message":  "Plugin settings updated successfully",
	})
}

func (h *PluginHandler) InstallPluginFromURL(w http.ResponseWriter, r *http.Request) {
	h.installPluginFromURL(w, r, false)
}

func (h *PluginHandler) UpdatePluginFromURL(w http.ResponseWriter, r *http.Request) {
	h.installPluginFromURL(w, r, true)
}

func (h *PluginHandler) UninstallPluginCanonical(w http.ResponseWriter, r *http.Request) {
	identifier, err := canonicalPluginIdentifier(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("npmName", identifier)
	req := r.Clone(contextWithRouteContext(r, rctx))
	h.UninstallPlugin(w, req)
}

func (h *PluginHandler) GetAllStatistics(w http.ResponseWriter, r *http.Request) {
	statistics, err := h.pluginRepo.GetAllStatistics(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get statistics: %w", err))
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

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *PluginHandler) GetExecutionHistory(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid plugin ID"))
		return
	}

	limit := 100

	executions, err := h.pluginRepo.GetExecutionHistory(r.Context(), pluginID, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get execution history: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, executions)
}

func (h *PluginHandler) GetPluginHealth(w http.ResponseWriter, r *http.Request) {
	pluginIDStr := chi.URLParam(r, "id")
	pluginID, err := uuid.Parse(pluginIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid plugin ID"))
		return
	}

	health, err := h.pluginRepo.GetPluginHealth(r.Context(), pluginID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get plugin health: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, health)
}

func (h *PluginHandler) resolvePluginRecord(w http.ResponseWriter, r *http.Request) (*domain.PluginRecord, bool) {
	identifier := pluginIdentifier(r)
	if identifier == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid plugin identifier"))
		return nil, false
	}

	if h.pluginRepo == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("PLUGIN_REPO_UNAVAILABLE", "Plugin repository not configured"))
		return nil, false
	}

	plugin, err := h.getPluginByIdentifier(r, identifier)
	if err == domain.ErrPluginNotFound {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Plugin not found"))
		return nil, false
	}
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get plugin: %w", err))
		return nil, false
	}

	return plugin, true
}

func (h *PluginHandler) getPluginByIdentifier(r *http.Request, identifier string) (*domain.PluginRecord, error) {
	if pluginID, err := uuid.Parse(identifier); err == nil {
		return h.pluginRepo.GetByID(r.Context(), pluginID)
	}

	return h.pluginRepo.GetByName(r.Context(), identifier)
}

func pluginIdentifier(r *http.Request) string {
	for _, key := range []string{"id", "name", "npmName"} {
		if value := chi.URLParam(r, key); value != "" {
			return value
		}
	}

	return ""
}

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

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *PluginHandler) TriggerHook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventType plugin.EventType `json:"event_type"`
		Data      any              `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Invalid request body"))
		return
	}

	if req.EventType == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Event type is required"))
		return
	}

	if err := h.pluginManager.TriggerEvent(r.Context(), req.EventType, req.Data); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to trigger hook: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Hook %s triggered successfully", req.EventType),
	})
}

func (h *PluginHandler) UploadPlugin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("failed to parse multipart form: %w", err))
		return
	}

	file, header, err := r.FormFile("plugin")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("plugin file is required: %w", err))
		return
	}
	defer func() { _ = file.Close() }()

	if !strings.HasSuffix(header.Filename, ".zip") {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Plugin must be a ZIP file"))
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to read plugin file: %w", err))
		return
	}

	manifest, err := h.extractManifest(fileBytes)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid plugin manifest: %v", err))
		return
	}

	signatureFile, _, err := r.FormFile("signature")
	var signatureBytes []byte
	if err == nil {
		defer func() { _ = signatureFile.Close() }()
		signatureBytes, err = io.ReadAll(signatureFile)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("failed to read signature file: %w", err))
			return
		}
	}

	if h.signatureVerifier != nil {
		if len(signatureBytes) > 0 {
			if err := h.signatureVerifier.VerifySignature(fileBytes, signatureBytes, manifest.Author); err != nil {
				shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("invalid signature: %v", err))
				return
			}
		} else if h.requireSignatures {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ERROR", "Plugin signature is required"))
			return
		} else if !h.signatureVerifier.IsAuthorTrusted(manifest.Author) {
			shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("author %s is not trusted; please provide a valid signature or add author to trusted list", manifest.Author))
			return
		}
	}

	if err := plugin.ValidatePermissions(manifest.Permissions); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid permissions: %v", err))
		return
	}

	pluginRecord, err := installPluginArchive(r.Context(), h.pluginRepo, h.pluginManager, fileBytes, false)
	if err != nil {
		if err == domain.ErrPluginAlreadyExists {
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("plugin %s is already installed", manifest.Name))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to install plugin: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":          pluginRecord.ID,
		"name":        pluginRecord.Name,
		"version":     pluginRecord.Version,
		"status":      pluginRecord.Status,
		"message":     fmt.Sprintf("Plugin %s installed successfully", pluginRecord.Name),
		"permissions": pluginRecord.Permissions,
		"hooks":       pluginRecord.Hooks,
	})
}

func (h *PluginHandler) extractManifest(zipData []byte) (*plugin.PluginInfo, error) {
	return extractPluginManifest(zipData)
}

func (h *PluginHandler) extractPlugin(zipData []byte, destDir, pluginName string) (string, error) {
	return extractPluginArchive(zipData, destDir, pluginName)
}

func (h *PluginHandler) CleanupExecutions(w http.ResponseWriter, r *http.Request) {
	count, err := h.pluginRepo.CleanupOldExecutions(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to cleanup executions: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": fmt.Sprintf("Cleaned up %d old execution records", count),
		"count":   count,
	})
}

func (h *PluginHandler) installPluginFromURL(w http.ResponseWriter, r *http.Request, overwrite bool) {
	var req struct {
		PluginURL string `json:"pluginURL"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PluginURL == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "pluginURL is required"))
		return
	}

	if !strings.HasPrefix(req.PluginURL, "https://") {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_URL", "pluginURL must be an https URL"))
		return
	}

	archive, err := downloadPluginArchive(req.PluginURL)
	if err != nil {
		shared.WriteError(w, http.StatusBadGateway, fmt.Errorf("failed to download plugin archive: %w", err))
		return
	}

	record, err := installPluginArchive(r.Context(), h.pluginRepo, h.pluginManager, archive, overwrite)
	if err != nil {
		switch err {
		case domain.ErrPluginAlreadyExists:
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("plugin is already installed"))
		case domain.ErrPluginNotFound:
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("plugin is not installed"))
		default:
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to install plugin: %w", err))
		}
		return
	}

	status := http.StatusCreated
	if overwrite {
		status = http.StatusOK
	}

	shared.WriteJSON(w, status, map[string]any{
		"id":      record.ID,
		"name":    record.Name,
		"version": record.Version,
		"status":  record.Status,
	})
}

func (h *PluginHandler) updatePluginConfigData(ctx context.Context, pluginRecord *domain.PluginRecord, config map[string]any) error {
	if h.pluginManager != nil {
		if err := h.pluginManager.UpdatePluginConfig(ctx, pluginRecord.Name, config); err != nil && !isMissingRuntimePluginError(err) {
			return err
		}
	}

	if err := pluginRecord.UpdateConfig(config); err != nil {
		return err
	}

	return h.pluginRepo.Update(ctx, pluginRecord)
}

func canonicalPluginIdentifier(r *http.Request) (string, error) {
	if identifier := pluginIdentifier(r); identifier != "" {
		return identifier, nil
	}

	var req struct {
		NPMName string `json:"npmName"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", domain.NewDomainError("INVALID_REQUEST", "Invalid request body")
	}
	if req.NPMName != "" {
		return req.NPMName, nil
	}
	if req.Name != "" {
		return req.Name, nil
	}

	return "", domain.NewDomainError("INVALID_REQUEST", "Plugin name is required")
}

func isMissingRuntimePluginError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

func contextWithRouteContext(r *http.Request, rctx *chi.Context) context.Context {
	return context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
}
