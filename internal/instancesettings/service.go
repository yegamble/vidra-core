// Package instancesettings implements the DB-backed dynamic instance-settings
// overlay (fix_plan P10 admin instance configuration + feature toggles).
//
// A defined MUTABLE subset of instance settings — the instance name/description
// and legal-contact metadata, the registration gates, the upload-quarantine
// gate, and the uploads/imports/live/comments/downloads feature toggles — can be changed
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
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/media"
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
	KeyDownloadsEnabled            = "downloads_enabled"

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

	// Operational-limit keys (config-parity slice). Group-A overlays on already
	// enforced env knobs: each default comes from config and each value is read
	// on the request/job hot path, so an admin can retune caps without a restart.
	KeyDefaultUserQuotaBytes          = "default_user_quota_bytes"
	KeyUploadMaxSizeBytes             = "upload_max_size_bytes"
	KeyUploadMaxActiveSessionsPerUser = "upload_max_active_sessions_per_user"
	KeyImportMaxHeight                = "import_max_height"

	// Core-config-surface keys (config-parity W1). Registered NOW with their
	// kinds/defaults/validators so GET /instance can expose effective values;
	// ENFORCEMENT/consumption lands in later waves (W3 broadcast banner, W4
	// branding/social, W5 browse/landing/player defaults, W6 customization +
	// email, W9 publish defaults). All are pure overlay rows with HARDCODED
	// defaults — no config/env backing by design.
	KeyBroadcastEnabled     = "broadcast_enabled"
	KeyBroadcastMessage     = "broadcast_message" // markdown body (short; document-scale content lives in instance_documents)
	KeyBroadcastLevel       = "broadcast_level"
	KeyBroadcastDismissable = "broadcast_dismissable"

	KeyDefaultFeedSort                  = "default_feed_sort"
	KeyDefaultFeedScope                 = "default_feed_scope"
	KeyDefaultLandingPage               = "default_landing_page"
	KeyDefaultTheme                     = "default_theme"
	KeyDefaultPlayerAutoplay            = "default_player_autoplay"
	KeyMiniaturePreferAuthorDisplayName = "miniature_prefer_author_display_name"

	KeyDefaultVideoPrivacy    = "default_video_privacy"
	KeyDefaultVideoLicence    = "default_video_licence" // 0 = no default licence (mirrors PeerTube null)
	KeyDefaultCommentPolicy   = "default_comment_policy"
	KeyDefaultDownloadEnabled = "default_download_enabled"

	KeyThemePrimaryColor         = "theme_primary_color" // "#rrggbb" or "" (no override)
	KeySocialMetaTwitterUsername = "social_meta_twitter_username"
	KeyHeaderHideInstanceName    = "header_hide_instance_name"
	KeyEmailSubjectPrefix        = "email_subject_prefix" // supports {instance_name} substitution at the mail seam (W6)
	KeyEmailBodySignature        = "email_body_signature"

	// Shipped-feature toggle batch (config-parity W8): runtime knobs over
	// features that already exist, applied through provider-func seams (or
	// handler gates) so an admin can flip them without a restart. Boot
	// capabilities stay env-only (yt-dlp binary/proxy, WHISPER_ENDPOINT,
	// channel-sync cadence): a runtime toggle can never conjure a dependency
	// the deployment lacks — the effective value is settingAND(boot).
	KeyImportHTTPEnabled         = "import_http_enabled"          // yt-dlp platform-URL import path; imports_enabled stays the master
	KeyChannelSyncEnabled        = "channel_sync_enabled"         // channel auto-sync create + ticker pickup
	KeyChannelSyncMaxPerUser     = "channel_sync_max_per_user"    // 0 = unlimited
	KeyStoryboardsEnabled        = "storyboards_enabled"          // seek-preview sprite generation at publish
	KeyTranscriptionEnabled      = "transcription_enabled"        // Whisper auto-captions; effective only with WHISPER_ENDPOINT
	KeyUserImportEnabled         = "user_import_enabled"          // POST /me/import
	KeyUserExportEnabled         = "user_export_enabled"          // POST /me/export
	KeyUserExportExpirationHours = "user_export_expiration_hours" // 0 = archives never expire
	KeyUserExportMaxQuotaBytes   = "user_export_max_quota_bytes"  // refuse export gen above this usage; 0 = unlimited
	KeyMaxChannelsPerUser        = "max_channels_per_user"        // 0 = unlimited

	// VOD transcoding runtime knobs & worker pools (config-parity W10). Every
	// key is read through a provider-func seam at job/request time — never at
	// worker construction (the boot-baked-worker gotcha) — so changes apply
	// without a restart. transcoding_enabled gates job enqueue AND worker
	// pickup; the boot capability (ffmpeg/ffprobe on PATH) stays env-side and
	// is ANDed in at the seams.
	KeyTranscodingEnabled            = "transcoding_enabled"             // default from TRANSCODING_ENABLED
	KeyTranscodingResolutions        = "transcoding_resolutions"         // ladder rung heights (canonical set; no 0p audio-only rung in v1)
	KeyTranscodingMaxFPS             = "transcoding_max_fps"             // 0 = no cap, else 24..240 (applied only when the source rate exceeds it)
	KeyTranscodingThreads            = "transcoding_threads"             // 0 = ffmpeg default, else 1..64 (per-job -threads)
	KeyTranscodingConcurrency        = "transcoding_concurrency"         // parallel transcode jobs per tick, 1..16
	KeyImportJobsConcurrency         = "import_jobs_concurrency"         // parallel URL-import jobs per tick, 1..16
	KeyTranscodingOriginalResolution = "transcoding_original_resolution" // extra HLS rung at source size above the ladder max
	// Upload container policy (rides the transcoding wave; PT groups it under
	// transcoding too). Default ON — vidra's shipped allow-list already
	// included the extended set (documented deviation from PT's off default).
	KeyUploadAdditionalExtensionsEnabled = "upload_additional_extensions_enabled"

	// Live streaming enforcement knobs (config-parity W11). Every key has a
	// real enforcement point today: the replay pipeline gate, the live-create
	// default seed, the secret-handled nginx-rtmp publish-callback caps, and
	// the duration watchdog. The unbuilt live-transcoding cluster (ladder/DVR/
	// latency) is deliberately NOT registered — dormant keys mislead admins
	// (architecture note 7; see the parity ledger).
	KeyLiveAllowReplay       = "live_allow_replay"        // master replay gate; off = no replay produced regardless of per-stream save_replay
	KeyLiveDefaultSaveReplay = "live_default_save_replay" // seeds the live-create replay flag when the client omits it; no effect while allow_replay is off (PT parity)
	KeyLiveMaxInstanceLives  = "live_max_instance_lives"  // simultaneous live sessions across the instance; 0 = unlimited
	KeyLiveMaxUserLives      = "live_max_user_lives"      // simultaneous live sessions per user; 0 = unlimited
	KeyLiveMaxDurationSecs   = "live_max_duration_secs"   // watchdog force-closes sessions over this; 0 = no limit

	// Federation policy gates (config-parity W12). PROTOCOL LABELING: every one
	// of these keys governs the ActivityPub INBOX (federation.HandleInbox) —
	// vidra's only inbound federation surface. The ATProto integration
	// (ATPROTO_ENABLED) is OUTBOUND cross-posting only and has no inbound path,
	// so these keys are explicitly out of scope for it. None are retroactive:
	// flipping a gate never touches already-ingested comments or existing
	// followers. Defaults preserve the shipped behaviour (comments ingested,
	// channel follows auto-accepted, no approval queue, no follow-back).
	KeyFederationAcceptRemoteComments  = "federation_accept_remote_comments"  // off = inbound Create/Update{Note} dropped after the blocked-domain check
	KeyFederationAllowChannelFollowers = "federation_allow_channel_followers" // off = inbound channel Follows answered with a Reject
	KeyFederationFollowerApproval      = "federation_follower_approval"       // on = new channel Follows wait PENDING in the admin queue (Accept sent on approval)
	KeyFederationAutoFollowBack        = "federation_auto_follow_back"        // on = an accepted channel Follow triggers a follow-back FROM THAT CHANNEL'S ACTOR (vidra has no instance actor)

	// Remote-URI search gates (config-parity W13). A URI- or handle-shaped
	// search query ("https://…", "@user@host", "user@host") is resolved through
	// the EXISTING federation resolution machinery (WebFinger/actor/object
	// fetches, always SSRF-guarded) and the resolved remote video/channel/
	// account rides the search response as a typed `remote` item. The two keys
	// gate that resolution per auth state; both are moot while federation
	// itself is off (the /instance search block reports the EFFECTIVE values).
	// Anonymous defaults FALSE, matching PeerTube — anonymous traffic is the
	// SSRF/abuse surface.
	KeySearchRemoteURIUsers     = "search_remote_uri_users"
	KeySearchRemoteURIAnonymous = "search_remote_uri_anonymous"

	// Sign-up & new-user keys (config-parity W7).
	//
	// registration_require_email_verification holds new registrations behind a
	// verification link; EFFECTIVE only while an outbound mail path exists
	// (features.mail — a runtime toggle can never conjure a missing mailer).
	// Accounts created while the gate is off are never retroactively locked
	// (the pending flag is stamped at creation — the grandfather clause).
	// registration_user_limit refuses signups once the instance holds that
	// many accounts (0 = unlimited; the count is approximate under concurrent
	// signups by design). registration_minimum_age (0 = disabled) requires the
	// PeerTube-style signup ATTESTATION checkbox — no birthdate is collected.
	// default_user_daily_quota_bytes caps bytes uploaded per ROLLING 24h
	// window (0 = unlimited; internal/quota keeps the ledger).
	// new_user_history_enabled seeds the per-user watch-history preference at
	// account creation (existing accounts are untouched).
	KeyRegistrationRequireEmailVerification = "registration_require_email_verification"
	KeyRegistrationUserLimit                = "registration_user_limit"
	KeyRegistrationMinimumAge               = "registration_minimum_age"
	KeyDefaultUserDailyQuotaBytes           = "default_user_daily_quota_bytes"
	KeyNewUserHistoryEnabled                = "new_user_history_enabled"
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
	// KindInt is a bounded integer (operational limits/quotas). The TEXT value
	// column stores the canonical decimal string; the API value is a JSON number.
	// Values are capped at maxSafeInt so they round-trip exactly through a JS
	// double.
	KindInt Kind = "int"
)

