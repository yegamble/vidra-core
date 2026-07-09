// Package instancesettings implements the DB-backed dynamic instance-settings
// overlay (fix_plan P10 admin instance configuration + feature toggles).
//
// A defined MUTABLE subset of instance settings — the instance name/description
// and legal-contact metadata, the registration gates, the upload-quarantine
// gate, and the uploads/imports/live/comments feature toggles — can be changed
// at runtime by an admin and takes effect without a restart. Every other
// setting (the database DSN, the KEKs, the JWT signing secret, the storage
// backend) is boot-time-only and STAYS in config: those are either unsafe to
// change without a restart or are secrets that must never live in a queryable
// table.
//
// The service loads every override row from instance_settings at boot and
// caches them in memory. A typed accessor per key returns the DB override or
// the config default (supplied at construction via Defaults). A write validates
// the incoming values per key, persists them, and invalidates + reloads the
// cache so subsequent reads see the change. The overlay is read on the hot path
// (the GET /instance document and the upload/import/live/comment/registration
// gates), so reads are lock-guarded map lookups with no per-request DB round
// trip.
package instancesettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// Setting keys. These are the stable, snake_case identifiers stored in the
// instance_settings.key column and accepted by PATCH /admin/instance-settings.
const (
	KeyInstanceName                = "instance_name"
	KeyInstanceDescription         = "instance_description"
	KeyTermsURL                    = "terms_url"
	KeyPrivacyURL                  = "privacy_url"
	KeyContactEmail                = "contact_email"
	KeyRegistrationEnabled         = "registration_enabled"
	KeyRegistrationRequireApproval = "registration_require_approval"
	KeyQuarantineNewUploads        = "quarantine_new_uploads"
	KeyUploadsEnabled              = "uploads_enabled"
	KeyImportsEnabled              = "imports_enabled"
	KeyLiveEnabled                 = "live_enabled"
	KeyCommentsEnabled             = "comments_enabled"

	// Platform-information keys (spec instance-platform-info). All are pure
	// key-value overlay rows with HARDCODED defaults (no config/env backing):
	// zero values, except default_language ("en") and sensitive_content_policy
	// ("hide"). The markdown keys store/serve raw markdown text.
	KeyInstanceShortDescription = "instance_short_description"
	KeyServerCountry            = "server_country"
	KeySupportText              = "support_text"
	KeyWebsiteLink              = "website_link"
	KeyMastodonLink             = "mastodon_link"
	KeyXLink                    = "x_link"
	KeyBlueskyLink              = "bluesky_link"
	KeyTerms                    = "terms" // markdown body; distinct from terms_url
	KeyCodeOfConduct            = "code_of_conduct"
	KeyModerationInfo           = "moderation_info"
	KeyAdministratorInfo        = "administrator_info"
	KeyCreationReason           = "creation_reason"
	KeyMaintenanceLifetime      = "maintenance_lifetime"
	KeyBusinessModel            = "business_model"
	KeyHardwareInfo             = "hardware_info"
	KeyDefaultLanguage          = "default_language"
	KeyContactFormEnabled       = "contact_form_enabled"
	KeyInstanceIsSensitive      = "instance_is_sensitive"
	KeySensitiveContentPolicy   = "sensitive_content_policy"
	KeyInstanceCategories       = "instance_categories"
	KeyModeratorLanguages       = "moderator_languages"
)

// Kind is a setting's value type, reported to clients and used to validate the
// incoming PATCH payload.
type Kind string

const (
	KindString Kind = "string"
	KindBool   Kind = "bool"
	// KindEnum is a string constrained to a fixed option set; the admin view
	// exposes the options so a client can render a picker.
	KindEnum Kind = "enum"
	// KindList is a list of strings. The TEXT value column stores the canonical
	// JSON array; the API value is a JSON array of strings.
	KindList Kind = "list"
)

