package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancedocs"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/profileimage"
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
	Uploads   bool `json:"uploads"`
	Imports   bool `json:"imports"`
	Live      bool `json:"live"`
	Comments  bool `json:"comments"`
	Downloads bool `json:"downloads"`

	// Messaging and MessagingE2EE report the EFFECTIVE direct-messaging
	// availability: the runtime setting AND the wiring (a deployment that never
	// mounts the messaging/e2ee services has no routes to offer). MessagingE2EE
	// additionally folds in Messaging — the encrypted surface is nested under
	// the master switch. The client MUST read these rather than probing
	// /e2ee/devices for a non-404: a probe cannot distinguish "the operator
	// turned this off" (403) from a transport error, and it could never see the
	// plaintext-vs-encrypted distinction at all.
	Messaging     bool `json:"messaging"`
	MessagingE2EE bool `json:"messaging_e2ee"`

	// Shipped-feature toggle batch (config-parity W8). Every flag is the
	// EFFECTIVE availability: the runtime admin setting AND (where one exists)
	// the boot capability — import_http and transcription report false when
	// yt-dlp/Whisper is not wired regardless of the setting.
	ImportHTTP  bool `json:"import_http"`
	ChannelSync bool `json:"channel_sync"`
	Storyboards bool `json:"storyboards"`
	// VideoCardPreviews is the instance half of the two-factor hover-preview
	// gate. VideoCardPreviewsDefaultEnabled is inherited by users who have not
	// made an explicit choice; the player-settings endpoint resolves that default
	// and returns the effective preference.
	VideoCardPreviews               bool `json:"video_card_previews"`
	VideoCardPreviewsDefaultEnabled bool `json:"video_card_previews_default_enabled"`
	Transcription                   bool `json:"transcription"`
	UserImport                      bool `json:"user_import"`
	UserExport                      bool `json:"user_export"`
	// Transcoding reports the EFFECTIVE HLS pipeline availability (config-
	// parity W10): the runtime transcoding_enabled setting AND the boot
	// capability (ffmpeg/ffprobe wired). Already-produced playlists keep
	// serving when this is false.
	Transcoding bool `json:"transcoding"`
	// VideoReplace reports the EFFECTIVE video-file-replacement availability
	// (config-parity W14): video_replace_enabled AND uploads — the UI hides
	// the studio "Replace video file" affordance in lock-step with the 403
	// feature_disabled gate on the replace endpoints.
	VideoReplace bool `json:"video_replace"`
	// UploadAdditionalExtensions reports whether the extended upload container
	// set (.mkv/.mov/.avi/…) is currently accepted
	// (upload_additional_extensions_enabled, W10) so the studio can narrow its
	// file-picker accept list in lock-step with the server's extension gate.
	UploadAdditionalExtensions bool `json:"upload_additional_extensions"`
	// Mail reports whether this deployment has an outbound mail path (an SMTP
	// relay, or the dev capture seam) — a boot capability, not a runtime
	// setting. The admin UI uses it to render mail-dependent settings
	// (email_subject_prefix, email_body_signature, contact_form_enabled)
	// disabled-with-explanation instead of silently ineffective (config-parity
	// W6/W12 email seam).
	Mail bool `json:"mail"`
}

// instanceSocialLinks is the operator's social/link row (empty strings when
// unset).
type instanceSocialLinks struct {
	Website  string `json:"website"`
	Mastodon string `json:"mastodon"`
	X        string `json:"x"`
	Bluesky  string `json:"bluesky"`
}

// instanceAssetRef points at one instance branding asset: its public serving
// URL ("" when unset) and whether the client should fall back to its built-in
// default imagery.
type instanceAssetRef struct {
	URL        string `json:"url"`
	IsFallback bool   `json:"is_fallback"`
}

// instanceLogos is the typed logo-slot map (PeerTube LogoType parity).
type instanceLogos struct {
	Favicon      instanceAssetRef `json:"favicon"`
	HeaderWide   instanceAssetRef `json:"header_wide"`
	HeaderSquare instanceAssetRef `json:"header_square"`
	Opengraph    instanceAssetRef `json:"opengraph"`
}

