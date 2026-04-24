package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/obs"
)

// configResetRepo is the minimal interface needed for config reset and homepage management.
type configResetRepo interface {
	DeleteAllInstanceConfigs(ctx context.Context) error
	GetConfigValue(ctx context.Context, key string) (string, error)
	SetConfigValue(ctx context.Context, key, value string) error
}

// ConfigResetHandlers provides config reset and custom homepage endpoints.
type ConfigResetHandlers struct {
	repo        configResetRepo
	auditLogger *obs.AuditLogger
}

// NewConfigResetHandlers creates a new ConfigResetHandlers.
func NewConfigResetHandlers(repo configResetRepo, auditLogger ...*obs.AuditLogger) *ConfigResetHandlers {
	h := &ConfigResetHandlers{repo: repo}
	if len(auditLogger) > 0 {
		h.auditLogger = auditLogger[0]
	}
	return h
}

// DeleteCustomConfig handles DELETE /api/v1/config/custom — resets all custom config to defaults.
func (h *ConfigResetHandlers) DeleteCustomConfig(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeleteAllInstanceConfigs(r.Context()); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to reset config: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// customConfigBody is the request/response body for GET/PUT /api/v1/config/custom.
// Mirrors the frontend InstanceConfig shape so the admin settings form round-trips
// without silent field drops.
type customConfigBody struct {
	Instance    customConfigInstance    `json:"instance"`
	Signup      customConfigSignup      `json:"signup"`
	User        customConfigUser        `json:"user"`
	Transcoding customConfigTranscoding `json:"transcoding"`
	Live        customConfigLive        `json:"live"`
	Import      customConfigImport      `json:"import"`
}

type customConfigInstance struct {
	Name              string `json:"name"`
	ShortDescription  string `json:"shortDescription"`
	Description       string `json:"description"`
	Terms             string `json:"terms"`
	IsNSFW            bool   `json:"isNSFW"`
	DefaultNSFWPolicy string `json:"defaultNSFWPolicy"`
}

type customConfigSignup struct {
	Enabled                   bool `json:"enabled"`
	RequiresEmailVerification bool `json:"requiresEmailVerification"`
	Limit                     int  `json:"limit"`
}

type customConfigUser struct {
	VideoQuota      int64 `json:"videoQuota"`
	VideoQuotaDaily int64 `json:"videoQuotaDaily"`
}

type customConfigTranscoding struct {
	Enabled     bool            `json:"enabled"`
	Resolutions map[string]bool `json:"resolutions"`
}

type customConfigLive struct {
	Enabled     bool `json:"enabled"`
	MaxDuration int  `json:"maxDuration"`
}

type customConfigImport struct {
	Videos customConfigImportVideos `json:"videos"`
}

type customConfigImportVideos struct {
	HTTP    customConfigImportTransport `json:"http"`
	Torrent customConfigImportTransport `json:"torrent"`
}

type customConfigImportTransport struct {
	Enabled bool `json:"enabled"`
}

// GetCustomConfig handles GET /api/v1/config/custom — admin only.
func (h *ConfigResetHandlers) GetCustomConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := customConfigBody{}

	// Instance.
	cfg.Instance.Name, _ = h.repo.GetConfigValue(ctx, "instance_name")
	cfg.Instance.ShortDescription, _ = h.repo.GetConfigValue(ctx, "instance_short_description")
	cfg.Instance.Description, _ = h.repo.GetConfigValue(ctx, "instance_description")
	cfg.Instance.Terms, _ = h.repo.GetConfigValue(ctx, "instance_terms")
	isNSFW, _ := h.repo.GetConfigValue(ctx, "instance_is_nsfw")
	cfg.Instance.IsNSFW = isNSFW == "true"
	defaultNSFW, _ := h.repo.GetConfigValue(ctx, "instance_default_nsfw_policy")
	if defaultNSFW == "" {
		defaultNSFW = "blur"
	}
	cfg.Instance.DefaultNSFWPolicy = defaultNSFW

	// Signup.
	signupEnabled, _ := h.repo.GetConfigValue(ctx, "signup_enabled")
	cfg.Signup.Enabled = signupEnabled != "false"
	requiresEmail, _ := h.repo.GetConfigValue(ctx, "signup_requires_email_verification")
	cfg.Signup.RequiresEmailVerification = requiresEmail != "false"
	signupLimit, _ := h.repo.GetConfigValue(ctx, "signup_limit")
	cfg.Signup.Limit = parseIntOrDefault(signupLimit, 0)

	// User quotas.
	cfg.User.VideoQuota = parseInt64OrDefault(mustGet(h.repo, ctx, "user_video_quota"), 50)
	cfg.User.VideoQuotaDaily = parseInt64OrDefault(mustGet(h.repo, ctx, "user_video_quota_daily"), -1)

	// Transcoding.
	transEnabled, _ := h.repo.GetConfigValue(ctx, "transcoding_enabled")
	cfg.Transcoding.Enabled = transEnabled != "false"
	resBlob, _ := h.repo.GetConfigValue(ctx, "transcoding_resolutions")
	cfg.Transcoding.Resolutions = parseResolutionMap(resBlob)

	// Live.
	liveEnabled, _ := h.repo.GetConfigValue(ctx, "live_enabled")
	cfg.Live.Enabled = liveEnabled != "false"
	cfg.Live.MaxDuration = parseIntOrDefault(mustGet(h.repo, ctx, "live_max_duration"), -1)

	// Import.
	httpImport, _ := h.repo.GetConfigValue(ctx, "import_http_enabled")
	cfg.Import.Videos.HTTP.Enabled = httpImport != "false"
	torrentImport, _ := h.repo.GetConfigValue(ctx, "import_torrent_enabled")
	cfg.Import.Videos.Torrent.Enabled = torrentImport == "true"

	shared.WriteJSON(w, http.StatusOK, cfg)
}