// Sensitive-content policy values (KeySensitiveContentPolicy). "hide" is
// enforced server-side (sensitive videos drop out of the public browse/search
// surfaces); the others are presentation-only, applied by the frontend.
const (
	SensitiveContentPolicyHide    = "hide"
	SensitiveContentPolicyWarn    = "warn"
	SensitiveContentPolicyBlur    = "blur"
	SensitiveContentPolicyDisplay = "display"
)

// SensitiveContentPolicyOptions is the canonical option order for the
// sensitive_content_policy enum (also the admin-view/OpenAPI order).
var SensitiveContentPolicyOptions = []string{
	SensitiveContentPolicyHide,
	SensitiveContentPolicyWarn,
	SensitiveContentPolicyBlur,
	SensitiveContentPolicyDisplay,
}

// Hardcoded defaults for the keys with a non-zero default and no config/env
// backing (spec instance-platform-info: no new env vars).
const (
	DefaultDefaultLanguage        = "en"
	DefaultSensitiveContentPolicy = SensitiveContentPolicyHide
)

// Defaults are the config-derived fallbacks for every mutable setting: when a
// key has no override row, its accessor returns the matching Defaults field.
// cmd/api builds this from the parsed *config.Config.
type Defaults struct {
	InstanceName                string
	InstanceDescription         string
	TermsURL                    string
	PrivacyURL                  string
	ContactEmail                string
	RegistrationEnabled         bool
	RegistrationRequireApproval bool
	QuarantineNewUploads        bool
	UploadsEnabled              bool
	ImportsEnabled              bool
	LiveEnabled                 bool
	CommentsEnabled             bool
}

// spec describes one setting: its key, value kind, how to resolve its default
// from Defaults, how to validate a candidate override value (a normalised
// string — "true"/"false" for bool keys, a canonical JSON array for list keys),
// and — for enum kinds — the allowed option set.
type spec struct {
	key       string
	kind      Kind
	defString func(Defaults) string
	defBool   func(Defaults) bool
	options   []string // enum kinds only: the allowed values, in display order
	validate  func(string) error
}

// hardcoded wraps a config-independent default (the platform-information keys
// carry no config/env backing; their defaults are fixed by the spec).
func hardcoded(v string) func(Defaults) string { return func(Defaults) string { return v } }