// maxSafeInt is JavaScript's Number.MAX_SAFE_INTEGER (2^53 - 1): the largest
// integer a client can hold without silent precision loss. Every int-kind value
// is bounded to it so GET/PATCH round-trips are exact.
const maxSafeInt int64 = 1<<53 - 1

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

// Enum option sets for the core-config-surface keys (config-parity W1), in
// canonical display order. Exposed so the admin view and OpenAPI stay in sync.
var (
	// BroadcastLevelOptions styles the W3 broadcast banner (PT parity).
	BroadcastLevelOptions = []string{"info", "warning", "error"}
	// FeedSortOptions is vidra's 3-way sort universe (deliberate deviation from
	// PeerTube's seven sort strings — vidra's feed supports exactly these).
	FeedSortOptions = []string{"recent", "popular", "trending"}
	// FeedScopeOptions: local (this instance) or all (adds federated content).
	FeedScopeOptions = []string{"local", "all"}
	// LandingPageOptions: what "/" shows anonymous visitors. "home" is the
	// admin-authored homepage document and only takes effect once one is set
	// (W6); the frontend falls back to home-recent until then.
	LandingPageOptions = []string{"home-recent", "trending", "local", "home"}
	// ThemeOptions over the shipped light-dark() token architecture (vidra has
	// no plugin themes — deliberate deviation).
	ThemeOptions = []string{"system", "light", "dark"}
	// VideoPrivacyOptions for the publish default. "password" is deliberately
	// absent: a password-protected video needs a password, so it cannot be a
	// seeded default.
	VideoPrivacyOptions = []string{"public", "unlisted", "private"}
	// CommentPolicyOptions for the per-video comment-policy default (W9 builds
	// the per-video field; requires_approval is deliberately deferred).
	CommentPolicyOptions = []string{"enabled", "disabled"}
)