// UpdateCustomConfig handles PUT /api/v1/config/custom — admin only.
func (h *ConfigResetHandlers) UpdateCustomConfig(w http.ResponseWriter, r *http.Request) {
	var req customConfigBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}
	ctx := r.Context()

	// Instance.
	_ = h.repo.SetConfigValue(ctx, "instance_name", req.Instance.Name)
	_ = h.repo.SetConfigValue(ctx, "instance_short_description", req.Instance.ShortDescription)
	_ = h.repo.SetConfigValue(ctx, "instance_description", req.Instance.Description)
	_ = h.repo.SetConfigValue(ctx, "instance_terms", req.Instance.Terms)
	_ = h.repo.SetConfigValue(ctx, "instance_is_nsfw", boolStr(req.Instance.IsNSFW))
	if req.Instance.DefaultNSFWPolicy != "" {
		_ = h.repo.SetConfigValue(ctx, "instance_default_nsfw_policy", req.Instance.DefaultNSFWPolicy)
	}

	// Signup.
	_ = h.repo.SetConfigValue(ctx, "signup_enabled", boolStr(req.Signup.Enabled))
	_ = h.repo.SetConfigValue(ctx, "signup_requires_email_verification", boolStr(req.Signup.RequiresEmailVerification))
	_ = h.repo.SetConfigValue(ctx, "signup_limit", strconv.Itoa(req.Signup.Limit))

	// User quotas.
	_ = h.repo.SetConfigValue(ctx, "user_video_quota", strconv.FormatInt(req.User.VideoQuota, 10))
	_ = h.repo.SetConfigValue(ctx, "user_video_quota_daily", strconv.FormatInt(req.User.VideoQuotaDaily, 10))

	// Transcoding.
	_ = h.repo.SetConfigValue(ctx, "transcoding_enabled", boolStr(req.Transcoding.Enabled))
	if req.Transcoding.Resolutions == nil {
		req.Transcoding.Resolutions = map[string]bool{}
	}
	resBlob, err := json.Marshal(req.Transcoding.Resolutions)
	if err == nil {
		_ = h.repo.SetConfigValue(ctx, "transcoding_resolutions", string(resBlob))
	}

	// Live.
	_ = h.repo.SetConfigValue(ctx, "live_enabled", boolStr(req.Live.Enabled))
	_ = h.repo.SetConfigValue(ctx, "live_max_duration", strconv.Itoa(req.Live.MaxDuration))

	// Import.
	_ = h.repo.SetConfigValue(ctx, "import_http_enabled", boolStr(req.Import.Videos.HTTP.Enabled))
	_ = h.repo.SetConfigValue(ctx, "import_torrent_enabled", boolStr(req.Import.Videos.Torrent.Enabled))

	if h.auditLogger != nil {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		h.auditLogger.Update("config", userID, obs.NewConfigAuditView(map[string]interface{}{
			"instance-name":       req.Instance.Name,
			"signup":              req.Signup.Enabled,
			"transcoding-enabled": req.Transcoding.Enabled,
			"live-enabled":        req.Live.Enabled,
			"user-video-quota":    req.User.VideoQuota,
		}), obs.NewConfigAuditView(map[string]interface{}{}))
	}

	shared.WriteJSON(w, http.StatusOK, req)
}

