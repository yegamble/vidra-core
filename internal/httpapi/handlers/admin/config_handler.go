package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"athena/internal/httpapi/shared"
)

// configResetRepo is the minimal interface needed for config reset and homepage management.
type configResetRepo interface {
	DeleteAllInstanceConfigs(ctx context.Context) error
	GetConfigValue(ctx context.Context, key string) (string, error)
	SetConfigValue(ctx context.Context, key, value string) error
}

// ConfigResetHandlers provides config reset and custom homepage endpoints.
type ConfigResetHandlers struct {
	repo configResetRepo
}

// NewConfigResetHandlers creates a new ConfigResetHandlers.
func NewConfigResetHandlers(repo configResetRepo) *ConfigResetHandlers {
	return &ConfigResetHandlers{repo: repo}
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
type customConfigBody struct {
	Instance customConfigInstance `json:"instance"`
	Signup   customConfigSignup   `json:"signup"`
}

type customConfigInstance struct {
	Name             string `json:"name"`
	ShortDescription string `json:"shortDescription"`
	Description      string `json:"description"`
	IsNSFW           bool   `json:"isNSFW"`
}

type customConfigSignup struct {
	Enabled bool `json:"enabled"`
}

// GetCustomConfig handles GET /api/v1/config/custom — admin only.
func (h *ConfigResetHandlers) GetCustomConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := customConfigBody{}
	cfg.Instance.Name, _ = h.repo.GetConfigValue(ctx, "instance_name")
	cfg.Instance.ShortDescription, _ = h.repo.GetConfigValue(ctx, "instance_short_description")
	cfg.Instance.Description, _ = h.repo.GetConfigValue(ctx, "instance_description")
	isNSFW, _ := h.repo.GetConfigValue(ctx, "instance_is_nsfw")
	cfg.Instance.IsNSFW = isNSFW == "true"
	signupEnabled, _ := h.repo.GetConfigValue(ctx, "signup_enabled")
	cfg.Signup.Enabled = signupEnabled != "false"
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
	_ = h.repo.SetConfigValue(ctx, "instance_name", req.Instance.Name)
	_ = h.repo.SetConfigValue(ctx, "instance_short_description", req.Instance.ShortDescription)
	_ = h.repo.SetConfigValue(ctx, "instance_description", req.Instance.Description)
	isNSFW := "false"
	if req.Instance.IsNSFW {
		isNSFW = "true"
	}
	_ = h.repo.SetConfigValue(ctx, "instance_is_nsfw", isNSFW)
	signupEnabled := "true"
	if !req.Signup.Enabled {
		signupEnabled = "false"
	}
	_ = h.repo.SetConfigValue(ctx, "signup_enabled", signupEnabled)
	shared.WriteJSON(w, http.StatusOK, req)
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
			Name:              "Athena Instance",
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
