package admin

import (
	"encoding/json"
	"net/http"
)

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
	TotalUsers           int64 `json:"totalUsers"`
	TotalDailyActiveUsers int64 `json:"totalDailyActiveUsers"`
	TotalWeeklyActiveUsers int64 `json:"totalWeeklyActiveUsers"`
	TotalMonthlyActiveUsers int64 `json:"totalMonthlyActiveUsers"`
	TotalLocalVideos     int64 `json:"totalLocalVideos"`
	TotalLocalVideoViews int64 `json:"totalLocalVideoViews"`
	TotalInstanceFollowers int64 `json:"totalInstanceFollowers"`
	TotalInstanceFollowing int64 `json:"totalInstanceFollowing"`
	TotalVideos          int64 `json:"totalVideos"`
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