// instanceBranding is the instance imagery block (config-parity W1 shape; W4
// wires the frontend consumers).
type instanceBranding struct {
	Avatar instanceAssetRef `json:"avatar"`
	Banner instanceAssetRef `json:"banner"`
	Logos  instanceLogos    `json:"logos"`
	// HideInstanceName hides the textual instance name in the header (only
	// meaningful once a header logo is set; header_hide_instance_name).
	HideInstanceName bool `json:"hide_instance_name"`
}

// instancePublishDefaults seeds the studio publish form (W9 wires enforcement).
type instancePublishDefaults struct {
	Privacy string `json:"privacy"`
	// Licence is a PT-compatible licence id; 0 = no default licence.
	Licence         int64  `json:"licence"`
	CommentPolicy   string `json:"comment_policy"`
	DownloadEnabled bool   `json:"download_enabled"`
}

// instanceDefaults carries the operator-tuned client behaviour defaults
// (feed/landing/theme/player; W5 wires the frontend consumers).
type instanceDefaults struct {
	FeedSort  string `json:"feed_sort"`
	FeedScope string `json:"feed_scope"`
	// BrowseScrollMode is "button" (an explicit pager/Load more — the default
	// and the shipped behaviour) or "auto" (infinite scroll). It sits beside
	// feed_sort/feed_scope because it is the same kind of decision: how the
	// operator wants browse lists to behave before any viewer touches them.
	BrowseScrollMode                 string                  `json:"browse_scroll_mode"`
	LandingPage                      string                  `json:"landing_page"`
	Theme                            string                  `json:"theme"`
	PlayerAutoplay                   bool                    `json:"player_autoplay"`
	MiniaturePreferAuthorDisplayName bool                    `json:"miniature_prefer_author_display_name"`
	Publish                          instancePublishDefaults `json:"publish"`
}

// instanceBroadcast is the operator broadcast-banner block (W3 renders it).
type instanceBroadcast struct {
	Enabled     bool   `json:"enabled"`
	Message     string `json:"message"` // raw markdown
	Level       string `json:"level"`   // info|warning|error
	Dismissable bool   `json:"dismissable"`
}

// instanceFeatured is the admin featured-banner block (home-featured-banner).
// The frontend fetches the picked video server-side and renders the masthead
// banner only when Enabled AND VideoID resolves to a public, published video —
// so this block reports the raw admin knobs, not a resolved video.
type instanceFeatured struct {
	Enabled     bool   `json:"enabled"`
	VideoID     string `json:"video_id"`    // "" when no video picked
	Title       string `json:"title"`       // "" = fall back to the video's title
	Description string `json:"description"` // "" = fall back to the video's description
	CTALabel    string `json:"cta_label"`   // "" = frontend default ("Watch now")
	Label       string `json:"label"`       // featured|sponsored
}

// instanceCustomization references the operator's custom CSS/JS documents by
// content hash (never inlined; fetch /instance/custom.css?v={css_hash}) plus
// the primary-color override ("" when unset).
type instanceCustomization struct {
	CSSHash      string `json:"css_hash"`
	JSHash       string `json:"js_hash"`
	PrimaryColor string `json:"primary_color"`
}

// instanceSocialMeta is link-card metadata (twitter:site; distinct from the
// social_links.x profile URL).
type instanceSocialMeta struct {
	TwitterUsername string `json:"twitter_username"`
}

// instanceHomepage reports the admin-authored homepage document's state; the
// body is fetched separately from GET /instance/homepage.
type instanceHomepage struct {
	Enabled bool   `json:"enabled"`
	Hash    string `json:"hash"`
}

// instanceLiveConfig is the effective live-streaming policy block (config-
// parity W11) so the live-create UI can hide the replay toggle, seed its
// default, and explain the caps. features.live stays the master availability
// flag; these are the policy knobs under it. Every limit follows the vidra
// 0 = unlimited/no-limit convention.
type instanceLiveConfig struct {
	AllowReplay bool `json:"allow_replay"`
	// DefaultSaveReplay is the EFFECTIVE seed (setting AND allow_replay).
	DefaultSaveReplay bool  `json:"default_save_replay"`
	MaxInstanceLives  int64 `json:"max_instance_lives"`
	MaxUserLives      int64 `json:"max_user_lives"`
	MaxDurationSecs   int64 `json:"max_duration_secs"`
}

