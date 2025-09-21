package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"athena/internal/domain"
	"athena/internal/repository"
	"athena/internal/usecase"
	"github.com/go-chi/chi/v5"
)

// InstanceHandlers handles instance-related HTTP requests
type InstanceHandlers struct {
	moderationRepo *repository.ModerationRepository
	userRepo       usecase.UserRepository
	videoRepo      usecase.VideoRepository
}

// NewInstanceHandlers creates a new instance of InstanceHandlers
func NewInstanceHandlers(moderationRepo *repository.ModerationRepository, userRepo usecase.UserRepository, videoRepo usecase.VideoRepository) *InstanceHandlers {
	return &InstanceHandlers{
		moderationRepo: moderationRepo,
		userRepo:       userRepo,
		videoRepo:      videoRepo,
	}
}

// GetInstanceAbout handles GET /api/v1/instance/about
func (h *InstanceHandlers) GetInstanceAbout(w http.ResponseWriter, r *http.Request) {
	// Get public configuration values
	configs, err := h.moderationRepo.ListInstanceConfigs(r.Context(), true)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get instance configuration"))
		return
	}

	// Get instance statistics
	totalUsers, totalVideos, totalLocalVideos, totalViews, err := h.moderationRepo.GetInstanceStats(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get instance statistics"))
		return
	}

	// Build instance info from configs with default values
	info := domain.InstanceInfo{
		Name:               "Athena Instance",
		Version:            "1.0.0",
		TotalUsers:         totalUsers,
		TotalVideos:        totalVideos,
		TotalLocalVideos:   totalLocalVideos,
		TotalInstanceViews: totalViews,
		Rules:              []string{},
		Languages:          []string{"en"},
		Categories:         []string{},
	}

	// Parse config values (overriding defaults only if config exists and is valid)
	for _, config := range configs {
		switch config.Key {
		case "instance_name":
			var name string
			if err := json.Unmarshal(config.Value, &name); err == nil && name != "" {
				info.Name = name
			}
		case "instance_description":
			var desc string
			if err := json.Unmarshal(config.Value, &desc); err == nil {
				info.Description = desc
			}
		case "instance_version":
			var version string
			if err := json.Unmarshal(config.Value, &version); err == nil && version != "" {
				info.Version = version
			}
		case "instance_contact_email":
			_ = json.Unmarshal(config.Value, &info.ContactEmail)
		case "instance_terms_url":
			_ = json.Unmarshal(config.Value, &info.TermsURL)
		case "instance_privacy_url":
			_ = json.Unmarshal(config.Value, &info.PrivacyURL)
		case "instance_rules":
			_ = json.Unmarshal(config.Value, &info.Rules)
		case "instance_languages":
			_ = json.Unmarshal(config.Value, &info.Languages)
		case "instance_categories":
			_ = json.Unmarshal(config.Value, &info.Categories)
		case "instance_default_nsfw_policy":
			_ = json.Unmarshal(config.Value, &info.DefaultNSFWPolicy)
		case "signup_enabled":
			_ = json.Unmarshal(config.Value, &info.SignupEnabled)
		}
	}

	// Ensure defaults are set if still empty after config parsing
	if info.Name == "" {
		info.Name = "Athena Instance"
	}
	if info.Version == "" {
		info.Version = "1.0.0"
	}

	WriteJSON(w, http.StatusOK, info)
}

// ListInstanceConfigs handles GET /api/v1/admin/instance/config (admin only)
func (h *InstanceHandlers) ListInstanceConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := h.moderationRepo.ListInstanceConfigs(r.Context(), false)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list instance configurations"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data":    configs,
		"success": true,
	})
}

// GetInstanceConfig handles GET /api/v1/admin/instance/config/{key} (admin only)
func (h *InstanceHandlers) GetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing configuration key"))
		return
	}

	config, err := h.moderationRepo.GetInstanceConfig(r.Context(), key)
	if err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, err)
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get configuration"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data":    config,
		"success": true,
	})
}

// UpdateInstanceConfig handles PUT /api/v1/admin/instance/config/{key} (admin only)
func (h *InstanceHandlers) UpdateInstanceConfig(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if key == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing configuration key"))
		return
	}

	var req domain.UpdateInstanceConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if err := h.moderationRepo.UpdateInstanceConfig(r.Context(), key, req.Value, req.IsPublic); err != nil {
		WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update configuration"))
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Configuration updated successfully",
		"success": true,
	})
}

// OEmbed handles GET /api/oembed
func (h *InstanceHandlers) OEmbed(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "URL parameter is required"))
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	maxWidth := r.URL.Query().Get("maxwidth")
	maxHeight := r.URL.Query().Get("maxheight")

	// Extract video ID from URL
	// Expected format: http(s)://domain/api/v1/videos/{id} or /videos/{id}
	videoID := ""
	if strings.Contains(url, "/videos/") {
		parts := strings.Split(url, "/videos/")
		if len(parts) == 2 {
			// Extract ID (might have query params)
			idParts := strings.Split(parts[1], "?")
			videoID = idParts[0]
			// Remove any trailing slashes
			videoID = strings.TrimSuffix(videoID, "/")
		}
	}

	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid video URL"))
		return
	}

	// Get video details
	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
		} else {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get video"))
		}
		return
	}

	// Get uploader info
	uploader, err := h.userRepo.GetByID(r.Context(), video.UserID)
	if err != nil {
		uploader = &domain.User{
			Username:    "Unknown",
			DisplayName: "Unknown User",
		}
	}

	// Build oEmbed response
	width := "640"
	height := "360"
	if maxWidth != "" {
		width = maxWidth
	}
	if maxHeight != "" {
		height = maxHeight
	}

	oembedResponse := map[string]interface{}{
		"version":       "1.0",
		"type":          "video",
		"title":         video.Title,
		"author_name":   uploader.DisplayName,
		"author_url":    fmt.Sprintf("%s/users/%s", r.Host, uploader.ID),
		"provider_name": "Athena",
		"provider_url":  fmt.Sprintf("https://%s", r.Host),
		"width":         width,
		"height":        height,
		"html": fmt.Sprintf(`<iframe width="%s" height="%s" src="%s/embed/%s" frameborder="0" allowfullscreen></iframe>`,
			width, height, r.Host, video.ID),
	}

	// Add thumbnail if available
	if video.ThumbnailCID != "" {
		oembedResponse["thumbnail_url"] = fmt.Sprintf("%s/ipfs/%s", r.Host, video.ThumbnailCID)
		oembedResponse["thumbnail_width"] = 640
		oembedResponse["thumbnail_height"] = 360
	}

	// Add duration if available
	if video.Duration > 0 {
		oembedResponse["duration"] = video.Duration
	}

	// Handle format
	if format == "xml" {
		w.Header().Set("Content-Type", "application/xml")
		// Simple XML encoding
		xmlResponse := `<?xml version="1.0" encoding="UTF-8"?><oembed>`
		for k, v := range oembedResponse {
			xmlResponse += fmt.Sprintf("<%s>%v</%s>", k, v, k)
		}
		xmlResponse += "</oembed>"
		_, _ = w.Write([]byte(xmlResponse))
	} else {
		WriteJSON(w, http.StatusOK, oembedResponse)
	}
}