// specs is the ordered, canonical registry of mutable settings. The order is
// stable so the GET response and tests are deterministic.
var specs = []spec{
	{key: KeyInstanceName, kind: KindString, defString: func(d Defaults) string { return d.InstanceName }, validate: validateInstanceName},
	{key: KeyInstanceDescription, kind: KindString, defString: func(d Defaults) string { return d.InstanceDescription }, validate: validateDescription},
	{key: KeyTermsURL, kind: KindString, defString: func(d Defaults) string { return d.TermsURL }, validate: validateOptionalURL},
	{key: KeyPrivacyURL, kind: KindString, defString: func(d Defaults) string { return d.PrivacyURL }, validate: validateOptionalURL},
	{key: KeyContactEmail, kind: KindString, defString: func(d Defaults) string { return d.ContactEmail }, validate: validateOptionalEmail},
	{key: KeyRegistrationEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.RegistrationEnabled }, validate: validateBool},
	{key: KeyRegistrationRequireApproval, kind: KindBool, defBool: func(d Defaults) bool { return d.RegistrationRequireApproval }, validate: validateBool},
	{key: KeyQuarantineNewUploads, kind: KindBool, defBool: func(d Defaults) bool { return d.QuarantineNewUploads }, validate: validateBool},
	{key: KeyUploadsEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.UploadsEnabled }, validate: validateBool},
	{key: KeyImportsEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.ImportsEnabled }, validate: validateBool},
	{key: KeyLiveEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.LiveEnabled }, validate: validateBool},
	{key: KeyCommentsEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.CommentsEnabled }, validate: validateBool},

	// Platform information (spec instance-platform-info). Defaults are hardcoded
	// (empty, except default_language and sensitive_content_policy) — these keys
	// have no config/env backing by design.
	{key: KeyInstanceShortDescription, kind: KindString, defString: hardcoded(""), validate: maxLen(250)},
	{key: KeyServerCountry, kind: KindString, defString: hardcoded(""), validate: maxLen(100)},
	{key: KeySupportText, kind: KindString, defString: hardcoded(""), validate: maxLen(5000)},
	{key: KeyWebsiteLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL},
	{key: KeyMastodonLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL},
	{key: KeyXLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL},
	{key: KeyBlueskyLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL},
	{key: KeyTerms, kind: KindString, defString: hardcoded(""), validate: maxLen(10000)},
	{key: KeyCodeOfConduct, kind: KindString, defString: hardcoded(""), validate: maxLen(10000)},
	{key: KeyModerationInfo, kind: KindString, defString: hardcoded(""), validate: maxLen(10000)},
	{key: KeyAdministratorInfo, kind: KindString, defString: hardcoded(""), validate: maxLen(5000)},
	{key: KeyCreationReason, kind: KindString, defString: hardcoded(""), validate: maxLen(5000)},
	{key: KeyMaintenanceLifetime, kind: KindString, defString: hardcoded(""), validate: maxLen(5000)},
	{key: KeyBusinessModel, kind: KindString, defString: hardcoded(""), validate: maxLen(5000)},
	{key: KeyHardwareInfo, kind: KindString, defString: hardcoded(""), validate: maxLen(5000)},
	{key: KeyDefaultLanguage, kind: KindString, defString: hardcoded(DefaultDefaultLanguage), validate: validateLanguageID},
	{key: KeyContactFormEnabled, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool},
	{key: KeyInstanceIsSensitive, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool},
	{key: KeySensitiveContentPolicy, kind: KindEnum, defString: hardcoded(DefaultSensitiveContentPolicy),
		options: SensitiveContentPolicyOptions, validate: enumOf(SensitiveContentPolicyOptions)},
	{key: KeyInstanceCategories, kind: KindList, defString: hardcoded(emptyList),
		validate: listOf(video.IsCategory, "category")},
	{key: KeyModeratorLanguages, kind: KindList, defString: hardcoded(emptyList),
		validate: listOf(video.IsLanguage, "language")},
}

var specByKey = func() map[string]spec {
	m := make(map[string]spec, len(specs))
	for _, sp := range specs {
		m[sp.key] = sp
	}
	return m
}()

// KindOf reports a key's value kind and whether the key is a recognised mutable
// setting. The HTTP layer uses it to shape/validate the PATCH payload.
func KindOf(key string) (Kind, bool) {
	sp, ok := specByKey[key]
	if !ok {
		return "", false
	}
	return sp.kind, true
}

// ValidationError is a per-key validation failure the HTTP layer maps to a 422
// field error (Field = Key).
type ValidationError struct {
	Key     string
	Message string
}

func (e *ValidationError) Error() string { return e.Key + ": " + e.Message }

// Repository is the data access the settings service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	ListInstanceSettings(ctx context.Context) ([]sqlcgen.InstanceSetting, error)
	UpsertInstanceSetting(ctx context.Context, arg sqlcgen.UpsertInstanceSettingParams) error
	DeleteInstanceSetting(ctx context.Context, key string) (int64, error)
}

// Service resolves effective instance settings from the config defaults + the
// DB overlay. It is safe for concurrent use.
type Service struct {
	repo     Repository
	defaults Defaults

	mu    sync.RWMutex
	cache map[string]string // key -> raw override value; absent = use default
}

// NewService builds the settings service with its config-derived defaults. Call
// Load once at boot (and it reloads itself after each write) to populate the
// override cache.
func NewService(repo Repository, defaults Defaults) *Service {
	return &Service{repo: repo, defaults: defaults, cache: map[string]string{}}
}

// Load (re)reads every override row into the in-memory cache. It is called once
// at boot and again after each successful write (cache invalidation + reload).
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.repo.ListInstanceSettings(ctx)
	if err != nil {
		return err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		if _, ok := specByKey[r.Key]; !ok {
			continue // ignore unknown/legacy keys defensively
		}
		m[r.Key] = r.Value
	}
	s.mu.Lock()
	s.cache = m
	s.mu.Unlock()
	return nil
}