// instanceSearchConfig reports the EFFECTIVE remote-URI search gates (config-
// parity W13): whether a URI/handle-shaped search query is resolved to remote
// content for logged-in and anonymous callers respectively. Each flag is the
// runtime setting AND the boot capability (federation on + wired), so the
// frontend's search help text stays in lock-step with the backend gate.
type instanceSearchConfig struct {
	RemoteURIUsers     bool `json:"remote_uri_users"`
	RemoteURIAnonymous bool `json:"remote_uri_anonymous"`
	// Search-service block (search-service W4/W9): the effective instance-level
	// search config the frontend threads into its discovery surfaces. Mode is the
	// ranking family; service_enabled is the effective smart-search availability
	// (svc-wired AND the admin routing toggle); suggestions_enabled is that AND the
	// suggestions toggle (so the frontend stops per-keystroke traffic the moment an
	// admin kills smart search); the personalization/history flags are the INSTANCE
	// half of the two-factor gate.
	ServiceEnabled                     bool   `json:"service_enabled"`
	Mode                               string `json:"mode"`
	SuggestionsEnabled                 bool   `json:"suggestions_enabled"`
	PersonalizedSearchEnabled          bool   `json:"personalized_search_enabled"`
	PersonalizedRecommendationsEnabled bool   `json:"personalized_recommendations_enabled"`
	SearchHistoryEnabled               bool   `json:"search_history_enabled"`
}

// instanceResponse is the public "about this instance" document the frontend
// app shell reads on load: instance name/description, software, whether signup
// is open, and operator-provided legal/contact links (empty when unset).
type instanceResponse struct {
	Name             string           `json:"name"`
	Description      string           `json:"description"`
	ShortDescription string           `json:"short_description"`
	Software         instanceSoftware `json:"software"`
	// RegistrationEnabled is the EFFECTIVE signup availability (config-parity
	// W7): the runtime toggle AND registration_user_limit headroom — it reads
	// false while the instance is at its user limit.
	RegistrationEnabled          bool `json:"registration_enabled"`
	RegistrationRequiresApproval bool `json:"registration_requires_approval"`
	// RegistrationDisabledReason is "" while registration is effectively
	// enabled (or plainly toggled off); "user_limit_reached" when the toggle
	// is on but registration_user_limit is exhausted, so the signup page can
	// explain the closure honestly (additive, W7).
	RegistrationDisabledReason string `json:"registration_disabled_reason"`
	// RegistrationRequiresEmailVerification is the EFFECTIVE verification gate
	// (the setting AND features.mail): new signups answer 202 and stay
	// sessionless until the emailed link is followed (W7).
	RegistrationRequiresEmailVerification bool `json:"registration_requires_email_verification"`
	// RegistrationMinimumAge is the signup age-attestation threshold: 0 = off,
	// otherwise registration requires the "I am at least N years old"
	// attestation flag (PeerTube parity — no birthdate is collected; W7).
	RegistrationMinimumAge int64 `json:"registration_minimum_age"`
	// OwnerClaimPending reports first-run state (0104): true while the
	// instance awaits its owner — zero accounts and an unclaimed boot-minted
	// setup token — so the frontend can route to the claim-owner wizard
	// without probing register. Every signup path answers 403
	// owner_claim_required while this is true.
	OwnerClaimPending bool `json:"owner_claim_pending"`
	// OAuthProviders lists the configured OIDC login provider names so the
	// frontend can render "continue with …" buttons pointing at
	// GET /api/v1/auth/oauth/{provider}. Empty array when OAuth is off.
	OAuthProviders []string `json:"oauth_providers"`
	// ATProtoLogin reports whether ATProto identity login (Bluesky / any PDS) is
	// enabled (ATPROTO_LOGIN_ENABLED), so the frontend can show the "sign in with
	// a handle" affordance. Independent of the OAuth providers above.
	ATProtoLogin bool `json:"atproto_login"`
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

	// Structured effective-config blocks (config-parity W1; see
	// .ralph/specs/config-parity/instance-contract.md). All additive; every
	// field is present with its zero-value/fallback content until the wave that
	// consumes it ships.
	Branding      instanceBranding      `json:"branding"`
	Defaults      instanceDefaults      `json:"defaults"`
	Broadcast     instanceBroadcast     `json:"broadcast"`
	Featured      instanceFeatured      `json:"featured"`
	Customization instanceCustomization `json:"customization"`
	Social        instanceSocialMeta    `json:"social"`
	Homepage      instanceHomepage      `json:"homepage"`
	Live          instanceLiveConfig    `json:"live"`
	Search        instanceSearchConfig  `json:"search"`
}

