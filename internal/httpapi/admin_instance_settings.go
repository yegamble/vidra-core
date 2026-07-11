package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/bytes"

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

func (s *Server) settingInt(key string, configDefault int64) int64 {
	if s.settingssvc != nil {
		return s.settingssvc.Int(key)
	}
	return configDefault
}

// uploadMaxSizeBytes is the effective single-upload byte cap (0 = no cap): the
// DB overlay when wired, else the boot-validated UPLOAD_MAX_SIZE parsed to bytes.
func (s *Server) uploadMaxSizeBytes() int64 {
	def, _ := bytes.Parse(s.cfg.UploadMaxSize) // boot-validated (config.go), so err is unreachable
	return s.settingInt(instancesettings.KeyUploadMaxSizeBytes, def)
}

// uploadMaxActiveSessions is the effective per-user active-session cap
// (0 = unlimited): the DB overlay when wired, else UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER.
func (s *Server) uploadMaxActiveSessions() int {
	return int(s.settingInt(instancesettings.KeyUploadMaxActiveSessionsPerUser, int64(s.cfg.UploadMaxActiveSessionsPerUser)))
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

// downloadsEnabled reports the runtime download-policy toggle. Downloads have
// no env/config backing and default on when the settings service is not wired.
func (s *Server) downloadsEnabled() bool {
	return s.settingBool(instancesettings.KeyDownloadsEnabled, true)
}

// --- admin GET/PATCH /admin/instance-settings ---

// instanceSettingView is one setting's effective state in the admin response:
// the effective value (a string, a bool, or an array of strings per type), the
// config default, and whether the DB currently overrides it. Enum-typed
// settings additionally list their allowed options.
type instanceSettingView struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	Value      any    `json:"value"`
	Default    any    `json:"default"`
	Overridden bool   `json:"overridden"`
	// Options is present for type=enum only: the allowed values, in display order.
	Options []string `json:"options,omitempty"`
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
			Options:    e.Options,
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
		case instancesettings.KindInt:
			// Unmarshalling into int64 already rejects JSON strings, floats, and
			// out-of-range magnitudes — the field-type check the API needs. Range
			// and sentinel rules stay in the settings service, like every kind.
			var n int64
			if err := json.Unmarshal(rawVal, &n); err != nil {
				fields = append(fields, FieldError{Field: key, Message: "must be an integer"})
				continue
			}
			updates[key] = instancesettings.Update{Value: instancesettings.FormatInt(n)}
		case instancesettings.KindString, instancesettings.KindEnum:
			// Enum values are JSON strings too; the settings service validates
			// them against the option set (like every other content rule).
			var str string
			if err := json.Unmarshal(rawVal, &str); err != nil {
				fields = append(fields, FieldError{Field: key, Message: "must be a string"})
				continue
			}
			updates[key] = instancesettings.Update{Value: str}
		case instancesettings.KindList:
			var items []string
			if err := json.Unmarshal(rawVal, &items); err != nil {
				fields = append(fields, FieldError{Field: key, Message: "must be an array of strings"})
				continue
			}
			// Canonicalise before storing (compact JSON array); per-item taxonomy
			// validation happens in the settings service.
			updates[key] = instancesettings.Update{Value: instancesettings.FormatList(items)}
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