// boolStr returns the canonical string representation for a boolean config value.
func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// parseIntOrDefault parses a string as int; returns def on empty/error.
func parseIntOrDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// parseInt64OrDefault parses a string as int64; returns def on empty/error.
func parseInt64OrDefault(s string, def int64) int64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// parseResolutionMap unmarshals the transcoding_resolutions JSON blob.
// Returns an empty map if the blob is missing or malformed.
func parseResolutionMap(blob string) map[string]bool {
	result := map[string]bool{}
	if blob == "" {
		return result
	}
	_ = json.Unmarshal([]byte(blob), &result)
	return result
}

// mustGet returns the value for key, ignoring errors (callers apply defaults).
func mustGet(repo configResetRepo, ctx context.Context, key string) string {
	v, _ := repo.GetConfigValue(ctx, key)
	return v
}

// GetCustomHomepage handles GET /api/v1/custom-pages/homepage/instance.
func (h *ConfigResetHandlers) GetCustomHomepage(w http.ResponseWriter, r *http.Request) {
	content, err := h.repo.GetConfigValue(r.Context(), "homepage_content")
	if err != nil {
		content = ""
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"content": content})
}

// UpdateCustomHomepage handles PUT /api/v1/custom-pages/homepage/instance.
func (h *ConfigResetHandlers) UpdateCustomHomepage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}
	if err := h.repo.SetConfigValue(r.Context(), "homepage_content", req.Content); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update homepage: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// publicConfigResponse matches PeerTube GET /api/v1/config response shape
type publicConfigResponse struct {
	Instance publicConfigInstance `json:"instance"`
	Signup   publicConfigSignup   `json:"signup"`
	Video    publicConfigVideo    `json:"video"`
	Features publicConfigFeatures `json:"features"`
}

type publicConfigInstance struct {
	Name              string `json:"name"`
	ShortDescription  string `json:"shortDescription"`
	Description       string `json:"description"`
	IsNSFW            bool   `json:"isNSFW"`
	DefaultNSFWPolicy string `json:"defaultNSFWPolicy"`
	ServerVersion     string `json:"serverVersion"`
}

type publicConfigSignup struct {
	Allowed                   bool `json:"allowed"`
	AllowedForCurrentIP       bool `json:"allowedForCurrentIP"`
	RequiresEmailVerification bool `json:"requiresEmailVerification"`
}

type publicConfigVideo struct {
	File publicConfigVideoFile `json:"file"`
}

type publicConfigVideoFile struct {
	Size publicConfigFileSize `json:"size"`
}

type publicConfigFileSize struct {
	Max int64 `json:"max"`
}

type publicConfigFeatures struct {
	Signup bool `json:"signup"`
	Login  bool `json:"login"`
}