// handleInstance returns public instance metadata. No auth required; it exposes
// only operator-configured, non-sensitive fields (provider names, never client
// credentials). The name/description/legal-contact metadata and the registration
// gates reflect the EFFECTIVE values — the DB-backed instance-settings overlay
// (fix_plan P10) when wired, else the static config.
//
// The response is cache-friendly (config-parity W1): a strong ETag over the
// serialized document plus Cache-Control: s-maxage=60, so a fronting cache can
// absorb the hot app-shell reads while settings changes still show within a
// minute. Everything the document reads comes from in-memory caches (settings
// overlay, document hashes, branding image metadata, the TTL-cached account
// count behind the registration user limit) — no DB round trip while
// registration_user_limit is unset, at most one every userCountTTL otherwise.
// The one exception is the owner_claim_pending probe, which hits the DB only
// until it settles false (see ownerClaimPending) — i.e. only on a first-run
// instance that has no traffic yet.
func (s *Server) handleInstance(c echo.Context) error {
	body, err := json.Marshal(s.instanceDocument(c.Request().Context()))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	h := c.Response().Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "s-maxage=60")
	if match := c.Request().Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		return c.NoContent(http.StatusNotModified)
	}
	return c.JSONBlob(http.StatusOK, body)
}

// instanceDocument assembles the full public instance document.
func (s *Server) instanceDocument(ctx context.Context) instanceResponse {
	// Effective registration state (config-parity W7): the runtime toggle,
	// then the user-limit headroom with an explicit machine-readable reason.
	regEnabled := s.registrationEnabled()
	regReason := ""
	if regEnabled && s.registrationAtUserLimit(ctx) {
		regEnabled = false
		regReason = "user_limit_reached"
	}
	return instanceResponse{
		Name:                                  s.settingString(instancesettings.KeyInstanceName, s.cfg.InstanceName),
		Description:                           s.settingString(instancesettings.KeyInstanceDescription, s.cfg.InstanceDescription),
		ShortDescription:                      s.settingString(instancesettings.KeyInstanceShortDescription, ""),
		Software:                              instanceSoftware{Name: "vidra", Version: version.Version},
		RegistrationEnabled:                   regEnabled,
		RegistrationRequiresApproval:          s.registrationRequiresApproval(),
		RegistrationDisabledReason:            regReason,
		RegistrationRequiresEmailVerification: s.registrationRequiresEmailVerification(),
		RegistrationMinimumAge:                s.registrationMinimumAge(),
		OwnerClaimPending:                     s.ownerClaimPending(ctx),
		OAuthProviders:                        s.cfg.OAuthProviderNames(),
		ATProtoLogin:                          s.atprotologinsvc != nil && s.atprotologinsvc.Enabled(),
		FederationEnabled:                     s.cfg.FederationEnabled,
		TermsURL:                              s.settingString(instancesettings.KeyTermsURL, s.cfg.InstanceTermsURL),
		PrivacyURL:                            s.settingString(instancesettings.KeyPrivacyURL, s.cfg.InstancePrivacyURL),
		ContactEmail:                          s.effectiveContactEmail(),
		DefaultLanguage:                       s.settingString(instancesettings.KeyDefaultLanguage, instancesettings.DefaultDefaultLanguage),
		Categories:                            s.settingStrings(instancesettings.KeyInstanceCategories),
		ModeratorLanguages:                    s.settingStrings(instancesettings.KeyModeratorLanguages),
		ServerCountry:                         s.settingString(instancesettings.KeyServerCountry, ""),
		IsSensitive:                           s.settingBool(instancesettings.KeyInstanceIsSensitive, false),
		SensitiveContentPolicy:                s.sensitiveContentPolicy(),
		ContactFormEnabled:                    s.contactFormAvailable(),
		SocialLinks: instanceSocialLinks{
			Website:  s.settingString(instancesettings.KeyWebsiteLink, ""),
			Mastodon: s.settingString(instancesettings.KeyMastodonLink, ""),
			X:        s.settingString(instancesettings.KeyXLink, ""),
			Bluesky:  s.settingString(instancesettings.KeyBlueskyLink, ""),
		},
		Features: instanceFeatures{
			Uploads:   s.uploadsEnabled(),
			Imports:   s.importsEnabled(),
			Live:      s.liveEnabled(),
			Comments:  s.commentsEnabled(),
			Downloads: s.downloadsEnabled(),
			// Setting AND wiring, so the UI hides the DM affordances in
			// lock-step with the 403 feature_disabled route gates instead of
			// offering controls that fail.
			Messaging:     s.messagingEnabled() && s.messagingsvc != nil,
			MessagingE2EE: s.messagingE2EEEnabled() && s.messagingsvc != nil && s.e2eesvc != nil,
			// W8 flags: setting AND boot capability (service Enabled() folds
			// the runtime provider in when cmd/api wires one).
			ImportHTTP:                      s.importsEnabled() && s.importHTTPEnabled() && s.importsvc != nil && s.importsvc.YtdlpEnabled(),
			ChannelSync:                     s.channelSyncEnabled() && s.channelsyncsvc != nil && s.channelsyncsvc.Enabled(),
			Storyboards:                     s.storyboardsEnabled(),
			VideoCardPreviews:               s.videoCardPreviewsEnabled(),
			VideoCardPreviewsDefaultEnabled: s.videoCardPreviewsDefaultEnabled(),
			Transcription:                   s.transcriptionEnabled() && s.captionjobsvc != nil && s.captionjobsvc.Enabled(),
			UserImport:                      s.userImportEnabled(),
			UserExport:                      s.userExportEnabled(),
			Transcoding:                     s.transcodingEnabled() && s.transcodesvc != nil && s.transcodesvc.Capable(),
			VideoReplace:                    s.videoReplaceAvailable(),
			UploadAdditionalExtensions:      s.uploadAdditionalExtensionsEnabled(),
			// The contact mailer is wired whenever ANY outbound mail path
			// exists (SMTP or the dev capture seam) — the deployment's
			// mail-capability signal.
			Mail: s.contactMailer != nil,
		},
		Branding:      s.instanceBrandingBlock(),
		Defaults:      s.instanceDefaultsBlock(),
		Broadcast:     s.instanceBroadcastBlock(),
		Featured:      s.instanceFeaturedBlock(),
		Customization: s.instanceCustomizationBlock(),
		Social: instanceSocialMeta{
			TwitterUsername: s.settingString(instancesettings.KeySocialMetaTwitterUsername, ""),
		},
		Homepage: s.instanceHomepageBlock(),
		Live: instanceLiveConfig{
			AllowReplay:       s.liveAllowReplay(),
			DefaultSaveReplay: s.liveDefaultSaveReplay(),
			MaxInstanceLives:  s.settingInt(instancesettings.KeyLiveMaxInstanceLives, 0),
			MaxUserLives:      s.settingInt(instancesettings.KeyLiveMaxUserLives, 0),
			MaxDurationSecs:   s.settingInt(instancesettings.KeyLiveMaxDurationSecs, 0),
		},
		Search: instanceSearchConfig{
			RemoteURIUsers:                     s.remoteSearchAvailable() && s.searchRemoteURIUsers(),
			RemoteURIAnonymous:                 s.remoteSearchAvailable() && s.searchRemoteURIAnonymous(),
			ServiceEnabled:                     s.searchEnabled() && s.searchServiceEnabled(),
			Mode:                               s.searchMode(),
			SuggestionsEnabled:                 s.searchEnabled() && s.searchServiceEnabled() && s.instanceSuggestionsEnabled(),
			PersonalizedSearchEnabled:          s.instancePersonalizedSearch(),
			PersonalizedRecommendationsEnabled: s.instancePersonalizedRecs(),
			SearchHistoryEnabled:               s.instanceSearchHistoryEnabled(),
		},
	}
}

