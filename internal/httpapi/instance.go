package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

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
	Name                string           `json:"name"`
	Description         string           `json:"description"`
	Software            instanceSoftware `json:"software"`
	RegistrationEnabled bool             `json:"registration_enabled"`
	TermsURL            string           `json:"terms_url"`
	PrivacyURL          string           `json:"privacy_url"`
	ContactEmail        string           `json:"contact_email"`
}

// handleInstance returns public instance metadata. No auth required; it exposes
// only operator-configured, non-sensitive fields.
func (s *Server) handleInstance(c echo.Context) error {
	return c.JSON(http.StatusOK, instanceResponse{
		Name:                s.cfg.InstanceName,
		Description:         s.cfg.InstanceDescription,
		Software:            instanceSoftware{Name: "vidra", Version: version.Version},
		RegistrationEnabled: s.cfg.RegistrationEnabled,
		TermsURL:            s.cfg.InstanceTermsURL,
		PrivacyURL:          s.cfg.InstancePrivacyURL,
		ContactEmail:        s.cfg.InstanceContactEmail,
	})
}