// override returns the raw override value for key and whether one is set.
func (s *Service) override(key string) (string, bool) {
	s.mu.RLock()
	v, ok := s.cache[key]
	s.mu.RUnlock()
	return v, ok
}

// Bool returns the effective boolean value for a bool-kind key: the DB override
// if present, else the config default. An unknown key returns false.
func (s *Service) Bool(key string) bool {
	sp, ok := specByKey[key]
	if !ok || sp.kind != KindBool {
		return false
	}
	if raw, set := s.override(key); set {
		return raw == "true"
	}
	return sp.defBool(s.defaults)
}

// String returns the effective string value for a string- or enum-kind key:
// the DB override if present, else the config default. An unknown key (or a
// bool/list key) returns "".
func (s *Service) String(key string) string {
	sp, ok := specByKey[key]
	if !ok || (sp.kind != KindString && sp.kind != KindEnum) {
		return ""
	}
	if raw, set := s.override(key); set {
		return raw
	}
	return sp.defString(s.defaults)
}

// Strings returns the effective value for a list-kind key: the DB override
// (stored as a canonical JSON array) if present, else the default. An unknown
// or non-list key — or an unparseable stored value — returns an empty list.
// Always non-nil, so callers marshal [] rather than null.
func (s *Service) Strings(key string) []string {
	sp, ok := specByKey[key]
	if !ok || sp.kind != KindList {
		return []string{}
	}
	raw, set := s.override(key)
	if !set {
		raw = sp.defString(s.defaults)
	}
	items, err := parseList(raw)
	if err != nil {
		return []string{}
	}
	return items
}

// Effective is one setting's resolved state for the admin GET response: the
// effective value (string, bool, or []string per kind), the config default,
// whether the DB currently overrides it, and — for enum kinds — the allowed
// option set.
type Effective struct {
	Key        string
	Kind       Kind
	Value      any // string (KindString/KindEnum), bool (KindBool), or []string (KindList)
	Default    any // same type as Value
	Overridden bool
	Options    []string // enum kinds only; nil otherwise
}

// Snapshot returns every mutable setting's effective state, in the canonical
// registry order (deterministic for clients + tests).
func (s *Service) Snapshot() []Effective {
	out := make([]Effective, 0, len(specs))
	for _, sp := range specs {
		_, overridden := s.override(sp.key)
		e := Effective{Key: sp.key, Kind: sp.kind, Overridden: overridden, Options: sp.options}
		switch sp.kind {
		case KindBool:
			e.Value = s.Bool(sp.key)
			e.Default = sp.defBool(s.defaults)
		case KindList:
			e.Value = s.Strings(sp.key)
			def, err := parseList(sp.defString(s.defaults))
			if err != nil {
				def = []string{}
			}
			e.Default = def
		default:
			e.Value = s.String(sp.key)
			e.Default = sp.defString(s.defaults)
		}
		out = append(out, e)
	}
	return out
}

// Update is one setting change: Value is the normalised string to store
// ("true"/"false" for bool keys), or Delete=true to remove the override and
// fall back to the config default.
type Update struct {
	Value  string
	Delete bool
}

// Apply validates and applies a batch of setting changes, then reloads the
// cache so the overlay reflects them. Validation runs across the whole batch
// first (all-or-nothing on validation), returning a *ValidationError for the
// first offending key; a persisted write that fails surfaces its raw error. An
// empty batch is a no-op. updatedBy is the acting admin (stamped on each row).
func (s *Service) Apply(ctx context.Context, updates map[string]Update, updatedBy uuid.UUID) error {
	if len(updates) == 0 {
		return nil
	}
	// Validate the entire batch before persisting anything.
	for key, u := range updates {
		sp, ok := specByKey[key]
		if !ok {
			return &ValidationError{Key: key, Message: "unknown setting"}
		}
		if u.Delete {
			continue
		}
		if err := sp.validate(u.Value); err != nil {
			return &ValidationError{Key: key, Message: err.Error()}
		}
	}
	// Persist. updated_by survives the admin's later deletion (ON DELETE SET NULL).
	by := pgtype.UUID{Bytes: updatedBy, Valid: updatedBy != uuid.Nil}
	for key, u := range updates {
		if u.Delete {
			if _, err := s.repo.DeleteInstanceSetting(ctx, key); err != nil {
				return err
			}
			continue
		}
		if err := s.repo.UpsertInstanceSetting(ctx, sqlcgen.UpsertInstanceSettingParams{
			Key:       key,
			Value:     u.Value,
			UpdatedBy: by,
		}); err != nil {
			return err
		}
	}
	return s.Load(ctx)
}