// ownerClaimPending resolves the first-run signal for the public instance
// document. Pending is monotone within a process (claim tokens are minted only
// at boot, so it can go true→false but never back), which makes the latch
// safe: once false is observed the DB is never asked again. While pending — a
// zero-user instance whose only traffic is the operator — each read re-checks
// so the flag clears the moment the owner claims. Errors report false: the
// setup wizard must never engage on a transient DB error.
func (s *Server) ownerClaimPending(ctx context.Context) bool {
	if s.authsvc == nil {
		return false
	}
	s.ownerClaimMu.Lock()
	defer s.ownerClaimMu.Unlock()
	if s.ownerClaimSettled {
		return false
	}
	pending, err := s.authsvc.OwnerClaimPending(ctx)
	if err != nil {
		return false
	}
	if !pending {
		s.ownerClaimSettled = true
	}
	return pending
}

// instanceAsset resolves one branding slot to its public URL (the dedicated
// serving route) or the empty fallback ref when no image is set. Lookups hit
// the profileimage in-memory instance cache only.
func (s *Server) instanceAsset(kind, url string) instanceAssetRef {
	if s.imagesvc != nil {
		if _, ok := s.imagesvc.InstanceImage(kind); ok {
			return instanceAssetRef{URL: url, IsFallback: false}
		}
	}
	return instanceAssetRef{URL: "", IsFallback: true}
}

