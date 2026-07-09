package httpapi

import (
	"net/http"
	"strings"

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

// instanceSocialLinks is the operator's social/link row (empty strings when
// unset).
type instanceSocialLinks struct {
	Website  string `json:"website"`
	Mastodon string `json:"mastodon"`
	X        string `json:"x"`
	Bluesky  string `json:"bluesky"`
}

// instanceResponse is the public "about this instance" document the frontend
// app shell reads on load: instance name/description, software, whether signup
// is open, and operator-provided legal/contact links (empty when unset).
type instanceResponse struct {
	Name                         string           `json:"name"`
	Description                  string           `json:"description"`
	ShortDescription             string           `json:"short_description"`
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
	// DefaultLanguage is the instance's default content language (a taxonomy
	// language id from GET /videos/config).
	DefaultLanguage string `json:"default_language"`
	// Categories and ModeratorLanguages carry the operator-selected taxonomy
	// ids ([] when unset).
	Categories         []string `json:"categories"`
	ModeratorLanguages []string `json:"moderator_languages"`
	ServerCountry      string   `json:"server_country"`
	// IsSensitive marks an instance dedicated to sensitive content.
	IsSensitive bool `json:"is_sensitive"`
	// SensitiveContentPolicy is hide|warn|blur|display. "hide" is enforced
	// server-side (public browse/search); the others are presentation-only.
	SensitiveContentPolicy string `json:"sensitive_content_policy"`
	// ContactFormEnabled reports the EFFECTIVE contact-form availability: the
	// admin toggle AND a non-empty effective contact email AND an outbound mail
	// path on this deployment. POST /instance/contact answers 409 whenever this
	// is false.
	ContactFormEnabled bool                `json:"contact_form_enabled"`
	SocialLinks        instanceSocialLinks `json:"social_links"`
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
		ShortDescription:             s.settingString(instancesettings.KeyInstanceShortDescription, ""),
		Software:                     instanceSoftware{Name: "vidra", Version: version.Version},
		RegistrationEnabled:          s.registrationEnabled(),
		RegistrationRequiresApproval: s.registrationRequiresApproval(),
		OAuthProviders:               s.cfg.OAuthProviderNames(),
		FederationEnabled:            s.cfg.FederationEnabled,
		TermsURL:                     s.settingString(instancesettings.KeyTermsURL, s.cfg.InstanceTermsURL),
		PrivacyURL:                   s.settingString(instancesettings.KeyPrivacyURL, s.cfg.InstancePrivacyURL),
		ContactEmail:                 s.effectiveContactEmail(),
		DefaultLanguage:              s.settingString(instancesettings.KeyDefaultLanguage, instancesettings.DefaultDefaultLanguage),
		Categories:                   s.settingStrings(instancesettings.KeyInstanceCategories),
		ModeratorLanguages:           s.settingStrings(instancesettings.KeyModeratorLanguages),
		ServerCountry:                s.settingString(instancesettings.KeyServerCountry, ""),
		IsSensitive:                  s.settingBool(instancesettings.KeyInstanceIsSensitive, false),
		SensitiveContentPolicy:       s.sensitiveContentPolicy(),
		ContactFormEnabled:           s.contactFormAvailable(),
		SocialLinks: instanceSocialLinks{
			Website:  s.settingString(instancesettings.KeyWebsiteLink, ""),
			Mastodon: s.settingString(instancesettings.KeyMastodonLink, ""),
			X:        s.settingString(instancesettings.KeyXLink, ""),
			Bluesky:  s.settingString(instancesettings.KeyBlueskyLink, ""),
		},
		Features: instanceFeatures{
			Uploads:  s.uploadsEnabled(),
			Imports:  s.importsEnabled(),
			Live:     s.liveEnabled(),
			Comments: s.commentsEnabled(),
		},
	})
}

// settingStrings returns the effective list value for a list-kind key ([] when
// the settings service is not wired — list keys have no config fallback).
func (s *Server) settingStrings(key string) []string {
	if s.settingssvc != nil {
		return s.settingssvc.Strings(key)
	}
	return []string{}
}

// effectiveContactEmail is the operator contact address the contact form
// delivers to (overlay, else config).
func (s *Server) effectiveContactEmail() string {
	return s.settingString(instancesettings.KeyContactEmail, s.cfg.InstanceContactEmail)
}

// sensitiveContentPolicy is the effective sensitive-content policy
// (hide|warn|blur|display; default hide).
func (s *Server) sensitiveContentPolicy() string {
	return s.settingString(instancesettings.KeySensitiveContentPolicy, instancesettings.DefaultSensitiveContentPolicy)
}

// hideSensitiveVideos reports whether sensitive videos must be excluded from
// the PUBLIC browse/search surfaces (policy "hide"). Owner/admin surfaces and
// direct watch-by-id are never filtered; the other policies are
// presentation-only (frontend).
func (s *Server) hideSensitiveVideos() bool {
	return s.sensitiveContentPolicy() == instancesettings.SensitiveContentPolicyHide
}

// contactFormAvailable is the EFFECTIVE contact-form availability: the admin
// toggle is on AND an effective contact email is set AND this deployment has
// an outbound mail path (a contact mailer is wired).
func (s *Server) contactFormAvailable() bool {
	return s.contactMailer != nil &&
		s.settingBool(instancesettings.KeyContactFormEnabled, false) &&
		strings.TrimSpace(s.effectiveContactEmail()) != ""
}
