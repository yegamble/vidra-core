package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
)

// PluginInstaller is the narrow interface for installing plugins.
type PluginInstaller interface {
	InstallFromURL(ctx context.Context, pluginURL string) error
}

// PluginInstallHandlers handles plugin install/available endpoints.
type PluginInstallHandlers struct {
	installer PluginInstaller
}

// NewPluginInstallHandlers returns a new PluginInstallHandlers.
func NewPluginInstallHandlers(installer PluginInstaller) *PluginInstallHandlers {
	return &PluginInstallHandlers{installer: installer}
}

// InstallPlugin handles POST /admin/plugins/install
func (h *PluginInstallHandlers) InstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PluginURL string `json:"pluginURL"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PluginURL == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "pluginURL is required"))
		return
	}

	// SSRF protection: only allow https:// URLs
	parsed, err := url.Parse(req.PluginURL)
	if err != nil || parsed.Scheme != "https" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_URL", "pluginURL must be an https URL"))
		return
	}

	if err := h.installer.InstallFromURL(r.Context(), req.PluginURL); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INSTALL_FAILED", "Failed to install plugin"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAvailablePlugins handles GET /admin/plugins/available
func (h *PluginInstallHandlers) ListAvailablePlugins(w http.ResponseWriter, r *http.Request) {
	// Returns an empty list initially; future versions will query a plugin registry.
	shared.WriteJSON(w, http.StatusOK, []map[string]string{})
}