func (s *Server) instanceBrandingBlock() instanceBranding {
	return instanceBranding{
		Avatar: s.instanceAsset(profileimage.KindAvatar, "/api/v1/instance/avatar"),
		Banner: s.instanceAsset(profileimage.KindBanner, "/api/v1/instance/banner"),
		Logos: instanceLogos{
			Favicon:      s.instanceAsset(profileimage.KindLogoFavicon, "/api/v1/instance/logo/favicon"),
			HeaderWide:   s.instanceAsset(profileimage.KindLogoHeaderWide, "/api/v1/instance/logo/header-wide"),
			HeaderSquare: s.instanceAsset(profileimage.KindLogoHeaderSquare, "/api/v1/instance/logo/header-square"),
			Opengraph:    s.instanceAsset(profileimage.KindLogoOpengraph, "/api/v1/instance/logo/opengraph"),
		},
		HideInstanceName: s.settingBool(instancesettings.KeyHeaderHideInstanceName, false),
	}
}

func (s *Server) instanceDefaultsBlock() instanceDefaults {
	return instanceDefaults{
		FeedSort:                         s.settingString(instancesettings.KeyDefaultFeedSort, instancesettings.DefaultFeedSort),
		FeedScope:                        s.settingString(instancesettings.KeyDefaultFeedScope, instancesettings.DefaultFeedScope),
		BrowseScrollMode:                 s.settingString(instancesettings.KeyBrowseScrollMode, instancesettings.DefaultBrowseScrollMode),
		LandingPage:                      s.settingString(instancesettings.KeyDefaultLandingPage, instancesettings.DefaultLandingPage),
		Theme:                            s.settingString(instancesettings.KeyDefaultTheme, instancesettings.DefaultTheme),
		PlayerAutoplay:                   s.settingBool(instancesettings.KeyDefaultPlayerAutoplay, true),
		MiniaturePreferAuthorDisplayName: s.settingBool(instancesettings.KeyMiniaturePreferAuthorDisplayName, false),
		Publish: instancePublishDefaults{
			Privacy:         s.settingString(instancesettings.KeyDefaultVideoPrivacy, instancesettings.DefaultVideoPrivacy),
			Licence:         s.settingInt(instancesettings.KeyDefaultVideoLicence, 0),
			CommentPolicy:   s.settingString(instancesettings.KeyDefaultCommentPolicy, instancesettings.DefaultCommentPolicy),
			DownloadEnabled: s.settingBool(instancesettings.KeyDefaultDownloadEnabled, true),
		},
	}
}

