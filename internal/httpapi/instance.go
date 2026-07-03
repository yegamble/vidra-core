package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/version"
)

// instanceSoftware describes the running software.
type instanceSoftware struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// instanceResponse is the public "about this instance" document the frontend
// app shell reads on load: instance name/description, software, whether signup
// is open, and operator-provided legal/contact links (empty when unset).
type instanceResponse struct {
	Name                         string           `json:"name"`
	Description                  string           `json:"description"`
	Software                     instanceSoftware `json:"software"`
	RegistrationEnabled          bool             `json:"registration_enabled"`
	RegistrationRequiresApproval bool             `json:"registration_requires_approval"`
	// OAuthProviders lists the configured OIDC login provider names so the
	// frontend can render "continue with …" buttons pointing at
	// GET /api/v1/auth/oauth/{provider}. Empty array when OAuth is off.
	OAuthProviders []string `json:"oauth_providers"`
	// FederationEnabled reports whether ActivityPub federation is on, so the
	// frontend can show/hide remote-content surfaces (remote follows, remote
	// videos).
	FederationEnabled bool   `json:"federation_enabled"`
	TermsURL          string `json:"terms_url"`
	PrivacyURL        string `json:"privacy_url"`
	ContactEmail      string `json:"contact_email"`
}

// handleInstance returns public instance metadata. No auth required; it exposes
// only operator-configured, non-sensitive fields (provider names, never client
// credentials). The name/description/legal-contact metadata and the registration
// gates reflect the EFFECTIVE values — the DB-backed instance-settings overlay
// (fix_plan P10) when wired, else the static config.
func (s *Server) handleInstance(c echo.Context) error {
	return c.JSON(http.StatusOK, instanceResponse{
		Name:                         s.settingString(instancesettings.KeyInstanceName, s.cfg.InstanceName),
		Description:                  s.settingString(instancesettings.KeyInstanceDescription, s.cfg.InstanceDescription),
		Software:                     instanceSoftware{Name: "vidra", Version: version.Version},
		RegistrationEnabled:          s.registrationEnabled(),
		RegistrationRequiresApproval: s.registrationRequiresApproval(),
		OAuthProviders:               s.cfg.OAuthProviderNames(),
		FederationEnabled:            s.cfg.FederationEnabled,
		TermsURL:                     s.settingString(instancesettings.KeyTermsURL, s.cfg.InstanceTermsURL),
		PrivacyURL:                   s.settingString(instancesettings.KeyPrivacyURL, s.cfg.InstancePrivacyURL),
		ContactEmail:                 s.settingString(instancesettings.KeyContactEmail, s.cfg.InstanceContactEmail),
	})
}
