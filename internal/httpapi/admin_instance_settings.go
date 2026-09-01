package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

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

// --- sign-up & new users (config-parity W7) ---

// registrationRequiresEmailVerification is the EFFECTIVE verification gate:
// the runtime setting AND an outbound mail path on this deployment (the gate
// can never hold accounts behind a message nobody can send). Exposed on
// GET /instance so the signup UI can explain the pending state.
func (s *Server) registrationRequiresEmailVerification() bool {
	return s.contactMailer != nil &&
		s.settingBool(instancesettings.KeyRegistrationRequireEmailVerification, false)
}

// registrationMinimumAge is the signup age-attestation threshold (0 = off).
func (s *Server) registrationMinimumAge() int64 {
	return s.settingInt(instancesettings.KeyRegistrationMinimumAge, 0)
}

// registrationUserLimit is the total-account cap (0 = unlimited).
func (s *Server) registrationUserLimit() int64 {
	return s.settingInt(instancesettings.KeyRegistrationUserLimit, 0)
}

// userCountTTL bounds how stale the cached account count may be. Short enough
// that the limit engages within seconds of being crossed, long enough to keep
// the hot /instance path DB-free.
const userCountTTL = 15 * time.Second

// registrationAtUserLimit reports whether the instance has reached
// registration_user_limit. false while no limit is set (no DB round trip) or
// no auth service is wired. The cached count makes this approximate under
// concurrent signups — documented race tolerance, mirroring the quota gates.
func (s *Server) registrationAtUserLimit(ctx context.Context) bool {
	limit := s.registrationUserLimit()
	if limit <= 0 || s.authsvc == nil {
		return false
	}
	s.userCountMu.Lock()
	defer s.userCountMu.Unlock()
	if time.Since(s.userCountFetched) > userCountTTL {
		n, err := s.authsvc.CountUsers(ctx)
		if err != nil {
			// Fail open: a transient count error must not close signups.
			return false
		}
		s.userCount = n
		s.userCountFetched = time.Now()
	}
	return s.userCount >= limit
}