// Default values for the core-config-surface keys.
const (
	DefaultBroadcastLevel = "info"
	DefaultFeedSort       = "recent"
	DefaultFeedScope      = "local"
	DefaultLandingPage    = "home-recent"
	DefaultTheme          = "system"
	// DefaultVideoPrivacy keeps the pre-W9 shipped behaviour (an omitted
	// privacy on create means private) — silently loosening third-party API
	// clients' drafts to public is not acceptable as a side effect of adding
	// the knob. Admins who want PeerTube's public-by-default set it here.
	DefaultVideoPrivacy   = "private"
	DefaultCommentPolicy  = "enabled"
	defaultPlayerAutoplay = true

	// DefaultUserExportExpirationHours is how long a finished account-export
	// archive stays downloadable (W8; mirrors account.ExportTTL's 7 days —
	// a deliberate deviation from PeerTube's 2). 0 = never expires.
	DefaultUserExportExpirationHours = 168
)

// Admin-page identifiers for the registry's page/section metadata (config-
// parity W1): every setting is placed on one of the admin-config IA pages so
// the metadata-driven admin UI auto-renders new keys into the right place.
const (
	PageGeneral       = "general"
	PageVOD           = "vod"
	PageLive          = "live"
	PageFederation    = "federation"
	PageCustomization = "customization"
	PageHomepage      = "homepage"
	PageAdvanced      = "advanced"
)

// Pages is the canonical page order (the admin UI's left rail).
var Pages = []string{PageGeneral, PageVOD, PageLive, PageFederation, PageCustomization, PageHomepage, PageAdvanced}

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

	// Operational limits (int kinds). Defaults come from the parsed config: the
	// per-user storage quota (bytes; 0 = unlimited), the max single-upload size
	// (bytes; 0 = no cap), the max concurrent active upload sessions per user
	// (0 = unlimited), and the URL-import resolution cap (0 = no cap).
	DefaultUserQuotaBytes          int64
	UploadMaxSizeBytes             int64
	UploadMaxActiveSessionsPerUser int64
	ImportMaxHeight                int64

	// Shipped-feature toggles (config-parity W8) whose defaults come from
	// existing env knobs. ChannelSyncEnabled mirrors CHANNEL_SYNC_ENABLED and
	// ChannelSyncMaxPerUser CHANNEL_SYNC_MAX_PER_USER (0 = unlimited);
	// TranscriptionEnabled mirrors WHISPER_ENABLED (effective only while
	// WHISPER_ENDPOINT is configured — the boot capability stays env-only).
	ChannelSyncEnabled    bool
	ChannelSyncMaxPerUser int64
	TranscriptionEnabled  bool

	// TranscodingEnabled mirrors TRANSCODING_ENABLED (config-parity W10): the
	// runtime master toggle's default. The boot capability (ffmpeg/ffprobe on
	// PATH) stays env-side — effective availability is settingAND(boot).
	TranscodingEnabled bool
}