// --- value validators (operate on the normalised string form) ---

func validateBool(v string) error {
	if v != "true" && v != "false" {
		return errors.New("must be a boolean")
	}
	return nil
}

func validateInstanceName(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("must not be empty")
	}
	if len(v) > 100 {
		return errors.New("must be at most 100 characters")
	}
	return nil
}

// validateDescription bounds the long (markdown) instance description.
func validateDescription(v string) error { return maxLen(10000)(v) }

// maxLen returns a validator accepting any string up to n characters (bytes,
// like the other length limits here). Empty is always fine — it clears the
// text back to nothing.
func maxLen(n int) func(string) error {
	msg := fmt.Sprintf("must be at most %d characters", n)
	return func(v string) error {
		if len(v) > n {
			return errors.New(msg)
		}
		return nil
	}
}

// validateLanguageID requires a known taxonomy language id (the GET
// /videos/config source). Clearing back to the default is done via null, not
// an empty string, so empty is invalid here.
func validateLanguageID(v string) error {
	if !video.IsLanguage(v) {
		return errors.New("must be a known language id")
	}
	return nil
}

// enumOf returns a validator accepting exactly one of options.
func enumOf(options []string) func(string) error {
	msg := "must be one of " + strings.Join(options, ", ")
	return func(v string) error {
		for _, o := range options {
			if v == o {
				return nil
			}
		}
		return errors.New(msg)
	}
}

// listOf returns a validator for a list-kind value: the normalised string must
// be a canonical JSON array of strings, each item passing the taxonomy check.
func listOf(valid func(string) bool, itemName string) func(string) error {
	return func(v string) error {
		items, err := parseList(v)
		if err != nil {
			return errors.New("must be an array of strings")
		}
		for _, it := range items {
			if !valid(it) {
				return fmt.Errorf("unknown %s %q", itemName, it)
			}
		}
		return nil
	}
}

// emptyList is the canonical stored form of an empty list value.
const emptyList = "[]"

// parseList decodes a stored list value (a JSON array of strings). The result
// is always non-nil on success.
func parseList(raw string) ([]string, error) {
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	if items == nil {
		items = []string{}
	}
	return items, nil
}

// FormatList normalises a list value to its canonical stored form (a compact
// JSON array of strings). Exported for the HTTP layer, which shapes the PATCH
// payload into Updates.
func FormatList(items []string) string {
	if items == nil {
		items = []string{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return emptyList // unreachable for []string; defensive
	}
	return string(b)
}

// validateOptionalURL accepts the empty string (clears the link) or a bounded
// absolute http(s) URL with a host.
func validateOptionalURL(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if len(v) > 2000 {
		return errors.New("must be at most 2000 characters")
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an absolute http(s) URL")
	}
	return nil
}

// validateOptionalEmail accepts the empty string (clears the contact) or a
// minimally well-formed, bounded address.
func validateOptionalEmail(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if len(v) > 254 {
		return errors.New("must be at most 254 characters")
	}
	at := strings.IndexByte(v, '@')
	if at <= 0 || at == len(v)-1 || strings.ContainsAny(v, " \t\r\n") || strings.Contains(v[at+1:], "@") {
		return errors.New("must be a valid email address")
	}
	return nil
}

// FormatBool normalises a Go bool to the stored string form. Exported for the
// HTTP layer, which shapes the PATCH payload into Updates.
func FormatBool(b bool) string { return strconv.FormatBool(b) }