// invalidateUserCount drops the cached account count (called after a
// successful signup so the limit engages promptly).
func (s *Server) invalidateUserCount() {
	s.userCountMu.Lock()
	s.userCountFetched = time.Time{}
	s.userCountMu.Unlock()
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

// messagingEnabled is the instance-wide direct-messaging switch
// (messaging_enabled). No env/config backing and DEFAULT ON — the shipped
// behaviour — so the fallback here must stay true: an instance whose settings
// service is not wired keeps its DMs.
func (s *Server) messagingEnabled() bool {
	return s.settingBool(instancesettings.KeyMessagingEnabled, true)
}

// messagingE2EEEnabled is the EFFECTIVE end-to-end-encryption availability:
// messaging_e2ee_enabled AND messaging_enabled. The nesting runs one way only.
// E2EE off with messaging on is coherent (plaintext DMs still work), but
// messaging off must take E2EE down too — a device directory and an envelope
// store are meaningless with no conversations to carry.
func (s *Server) messagingE2EEEnabled() bool {
	return s.messagingEnabled() && s.settingBool(instancesettings.KeyMessagingE2EEEnabled, true)
}

// --- shipped-feature toggle batch (config-parity W8) ---
//
// Each helper is the RUNTIME admin setting for one already-shipped feature.
// The gate idiom is uniform: setting off → the affected endpoint answers 403
// feature_disabled (the uploads_enabled shape) and the matching GET /instance
// features flag reports false so clients hide the affordance. A missing BOOT
// capability (yt-dlp not wired, Whisper not configured) keeps its existing
// 503 — a runtime toggle can never conjure a dependency the deployment lacks.

// importHTTPEnabled is the yt-dlp platform-URL import toggle
// (import_http_enabled). Defaults ON: with no override the path is governed by
// the boot capability alone. imports_enabled stays the master switch above it.
func (s *Server) importHTTPEnabled() bool {
	return s.settingBool(instancesettings.KeyImportHTTPEnabled, true)
}

// channelSyncEnabled is the channel auto-sync runtime gate: the
// channel_sync_enabled setting AND the import_http_enabled setting (the sync
// path IS a yt-dlp import path, so turning platform imports off pauses syncs
// too). Boot capability (the yt-dlp resolver) is enforced separately by the
// channelsync service (503).
func (s *Server) channelSyncEnabled() bool {
	return s.settingBool(instancesettings.KeyChannelSyncEnabled, s.cfg.ChannelSyncEnabled) &&
		s.importHTTPEnabled()
}

// storyboardsEnabled is the storyboard-generation toggle (storyboards_enabled;
// no env backing, default on). Generation additionally requires ffmpeg at the
// media seam; stored storyboards keep serving regardless.
func (s *Server) storyboardsEnabled() bool {
	return s.settingBool(instancesettings.KeyStoryboardsEnabled, true)
}

// videoCardPreviewsEnabled is the instance-level master gate for video-card
// hover playback. It is intentionally independent from the signed-in user's
// player preference: clients enable previews only when BOTH are true.
func (s *Server) videoCardPreviewsEnabled() bool {
	return s.settingBool(instancesettings.KeyVideoCardPreviewsEnabled, false)
}

// videoCardPreviewsDefaultEnabled is the effective preference for a signed-in
// user who has never explicitly enabled or disabled video-card previews. It is
// independent from the master gate above: clients still require both values.
func (s *Server) videoCardPreviewsDefaultEnabled() bool {
	return s.settingBool(instancesettings.KeyVideoCardPreviewsDefaultEnabled, false)
}

// transcriptionEnabled is the auto-caption runtime setting
// (transcription_enabled). The EFFECTIVE availability is this AND the Whisper
// boot capability — the captionjob service folds both in (503 when only the
// boot half is missing).
func (s *Server) transcriptionEnabled() bool {
	return s.settingBool(instancesettings.KeyTranscriptionEnabled, s.cfg.WhisperEnabled)
}

// userImportEnabled / userExportEnabled gate the account data-portability
// surfaces (user_import_enabled / user_export_enabled; default on).
func (s *Server) userImportEnabled() bool {
	return s.settingBool(instancesettings.KeyUserImportEnabled, true)
}

func (s *Server) userExportEnabled() bool {
	return s.settingBool(instancesettings.KeyUserExportEnabled, true)
}

// --- live streaming enforcement knobs (config-parity W11) ---

// liveAllowReplay is the instance-level replay master gate (live_allow_replay;
// no env backing, default on). Enforcement lives in live.Service.RunReplay via
// the same setting; this accessor feeds the /instance live block and the
// create-default logic so clients stay in lock-step.
func (s *Server) liveAllowReplay() bool {
	return s.settingBool(instancesettings.KeyLiveAllowReplay, true)
}

// liveDefaultSaveReplay seeds a new live stream's replay flag when the client
// omits it (live_default_save_replay, default off). It is ANDed with
// liveAllowReplay — a disabled replay feature must not seed dormant opt-ins
// (PT parity: the default has no effect while allow_replay is off).
func (s *Server) liveDefaultSaveReplay() bool {
	return s.settingBool(instancesettings.KeyLiveDefaultSaveReplay, false) && s.liveAllowReplay()
}

// --- VOD transcoding runtime knobs (config-parity W10) ---

// transcodingEnabled is the runtime master toggle over the HLS transcoding
// pipeline (transcoding_enabled; default from TRANSCODING_ENABLED). EFFECTIVE
// availability additionally requires the boot capability (ffmpeg/ffprobe
// wired) — the features block ANDs in transcode.Service.Capable().
func (s *Server) transcodingEnabled() bool {
	return s.settingBool(instancesettings.KeyTranscodingEnabled, s.cfg.TranscodingEnabled)
}

// uploadAdditionalExtensionsEnabled reports whether the extended upload
// container set is accepted (upload_additional_extensions_enabled). Default ON
// — vidra's shipped allow-list already included the extended set (documented
// deviation from PeerTube's off default).
func (s *Server) uploadAdditionalExtensionsEnabled() bool {
	return s.settingBool(instancesettings.KeyUploadAdditionalExtensionsEnabled, true)
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
	// Page/Section are the admin-IA placement metadata (config-parity W1): the
	// admin-config page (general|vod|live|federation|customization|homepage|
	// advanced) and in-page section this key renders into, so the metadata-driven
	// admin UI auto-places new keys.
	Page    string `json:"page"`
	Section string `json:"section"`
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
			Page:       e.Page,
			Section:    e.Section,
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
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(c.Request().Body).Decode(&raw); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if len(raw) == 0 {
		return &ValidationError{Fields: []FieldError{{Field: "settings", Message: "at least one setting is required"}}}
	}

	updates, changed, fields := coerceSettingUpdates(raw)
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}

	if err := s.settingssvc.Apply(c.Request().Context(), updates, callerID); err != nil {
		if bad := settingsFieldErrors(err); len(bad) > 0 {
			// KEY NAMES only, never values — the same rule the success event
			// follows (a contact email is operator PII).
			s.audit(c, observability.ActionAdminInstanceUpdate, observability.ResultFailure, callerID.String(), "invalid:"+strings.Join(fieldNames(bad), ","))
			return &ValidationError{Fields: bad}
		}
		return err
	}

	s.audit(c, observability.ActionAdminInstanceUpdate, observability.ResultSuccess, callerID.String(), "keys="+strings.Join(changed, ","))
	// Search: push the effective config to vidra-search when a search key changed
	// (search-service W4). Best-effort.
	s.emitSearchConfigChangedIfNeeded(c.Request().Context(), changed)
	return c.JSON(http.StatusOK, s.instanceSettingsResponse())
}

