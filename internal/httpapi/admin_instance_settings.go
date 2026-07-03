package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
)

// --- effective-setting accessors ---
//
// Each gate/handler reads the EFFECTIVE value: the DB overlay when the settings
// service is wired (it already folds in the config default), else the static
// config. Unit-test servers constructed without a settings service transparently
// fall back to config, so existing behaviour is preserved.

func (s *Server) settingBool(key string, configDefault bool) bool {
	if s.settingssvc != nil {
		return s.settingssvc.Bool(key)
	}
	return configDefault
}

func (s *Server) settingString(key, configDefault string) string {
	if s.settingssvc != nil {
		return s.settingssvc.String(key)
	}
	return configDefault
}

func (s *Server) registrationEnabled() bool {
	return s.settingBool(instancesettings.KeyRegistrationEnabled, s.cfg.RegistrationEnabled)
}

func (s *Server) registrationRequiresApproval() bool {
	return s.settingBool(instancesettings.KeyRegistrationRequireApproval, s.cfg.RegistrationRequireApproval)
}

func (s *Server) uploadsEnabled() bool {
	return s.settingBool(instancesettings.KeyUploadsEnabled, s.cfg.UploadsEnabled)
}

func (s *Server) importsEnabled() bool {
	return s.settingBool(instancesettings.KeyImportsEnabled, s.cfg.ImportsEnabled)
}

func (s *Server) liveEnabled() bool {
	return s.settingBool(instancesettings.KeyLiveEnabled, s.cfg.LiveEnabled)
}

func (s *Server) commentsEnabled() bool {
	return s.settingBool(instancesettings.KeyCommentsEnabled, s.cfg.CommentsEnabled)
}

// --- admin GET/PATCH /admin/instance-settings ---

// instanceSettingView is one setting's effective state in the admin response:
// the effective value (a string or a bool), the config default, and whether the
// DB currently overrides it. value/default are JSON string or bool per type.
type instanceSettingView struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	Value      any    `json:"value"`
	Default    any    `json:"default"`
	Overridden bool   `json:"overridden"`
}

// instanceSettingsResponse is the admin instance-settings document: every
// mutable setting's effective state, in stable registry order.
type instanceSettingsResponse struct {
	Settings []instanceSettingView `json:"settings"`
}

func (s *Server) instanceSettingsResponse() instanceSettingsResponse {
	snap := s.settingssvc.Snapshot()
	views := make([]instanceSettingView, 0, len(snap))
	for _, e := range snap {
		views = append(views, instanceSettingView{
			Key:        e.Key,
			Type:       string(e.Kind),
			Value:      e.Value,
			Default:    e.Default,
			Overridden: e.Overridden,
		})
	}
	return instanceSettingsResponse{Settings: views}
}

// handleGetInstanceSettings returns every mutable instance setting's effective
// value + whether it is overridden. Behind requireRole(admin).
func (s *Server) handleGetInstanceSettings(c echo.Context) error {
	return c.JSON(http.StatusOK, s.instanceSettingsResponse())
}

// handleUpdateInstanceSettings applies a partial, per-key-validated update to the
// instance-settings overlay and returns the full effective document. Behind
// requireRole(admin). The body is a flat JSON object of setting key → new value
// (bool for toggle keys, string for text keys); a null value clears the override
// (reset to the config default). Unknown keys or type/content-invalid values are
// 422 with field errors; nothing is written on any validation failure. Emits an
// audit event carrying the changed KEY NAMES only (never values — the contact
// email is operator PII).
func (s *Server) handleUpdateInstanceSettings(c echo.Context) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(c.Request().Body).Decode(&raw); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if len(raw) == 0 {
		return &ValidationError{Fields: []FieldError{{Field: "settings", Message: "at least one setting is required"}}}
	}

	updates := make(map[string]instancesettings.Update, len(raw))
	changed := make([]string, 0, len(raw))
	var fields []FieldError
	for key, rawVal := range raw {
		kind, known := instancesettings.KindOf(key)
		if !known {
			fields = append(fields, FieldError{Field: key, Message: "unknown setting"})
			continue
		}
		if isJSONNull(rawVal) {
			updates[key] = instancesettings.Update{Delete: true}
			changed = append(changed, key)
			continue
		}
		switch kind {
		case instancesettings.KindBool:
			var b bool
			if err := json.Unmarshal(rawVal, &b); err != nil {
				fields = append(fields, FieldError{Field: key, Message: "must be a boolean"})
				continue
			}
			updates[key] = instancesettings.Update{Value: instancesettings.FormatBool(b)}
		case instancesettings.KindString:
			var str string
			if err := json.Unmarshal(rawVal, &str); err != nil {
				fields = append(fields, FieldError{Field: key, Message: "must be a string"})
				continue
			}
			updates[key] = instancesettings.Update{Value: str}
		}
		changed = append(changed, key)
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}

	if err := s.settingssvc.Apply(c.Request().Context(), updates, callerID); err != nil {
		var ve *instancesettings.ValidationError
		if errors.As(err, &ve) {
			s.audit(c, observability.ActionAdminInstanceUpdate, observability.ResultFailure, callerID.String(), "invalid:"+ve.Key)
			return &ValidationError{Fields: []FieldError{{Field: ve.Key, Message: ve.Message}}}
		}
		return err
	}

	sort.Strings(changed)
	s.audit(c, observability.ActionAdminInstanceUpdate, observability.ResultSuccess, callerID.String(), "keys="+strings.Join(changed, ","))
	return c.JSON(http.StatusOK, s.instanceSettingsResponse())
}

// isJSONNull reports whether a raw JSON value is the literal null.
func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}