// spec describes one setting: its key, value kind, how to resolve its default
// from Defaults, how to validate a candidate override value (a normalised
// string — "true"/"false" for bool keys, a canonical JSON array for list keys),
// for enum kinds the allowed option set, and the admin-IA placement (page +
// in-page section) the metadata-driven admin UI renders it into.
type spec struct {
	key       string
	kind      Kind
	defString func(Defaults) string
	defBool   func(Defaults) bool
	defInt    func(Defaults) int64 // int kinds only
	options   []string             // enum kinds only: the allowed values, in display order
	validate  func(string) error
	page      string // one of the Page* identifiers
	section   string // in-page section label (stable, snake_case)
}

// hardcoded wraps a config-independent default (the platform-information keys
// carry no config/env backing; their defaults are fixed by the spec).
func hardcoded(v string) func(Defaults) string { return func(Defaults) string { return v } }

// specs is the ordered, canonical registry of mutable settings. The order is
// stable so the GET response and tests are deterministic. Every entry carries
// its admin-IA placement (page + section, config-parity W1) so the
// metadata-driven admin UI auto-renders keys into the right page.
var specs = []spec{
	{key: KeyInstanceName, kind: KindString, defString: func(d Defaults) string { return d.InstanceName }, validate: validateInstanceName,
		page: PageGeneral, section: "identity"},
	{key: KeyInstanceDescription, kind: KindString, defString: func(d Defaults) string { return d.InstanceDescription }, validate: validateDescription,
		page: PageGeneral, section: "identity"},
	{key: KeyTermsURL, kind: KindString, defString: func(d Defaults) string { return d.TermsURL }, validate: validateOptionalURL,
		page: PageGeneral, section: "about"},
	{key: KeyPrivacyURL, kind: KindString, defString: func(d Defaults) string { return d.PrivacyURL }, validate: validateOptionalURL,
		page: PageGeneral, section: "about"},
	{key: KeyContactEmail, kind: KindString, defString: func(d Defaults) string { return d.ContactEmail }, validate: validateOptionalEmail,
		page: PageGeneral, section: "contact"},
	{key: KeyRegistrationEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.RegistrationEnabled }, validate: validateBool,
		page: PageGeneral, section: "signup"},
	{key: KeyRegistrationRequireApproval, kind: KindBool, defBool: func(d Defaults) bool { return d.RegistrationRequireApproval }, validate: validateBool,
		page: PageGeneral, section: "signup"},
	{key: KeyQuarantineNewUploads, kind: KindBool, defBool: func(d Defaults) bool { return d.QuarantineNewUploads }, validate: validateBool,
		page: PageGeneral, section: "moderation"},
	{key: KeyUploadsEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.UploadsEnabled }, validate: validateBool,
		page: PageVOD, section: "uploads"},
	{key: KeyImportsEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.ImportsEnabled }, validate: validateBool,
		page: PageVOD, section: "imports"},
	{key: KeyLiveEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.LiveEnabled }, validate: validateBool,
		page: PageLive, section: "streaming"},
	{key: KeyCommentsEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.CommentsEnabled }, validate: validateBool,
		page: PageVOD, section: "comments"},
	// Downloads are enabled by default and intentionally have no env/config
	// backing: the runtime admin setting is the single operator control.
	{key: KeyDownloadsEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageVOD, section: "downloads"},

	// Platform information (spec instance-platform-info). Defaults are hardcoded
	// (empty, except default_language and sensitive_content_policy) — these keys
	// have no config/env backing by design.
	{key: KeyInstanceShortDescription, kind: KindString, defString: hardcoded(""), validate: maxLen(250),
		page: PageGeneral, section: "identity"},
	{key: KeyServerCountry, kind: KindString, defString: hardcoded(""), validate: maxLen(100),
		page: PageGeneral, section: "identity"},
	{key: KeySupportText, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageGeneral, section: "about"},
	{key: KeyWebsiteLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL,
		page: PageGeneral, section: "social"},
	{key: KeyMastodonLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL,
		page: PageGeneral, section: "social"},
	{key: KeyXLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL,
		page: PageGeneral, section: "social"},
	{key: KeyBlueskyLink, kind: KindString, defString: hardcoded(""), validate: validateOptionalURL,
		page: PageGeneral, section: "social"},
	{key: KeyTerms, kind: KindString, defString: hardcoded(""), validate: maxLen(10000),
		page: PageGeneral, section: "about"},
	{key: KeyCodeOfConduct, kind: KindString, defString: hardcoded(""), validate: maxLen(10000),
		page: PageGeneral, section: "about"},
	{key: KeyModerationInfo, kind: KindString, defString: hardcoded(""), validate: maxLen(10000),
		page: PageGeneral, section: "about"},
	{key: KeyAdministratorInfo, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageGeneral, section: "about"},
	{key: KeyCreationReason, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageGeneral, section: "about"},
	{key: KeyMaintenanceLifetime, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageGeneral, section: "about"},
	{key: KeyBusinessModel, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageGeneral, section: "about"},
	{key: KeyHardwareInfo, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageGeneral, section: "about"},
	{key: KeyDefaultLanguage, kind: KindString, defString: hardcoded(DefaultDefaultLanguage), validate: validateLanguageID,
		page: PageGeneral, section: "identity"},
	{key: KeyContactFormEnabled, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageGeneral, section: "contact"},
	{key: KeyInstanceIsSensitive, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageGeneral, section: "moderation"},
	{key: KeySensitiveContentPolicy, kind: KindEnum, defString: hardcoded(DefaultSensitiveContentPolicy),
		options: SensitiveContentPolicyOptions, validate: enumOf(SensitiveContentPolicyOptions),
		page: PageGeneral, section: "moderation"},
	{key: KeyInstanceCategories, kind: KindList, defString: hardcoded(emptyList),
		validate: listOf(video.IsCategory, "category"),
		page:     PageGeneral, section: "identity"},
	{key: KeyModeratorLanguages, kind: KindList, defString: hardcoded(emptyList),
		validate: listOf(video.IsLanguage, "language"),
		page:     PageGeneral, section: "identity"},

	// Operational limits (config-parity slice). Bounds mirror the config
	// validators exactly (config.go) so a runtime override can never set a value
	// the boot-time config would have rejected.
	{key: KeyDefaultUserQuotaBytes, kind: KindInt,
		defInt: func(d Defaults) int64 { return d.DefaultUserQuotaBytes }, validate: intMin(0),
		page: PageVOD, section: "uploads"},
	{key: KeyUploadMaxSizeBytes, kind: KindInt,
		defInt: func(d Defaults) int64 { return d.UploadMaxSizeBytes }, validate: intZeroOrRange(1<<20, maxSafeInt),
		page: PageVOD, section: "uploads"},
	{key: KeyUploadMaxActiveSessionsPerUser, kind: KindInt,
		defInt: func(d Defaults) int64 { return d.UploadMaxActiveSessionsPerUser }, validate: intRange(0, 10000),
		page: PageVOD, section: "uploads"},
	{key: KeyImportMaxHeight, kind: KindInt,
		defInt: func(d Defaults) int64 { return d.ImportMaxHeight }, validate: intZeroOrRange(144, 4320),
		page: PageVOD, section: "imports"},

	// Core config surface (config-parity W1). Registered now so GET /instance
	// exposes effective values; enforcement/consumption lands in later waves.
	// Broadcast banner (W3 consumes).
	{key: KeyBroadcastEnabled, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageGeneral, section: "broadcast"},
	{key: KeyBroadcastMessage, kind: KindString, defString: hardcoded(""), validate: maxLen(10000),
		page: PageGeneral, section: "broadcast"},
	{key: KeyBroadcastLevel, kind: KindEnum, defString: hardcoded(DefaultBroadcastLevel),
		options: BroadcastLevelOptions, validate: enumOf(BroadcastLevelOptions),
		page: PageGeneral, section: "broadcast"},
	{key: KeyBroadcastDismissable, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageGeneral, section: "broadcast"},
	// Landing & browse defaults (W5 consumes).
	{key: KeyDefaultFeedSort, kind: KindEnum, defString: hardcoded(DefaultFeedSort),
		options: FeedSortOptions, validate: enumOf(FeedSortOptions),
		page: PageGeneral, section: "browse"},
	{key: KeyDefaultFeedScope, kind: KindEnum, defString: hardcoded(DefaultFeedScope),
		options: FeedScopeOptions, validate: enumOf(FeedScopeOptions),
		page: PageGeneral, section: "browse"},
	{key: KeyDefaultLandingPage, kind: KindEnum, defString: hardcoded(DefaultLandingPage),
		options: LandingPageOptions, validate: enumOf(LandingPageOptions),
		page: PageGeneral, section: "browse"},
	{key: KeyMiniaturePreferAuthorDisplayName, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageGeneral, section: "browse"},
	// Publish defaults (W9 builds the per-video fields + seeding).
	{key: KeyDefaultVideoPrivacy, kind: KindEnum, defString: hardcoded(DefaultVideoPrivacy),
		options: VideoPrivacyOptions, validate: enumOf(VideoPrivacyOptions),
		page: PageVOD, section: "publish_defaults"},
	{key: KeyDefaultVideoLicence, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: validateLicenceID,
		page: PageVOD, section: "publish_defaults"},
	{key: KeyDefaultCommentPolicy, kind: KindEnum, defString: hardcoded(DefaultCommentPolicy),
		options: CommentPolicyOptions, validate: enumOf(CommentPolicyOptions),
		page: PageVOD, section: "publish_defaults"},
	// Default ON, preserving the shipped behaviour (every video downloadable
	// while the instance downloads_enabled gate is on) and matching PeerTube's
	// defaults.publish default. W9 corrects the placeholder false registered in
	// W1 before enforcement existed.
	{key: KeyDefaultDownloadEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageVOD, section: "publish_defaults"},
	// Customization: theme, header, player, email (W4/W5/W6 consume).
	{key: KeyDefaultTheme, kind: KindEnum, defString: hardcoded(DefaultTheme),
		options: ThemeOptions, validate: enumOf(ThemeOptions),
		page: PageCustomization, section: "theme"},
	{key: KeyThemePrimaryColor, kind: KindString, defString: hardcoded(""), validate: validateOptionalHexColor,
		page: PageCustomization, section: "theme"},
	{key: KeyHeaderHideInstanceName, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageCustomization, section: "header"},
	{key: KeyDefaultPlayerAutoplay, kind: KindBool, defBool: func(Defaults) bool { return defaultPlayerAutoplay }, validate: validateBool,
		page: PageCustomization, section: "player"},
	{key: KeyEmailSubjectPrefix, kind: KindString, defString: hardcoded(""), validate: maxLen(128),
		page: PageCustomization, section: "email"},
	{key: KeyEmailBodySignature, kind: KindString, defString: hardcoded(""), validate: maxLen(5000),
		page: PageCustomization, section: "email"},
	// Social link-card metadata (W4 consumes; distinct from the x_link
	// About-page key — twitter:site wants a handle, not a profile URL).
	{key: KeySocialMetaTwitterUsername, kind: KindString, defString: hardcoded(""), validate: validateOptionalTwitterUsername,
		page: PageGeneral, section: "social"},

	// Shipped-feature toggle batch (config-parity W8). Every key gates a
	// feature that already exists; enforcement is a provider-func seam or a
	// handler 403 feature_disabled gate at the documented apply point.
	// The yt-dlp platform-URL import path (imports_enabled stays the master
	// switch above it). Default ON: with no override the path is governed by
	// the boot capability alone (YTDLP_IMPORT_ENABLED wiring the resolver).
	{key: KeyImportHTTPEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageVOD, section: "imports"},
	{key: KeyChannelSyncEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.ChannelSyncEnabled }, validate: validateBool,
		page: PageVOD, section: "imports"},
	{key: KeyChannelSyncMaxPerUser, kind: KindInt,
		defInt: func(d Defaults) int64 { return d.ChannelSyncMaxPerUser }, validate: intRange(0, 10000),
		page: PageVOD, section: "imports"},
	// Storyboards default ON and have no env backing: the runtime setting is
	// the single operator control (generation additionally needs ffmpeg).
	{key: KeyStoryboardsEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageVOD, section: "storyboards"},
	{key: KeyTranscriptionEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.TranscriptionEnabled }, validate: validateBool,
		page: PageVOD, section: "transcription"},
	// User data portability (shipped internal/account flows). Both default ON
	// with no env backing — the runtime settings are the operator controls.
	{key: KeyUserImportEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageAdvanced, section: "user_data"},
	{key: KeyUserExportEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageAdvanced, section: "user_data"},
	{key: KeyUserExportExpirationHours, kind: KindInt,
		defInt: func(Defaults) int64 { return DefaultUserExportExpirationHours }, validate: intZeroOrRange(1, 8760),
		page: PageAdvanced, section: "user_data"},
	{key: KeyUserExportMaxQuotaBytes, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intMin(0),
		page: PageAdvanced, section: "user_data"},
	// Channel cap (distinct from channel_sync_max_per_user — this caps
	// CHANNELS, that caps sync bindings). 0 = unlimited, the shipped default.
	{key: KeyMaxChannelsPerUser, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intRange(0, 10000),
		page: PageGeneral, section: "channels"},

	// VOD transcoding runtime knobs & worker pools (config-parity W10).
	{key: KeyTranscodingEnabled, kind: KindBool, defBool: func(d Defaults) bool { return d.TranscodingEnabled }, validate: validateBool,
		page: PageVOD, section: "transcoding"},
	{key: KeyTranscodingResolutions, kind: KindList, defString: hardcoded(defaultTranscodingResolutions),
		validate: validateTranscodingResolutions,
		page:     PageVOD, section: "transcoding"},
	{key: KeyTranscodingMaxFPS, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intZeroOrRange(24, 240),
		page: PageVOD, section: "transcoding"},
	{key: KeyTranscodingThreads, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intZeroOrRange(1, 64),
		page: PageVOD, section: "transcoding"},
	{key: KeyTranscodingConcurrency, kind: KindInt,
		defInt: func(Defaults) int64 { return 1 }, validate: intRange(1, 16),
		page: PageVOD, section: "transcoding"},
	{key: KeyTranscodingOriginalResolution, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageVOD, section: "transcoding"},
	{key: KeyImportJobsConcurrency, kind: KindInt,
		defInt: func(Defaults) int64 { return 1 }, validate: intRange(1, 16),
		page: PageVOD, section: "imports"},
	// Default ON: the shipped allow-list already included the extended
	// container set (documented deviation from PeerTube's off default).
	{key: KeyUploadAdditionalExtensionsEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageVOD, section: "uploads"},

	// Live streaming enforcement knobs (config-parity W11). None have env
	// backing: the runtime settings are the operator controls. Replay defaults
	// ON (the shipped behaviour — replay has always been available when a
	// stream opts in); the save-replay default and every limit default to the
	// shipped "off"/unlimited state.
	{key: KeyLiveAllowReplay, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageLive, section: "replay"},
	{key: KeyLiveDefaultSaveReplay, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageLive, section: "replay"},
	{key: KeyLiveMaxInstanceLives, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intRange(0, 10000),
		page: PageLive, section: "limits"},
	{key: KeyLiveMaxUserLives, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intRange(0, 10000),
		page: PageLive, section: "limits"},
	// 0 = no limit; otherwise 1 minute .. 30 days (the watchdog sweeps every
	// ~30s, so sub-minute limits would be noise).
	{key: KeyLiveMaxDurationSecs, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intZeroOrRange(60, 2592000),
		page: PageLive, section: "limits"},

	// Federation policy gates (config-parity W12). None have env backing: the
	// runtime settings are the operator controls, defaults keep the shipped
	// behaviour. All gate the ActivityPub inbox only (ATProto is outbound-only;
	// see the key-constant comments).
	{key: KeyFederationAcceptRemoteComments, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageFederation, section: "comments"},
	{key: KeyFederationAllowChannelFollowers, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageFederation, section: "followers"},
	{key: KeyFederationFollowerApproval, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageFederation, section: "followers"},
	{key: KeyFederationAutoFollowBack, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageFederation, section: "followers"},

	// Remote-URI search gates (config-parity W13). No env backing: the runtime
	// settings are the operator controls. Logged-in resolution defaults ON
	// (PT parity), anonymous defaults OFF (SSRF/abuse surface).
	{key: KeySearchRemoteURIUsers, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageFederation, section: "search"},
	{key: KeySearchRemoteURIAnonymous, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageFederation, section: "search"},

	// Sign-up & new users (config-parity W7). None have env backing: the
	// runtime settings are the operator controls, defaults keep the shipped
	// behaviour (no verification hold, no user limit, no age attestation, no
	// daily quota, history on for new accounts).
	{key: KeyRegistrationRequireEmailVerification, kind: KindBool, defBool: func(Defaults) bool { return false }, validate: validateBool,
		page: PageGeneral, section: "signup"},
	{key: KeyRegistrationUserLimit, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intMin(0),
		page: PageGeneral, section: "signup"},
	// 0 = disabled; otherwise a plausible human age (PT's own default is 16).
	{key: KeyRegistrationMinimumAge, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intZeroOrRange(1, 150),
		page: PageGeneral, section: "signup"},
	{key: KeyNewUserHistoryEnabled, kind: KindBool, defBool: func(Defaults) bool { return true }, validate: validateBool,
		page: PageGeneral, section: "signup"},
	// The daily quota lives with the other storage limits on the VOD page.
	{key: KeyDefaultUserDailyQuotaBytes, kind: KindInt,
		defInt: func(Defaults) int64 { return 0 }, validate: intMin(0),
		page: PageVOD, section: "uploads"},
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

