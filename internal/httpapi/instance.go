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

// instanceFeatures reports the EFFECTIVE feature toggles (the DB-backed
// instance-settings overlay over static config) so the frontend can disable the
// matching affordances — e.g. hide the studio upload form when uploads are off.
type instanceFeatures struct {
	Uploads  bool `json:"uploads"`
	Imports  bool `json:"imports"`
	Live     bool `json:"live"`
	Comments bool `json:"comments"`
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
	// Features carries the effective feature toggles so the frontend can gate
	// its affordances in lock-step with the backend's enforcement.
	Features instanceFeatures `json:"features"`
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
		Features: instanceFeatures{
			Uploads:  s.uploadsEnabled(),
			Imports:  s.importsEnabled(),
			Live:     s.liveEnabled(),
			Comments: s.commentsEnabled(),
		},
	})
}