// publicStatsResponse matches PeerTube GET /api/v1/instance/stats response shape
type publicStatsResponse struct {
	TotalUsers              int64 `json:"totalUsers"`
	TotalDailyActiveUsers   int64 `json:"totalDailyActiveUsers"`
	TotalWeeklyActiveUsers  int64 `json:"totalWeeklyActiveUsers"`
	TotalMonthlyActiveUsers int64 `json:"totalMonthlyActiveUsers"`
	TotalLocalVideos        int64 `json:"totalLocalVideos"`
	TotalLocalVideoViews    int64 `json:"totalLocalVideoViews"`
	TotalInstanceFollowers  int64 `json:"totalInstanceFollowers"`
	TotalInstanceFollowing  int64 `json:"totalInstanceFollowing"`
	TotalVideos             int64 `json:"totalVideos"`
}

// GetPublicConfig handles GET /api/v1/config — public, no auth required.
// Returns PeerTube-compatible instance configuration.
func (h *InstanceHandlers) GetPublicConfig(w http.ResponseWriter, r *http.Request) {
	resp := publicConfigResponse{
		Instance: publicConfigInstance{
			Name:              "Vidra Core Instance",
			ShortDescription:  "",
			Description:       "",
			IsNSFW:            false,
			DefaultNSFWPolicy: "do_not_list",
			ServerVersion:     "1.0.0",
		},
		Signup: publicConfigSignup{
			Allowed:                   true,
			AllowedForCurrentIP:       true,
			RequiresEmailVerification: false,
		},
		Video: publicConfigVideo{
			File: publicConfigVideoFile{
				Size: publicConfigFileSize{Max: 8589934592}, // 8 GiB default
			},
		},
		Features: publicConfigFeatures{
			Signup: true,
			Login:  true,
		},
	}

	// Overlay instance config from DB if repo is available
	if h.moderationRepo != nil {
		configs, err := h.moderationRepo.ListInstanceConfigs(r.Context(), true)
		if err == nil {
			for _, cfg := range configs {
				switch cfg.Key {
				case "instance_name":
					var v string
					if err := json.Unmarshal(cfg.Value, &v); err == nil && v != "" {
						resp.Instance.Name = v
					}
				case "instance_description":
					var v string
					if err := json.Unmarshal(cfg.Value, &v); err == nil {
						resp.Instance.Description = v
					}
				case "instance_default_nsfw_policy":
					var v string
					if err := json.Unmarshal(cfg.Value, &v); err == nil && v != "" {
						resp.Instance.DefaultNSFWPolicy = v
					}
				case "signup_enabled":
					var v bool
					if err := json.Unmarshal(cfg.Value, &v); err == nil {
						resp.Signup.Allowed = v
						resp.Features.Signup = v
					}
				}
			}
		}
	}

	writePublicJSON(w, resp)
}

// GetInstanceAboutPublic handles GET /api/v1/config/about — public, no auth required.
// Returns instance about info (name, description, contact, terms, rules).
func (h *InstanceHandlers) GetInstanceAboutPublic(w http.ResponseWriter, r *http.Request) {
	// Delegate to existing GetInstanceAbout which already builds the full InstanceInfo
	h.GetInstanceAbout(w, r)
}

// GetPublicStats handles GET /api/v1/instance/stats — public, no auth required.
// Returns aggregate instance statistics.
func (h *InstanceHandlers) GetPublicStats(w http.ResponseWriter, r *http.Request) {
	resp := publicStatsResponse{}

	if h.moderationRepo != nil {
		totalUsers, totalVideos, totalLocalVideos, _, err := h.moderationRepo.GetInstanceStats(r.Context())
		if err == nil {
			resp.TotalUsers = totalUsers
			resp.TotalVideos = totalVideos
			resp.TotalLocalVideos = totalLocalVideos
		}
	}

	writePublicJSON(w, resp)
}

// writePublicJSON writes a JSON response without the shared envelope, for PeerTube-compatible endpoints.
func writePublicJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}