// instanceSettingsValidationResponse is the answer to a DRY RUN: the field
// problems this body would produce, or an empty list when it would be accepted.
// fields is never null — an empty array is the "clean" answer a client renders
// as "no problems", and `null` would make that ambiguous.
type instanceSettingsValidationResponse struct {
	Fields []FieldError `json:"fields"`
}

// handleValidateInstanceSettings reports the field problems a body would
// produce, without writing anything. Behind requireRole(admin), same body shape
// as the PATCH.
//
// It exists so the admin config form can validate a field as the operator
// leaves it, against the SERVER's rules, instead of a hand-copied second copy of
// them in TypeScript — a copy drifts the first time either side is edited, and
// the operator discovers the drift as a 422 on save. The PATCH's 422 stays the
// backstop; this is the early answer.
//
// It is not a byte-for-byte replay of the write, and deliberately so. The PATCH
// stops after the type pass — nothing may be persisted, so there is no reason to
// go further — while a dry run has nothing to protect and carries on to check
// the CONTENT of every key that typed cleanly. On a mixed body it therefore
// reports MORE problems than the PATCH would have, never different ones: each
// message comes from the same rule the write would have applied, so no finding
// here can be a false alarm at save time. Reporting the whole list at once is
// the point — a form that reveals its problems a pass at a time is a form the
// operator submits repeatedly.
//
// It always answers 200: the field problems ARE the successful result of asking
// a validation question, so a 422 here would mean "your validation request was
// invalid", which is a different sentence. Nothing is persisted and no audit
// event is emitted — a dry run changes nothing, and an event per keystroke would
// drown the trail that records real changes.
func (s *Server) handleValidateInstanceSettings(c echo.Context) error {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(c.Request().Body).Decode(&raw); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if len(raw) == 0 {
		return c.JSON(http.StatusOK, instanceSettingsValidationResponse{
			Fields: []FieldError{{Field: "settings", Message: "at least one setting is required"}},
		})
	}
	// The same coercion the PATCH runs, so a type problem is reported here in
	// the words the write would have used.
	updates, _, fields := coerceSettingUpdates(raw)
	for _, key := range sortedUpdateKeys(updates) {
		u := updates[key]
		if u.Delete {
			// Clearing an override restores the config default, which validated
			// at boot. There is nothing to check.
			continue
		}
		if err := instancesettings.Validate(key, u.Value); err != nil {
			fields = append(fields, settingsFieldErrors(err)...)
		}
	}
	if fields == nil {
		fields = []FieldError{}
	}
	return c.JSON(http.StatusOK, instanceSettingsValidationResponse{Fields: fields})
}

// coerceSettingUpdates turns a settings write body — a flat JSON object of
// setting key → new value — into the normalised string form the settings
// service validates and stores, collecting a TYPE error per key rather than
// stopping at the first. The PATCH and the dry-run endpoint share it verbatim:
// a second copy of "which JSON type does this kind take" is a second place for
// the answer to drift.
//
// Keys are visited in sorted order so the problems come back in the same
// sequence on every call — Go randomises map iteration, and a form whose error
// list reshuffles between saves reads as broken. changed names the keys that
// produced a well-typed update, sorted: it is the audit event's key-names-only
// payload.
func coerceSettingUpdates(raw map[string]json.RawMessage) (map[string]instancesettings.Update, []string, []FieldError) {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	updates := make(map[string]instancesettings.Update, len(raw))
	changed := make([]string, 0, len(raw))
	var fields []FieldError
	for _, key := range keys {
		rawVal := raw[key]
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
	return updates, changed, fields
}

// sortedUpdateKeys orders a coerced batch, so the dry run reports its per-key
// rejections in the same sequence every time.
func sortedUpdateKeys(updates map[string]instancesettings.Update) []string {
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// settingsFieldErrors flattens what the settings service returns — one
// *instancesettings.ValidationError per offending key, joined — into the
// field-error list the 422 envelope and the dry-run response both carry.
//
// It returns nil when ANY leaf of the tree is something else: a persistence
// failure is not a form problem, and rendering half a write failure as "fix this
// field" would tell an admin their database outage is a typo. The caller falls
// through to the generic 500 path in that case.
func settingsFieldErrors(err error) []FieldError {
	var out []FieldError
	ours := true
	var walk func(error)
	walk = func(e error) {
		if e == nil || !ours {
			return
		}
		if joined, isJoined := e.(interface{ Unwrap() []error }); isJoined {
			for _, sub := range joined.Unwrap() {
				walk(sub)
			}
			return
		}
		var ve *instancesettings.ValidationError
		if errors.As(e, &ve) {
			out = append(out, FieldError{Field: ve.Key, Message: ve.Message})
			return
		}
		ours = false
	}
	walk(err)
	if !ours {
		return nil
	}
	return out
}

// fieldNames lists the offending field names for an audit reason — names only,
// never the values that failed.
func fieldNames(fields []FieldError) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Field)
	}
	return out
}

// isJSONNull reports whether a raw JSON value is the literal null.
func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}