// Int returns the effective value for an int-kind key: the DB override if
// present and parseable, else the config default. An unknown or non-int key
// returns 0. A stored value that fails to parse (only reachable via a manual DB
// edit — Apply validates every write) falls back to the default, mirroring the
// defensive posture of Strings.
func (s *Service) Int(key string) int64 {
	sp, ok := specByKey[key]
	if !ok || sp.kind != KindInt {
		return 0
	}
	if raw, set := s.override(key); set {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return v
		}
	}
	return sp.defInt(s.defaults)
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
	// Page/Section are the admin-IA placement metadata (config-parity W1): the
	// admin-config page this key renders on and its in-page section.
	Page    string
	Section string
}

// Snapshot returns every mutable setting's effective state, in the canonical
// registry order (deterministic for clients + tests).
func (s *Service) Snapshot() []Effective {
	out := make([]Effective, 0, len(specs))
	for _, sp := range specs {
		_, overridden := s.override(sp.key)
		e := Effective{Key: sp.key, Kind: sp.kind, Overridden: overridden, Options: sp.options,
			Page: sp.page, Section: sp.section}
		switch sp.kind {
		case KindBool:
			e.Value = s.Bool(sp.key)
			e.Default = sp.defBool(s.defaults)
		case KindInt:
			e.Value = s.Int(sp.key)
			e.Default = sp.defInt(s.defaults)
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

// parseIntValue decodes the normalised decimal string form of an int-kind value
// and enforces the shared upper bound (maxSafeInt) so it round-trips through a
// client's JS double without precision loss.
func parseIntValue(v string) (int64, error) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, errors.New("must be an integer")
	}
	if n > maxSafeInt {
		return 0, fmt.Errorf("must be at most %d", maxSafeInt)
	}
	return n, nil
}

// intMin returns a validator accepting any integer >= min.
func intMin(min int64) func(string) error {
	msg := fmt.Sprintf("must be an integer >= %d", min)
	return func(v string) error {
		n, err := parseIntValue(v)
		if err != nil {
			return err
		}
		if n < min {
			return errors.New(msg)
		}
		return nil
	}
}

// intRange returns a validator accepting an integer in [min, max].
func intRange(min, max int64) func(string) error {
	msg := fmt.Sprintf("must be an integer between %d and %d", min, max)
	return func(v string) error {
		n, err := parseIntValue(v)
		if err != nil {
			return err
		}
		if n < min || n > max {
			return errors.New(msg)
		}
		return nil
	}
}

// intZeroOrRange returns a validator accepting 0 (the "unlimited"/"no cap"
// sentinel used by the operational limits) or an integer in [min, max].
func intZeroOrRange(min, max int64) func(string) error {
	msg := fmt.Sprintf("must be 0 or an integer between %d and %d", min, max)
	return func(v string) error {
		n, err := parseIntValue(v)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if n < min || n > max {
			return errors.New(msg)
		}
		return nil
	}
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

// hexColorRe matches a 6-digit CSS hex color ("#rrggbb").
var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validateOptionalHexColor accepts the empty string (no primary-color
// override) or a strict 6-digit hex color.
func validateOptionalHexColor(v string) error {
	if v == "" {
		return nil
	}
	if !hexColorRe.MatchString(v) {
		return errors.New(`must be a hex color like "#0f62fe"`)
	}
	return nil
}

// twitterUsernameRe matches an X/Twitter handle with an optional leading @
// (1-15 word characters, the platform's own constraint).
var twitterUsernameRe = regexp.MustCompile(`^@?[A-Za-z0-9_]{1,15}$`)

// validateOptionalTwitterUsername accepts the empty string (no twitter:site
// card attribution) or a well-formed handle.
func validateOptionalTwitterUsername(v string) error {
	if v == "" {
		return nil
	}
	if !twitterUsernameRe.MatchString(v) {
		return errors.New(`must be an X/Twitter username like "@vidra"`)
	}
	return nil
}

// defaultTranscodingResolutions is the canonical stored form of the shipped
// HLS ladder (media.DefaultHLSResolutionHeights), computed once so the
// registry default can never drift from the transcoder's.
var defaultTranscodingResolutions = FormatList(rungHeightsToStrings(media.DefaultHLSResolutionHeights))

// rungHeightsToStrings renders ladder rung heights as the registry's list-item
// strings ("1080", "720", …).
func rungHeightsToStrings(heights []int) []string {
	out := make([]string, 0, len(heights))
	for _, h := range heights {
		out = append(out, strconv.Itoa(h))
	}
	return out
}

// ParseRungHeights maps a transcoding_resolutions list value to integer rung
// heights, dropping anything non-numeric (unreachable through Apply, which
// validates every item). Exported for cmd/api's encode-settings provider.
func ParseRungHeights(items []string) []int {
	out := make([]int, 0, len(items))
	for _, it := range items {
		if n, err := strconv.Atoi(strings.TrimSpace(it)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// validateTranscodingResolutions requires a non-empty, duplicate-free list of
// canonical ladder rung heights (media.HLSCanonicalRungHeights; there is
// deliberately no 0p audio-only rung in v1). An empty list is refused — it
// would disable every rung while transcoding stays on; use transcoding_enabled
// for that.
func validateTranscodingResolutions(v string) error {
	items, err := parseList(v)
	if err != nil {
		return errors.New("must be an array of strings")
	}
	if len(items) == 0 {
		return errors.New("must enable at least one resolution (use transcoding_enabled to turn transcoding off)")
	}
	seen := map[int]bool{}
	for _, it := range items {
		n, err := strconv.Atoi(strings.TrimSpace(it))
		if err != nil || !media.IsHLSRungHeight(n) {
			return fmt.Errorf("unknown resolution %q (valid: %s)", it, strings.Join(rungHeightsToStrings(media.HLSCanonicalRungHeights), ", "))
		}
		if seen[n] {
			return fmt.Errorf("duplicate resolution %q", it)
		}
		seen[n] = true
	}
	return nil
}

// validateLicenceID accepts 0 (no default licence, mirroring PeerTube's null)
// or a known taxonomy licence id (the GET /videos/config source; ids are
// PT-compatible ints).
func validateLicenceID(v string) error {
	n, err := parseIntValue(v)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if !video.IsLicense(strconv.FormatInt(n, 10)) {
		return errors.New("must be 0 or a known licence id")
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

// FormatInt normalises a Go int64 to the stored decimal-string form. Exported
// for the HTTP layer, which shapes the PATCH payload into Updates.
func FormatInt(v int64) string { return strconv.FormatInt(v, 10) }