func (s *Server) instanceBroadcastBlock() instanceBroadcast {
	return instanceBroadcast{
		Enabled:     s.settingBool(instancesettings.KeyBroadcastEnabled, false),
		Message:     s.settingString(instancesettings.KeyBroadcastMessage, ""),
		Level:       s.settingString(instancesettings.KeyBroadcastLevel, instancesettings.DefaultBroadcastLevel),
		Dismissable: s.settingBool(instancesettings.KeyBroadcastDismissable, false),
	}
}

func (s *Server) instanceFeaturedBlock() instanceFeatured {
	return instanceFeatured{
		Enabled:     s.settingBool(instancesettings.KeyFeaturedEnabled, false),
		VideoID:     s.settingString(instancesettings.KeyFeaturedVideoID, ""),
		Title:       s.settingString(instancesettings.KeyFeaturedTitle, ""),
		Description: s.settingString(instancesettings.KeyFeaturedDescription, ""),
		CTALabel:    s.settingString(instancesettings.KeyFeaturedCTALabel, ""),
		Label:       s.settingString(instancesettings.KeyFeaturedLabel, instancesettings.DefaultFeaturedLabel),
	}
}

func (s *Server) instanceCustomizationBlock() instanceCustomization {
	block := instanceCustomization{
		PrimaryColor: s.settingString(instancesettings.KeyThemePrimaryColor, ""),
	}
	if s.instancedocssvc != nil {
		block.CSSHash = s.instancedocssvc.Hash(instancedocs.NameCustomCSS)
		block.JSHash = s.instancedocssvc.Hash(instancedocs.NameCustomJS)
	}
	return block
}

func (s *Server) instanceHomepageBlock() instanceHomepage {
	if s.instancedocssvc == nil {
		return instanceHomepage{}
	}
	doc, ok := s.instancedocssvc.Get(instancedocs.NameHomepage)
	return instanceHomepage{Enabled: ok && doc.Body != "", Hash: doc.SHA256}
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
// presentation-only (frontend). This is the INSTANCE-level view (no per-user
// override) — the anonymous surfaces (RSS/sitemap) use it directly.
func (s *Server) hideSensitiveVideos() bool {
	return s.sensitiveContentPolicy() == instancesettings.SensitiveContentPolicyHide
}

// effectiveSensitivePolicy resolves the sensitive-content policy for THIS request
// (0100): a signed-in caller's non-NULL per-user override wins, else the instance
// policy. An anonymous request (optionalAuth with no token) resolves to the
// instance policy, so its behaviour is unchanged.
func (s *Server) effectiveSensitivePolicy(c echo.Context) string {
	if userID, _, ok := principalFromContext(c); ok && s.authsvc != nil {
		if u, err := s.authsvc.UserByID(c.Request().Context(), userID); err == nil &&
			u.SensitiveContentPolicy != nil {
			if p := strings.TrimSpace(*u.SensitiveContentPolicy); p != "" {
				return p
			}
		}
	}
	return s.sensitiveContentPolicy()
}

// effectiveHideSensitive is hideSensitiveVideos with the per-user override
// applied: sensitive videos are excluded only under the server-enforced "hide"
// policy (warn/blur/display stay presentation-only). Used by the per-viewer
// browse/search surfaces (all optionalAuth).
func (s *Server) effectiveHideSensitive(c echo.Context) bool {
	return s.effectiveSensitivePolicy(c) == instancesettings.SensitiveContentPolicyHide
}

// contactFormAvailable is the EFFECTIVE contact-form availability: the admin
// toggle is on AND an effective contact email is set AND this deployment has
// an outbound mail path (a contact mailer is wired).
func (s *Server) contactFormAvailable() bool {
	return s.contactMailer != nil &&
		s.settingBool(instancesettings.KeyContactFormEnabled, false) &&
		strings.TrimSpace(s.effectiveContactEmail()) != ""
}
