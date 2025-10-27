package admin

import (
	"athena/internal/httpapi/shared"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"athena/internal/domain"
	"athena/internal/middleware"
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

// WellKnownAtprotoDID serves the instance DID for ATProto handle verification.
// Path: /.well-known/atproto-did
func (h *InstanceHandlers) WellKnownAtprotoDID(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.moderationRepo.GetInstanceConfig(r.Context(), "atproto_did")
	if err != nil {
		// If not configured, return 404 per ATProto expectations
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "ATProto DID not configured"))
		return
	}
	var did string
	if err := json.Unmarshal(cfg.Value, &did); err != nil || strings.TrimSpace(did) == "" {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "ATProto DID not configured"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(did))
}

// GetInstanceAbout handles GET /api/v1/instance/about
func (h *InstanceHandlers) GetInstanceAbout(w http.ResponseWriter, r *http.Request) {
	// Get public configuration values
	configs, err := h.moderationRepo.ListInstanceConfigs(r.Context(), true)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get instance configuration"))
		return
	}

	// Get instance statistics
	totalUsers, totalVideos, totalLocalVideos, totalViews, err := h.moderationRepo.GetInstanceStats(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get instance statistics"))
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

	shared.WriteJSON(w, http.StatusOK, info)
}

// ListInstanceConfigs handles GET /api/v1/admin/instance/config (admin only)
func (h *InstanceHandlers) ListInstanceConfigs(w http.ResponseWriter, r *http.Request) {
	// Admin only
	if role, ok := r.Context().Value(middleware.UserRoleKey).(string); ok {
		if role != string(domain.RoleAdmin) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
			return
		}
	} else {
		// Fallback: check via repository when role not present (tests call handlers directly)
		userIDVal := r.Context().Value(middleware.UserIDKey)
		if userIDVal == nil {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
			return
		}
		role, err := h.moderationRepo.GetUserRole(r.Context(), fmt.Sprintf("%v", userIDVal))
		if err != nil || role != domain.RoleAdmin {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
			return
		}
	}
	configs, err := h.moderationRepo.ListInstanceConfigs(r.Context(), false)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list instance configurations"))
		return
	}
	// Return plain array as data to match tests
	shared.WriteJSON(w, http.StatusOK, configs)
}

// GetInstanceConfig handles GET /api/v1/admin/instance/config/{key} (admin only)
func (h *InstanceHandlers) GetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	// Admin only
	if role, ok := r.Context().Value(middleware.UserRoleKey).(string); ok {
		if role != string(domain.RoleAdmin) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
			return
		}
	} else {
		userIDVal := r.Context().Value(middleware.UserIDKey)
		if userIDVal == nil {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
			return
		}
		role, err := h.moderationRepo.GetUserRole(r.Context(), fmt.Sprintf("%v", userIDVal))
		if err != nil || role != domain.RoleAdmin {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
			return
		}
	}
	key := chi.URLParam(r, "key")
	if key == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing configuration key"))
		return
	}

	config, err := h.moderationRepo.GetInstanceConfig(r.Context(), key)
	if err != nil {
		if domainErr, ok := err.(*domain.DomainError); ok && domainErr.Code == "NOT_FOUND" {
			shared.WriteError(w, http.StatusNotFound, err)
		} else {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get configuration"))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, config)
}

// UpdateInstanceConfig handles PUT /api/v1/admin/instance/config/{key} (admin only)
func (h *InstanceHandlers) UpdateInstanceConfig(w http.ResponseWriter, r *http.Request) {
	// Admin only
	if role, ok := r.Context().Value(middleware.UserRoleKey).(string); ok {
		if role != string(domain.RoleAdmin) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
			return
		}
	} else {
		userIDVal := r.Context().Value(middleware.UserIDKey)
		if userIDVal == nil {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
			return
		}
		role, err := h.moderationRepo.GetUserRole(r.Context(), fmt.Sprintf("%v", userIDVal))
		if err != nil || role != domain.RoleAdmin {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
			return
		}
	}
	key := chi.URLParam(r, "key")
	if key == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing configuration key"))
		return
	}

	var req domain.UpdateInstanceConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	if err := h.moderationRepo.UpdateInstanceConfig(r.Context(), key, req.Value, req.IsPublic); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update configuration"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Configuration updated successfully",
		"success": true,
	})
}

// OEmbed handles GET /api/oembed
func (h *InstanceHandlers) OEmbed(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "URL parameter is required"))
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	} else if format != "json" && format != "xml" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid format parameter"))
		return
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
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid video URL"))
		return
	}

	// Get video details
	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		// Always return 404 for video not found errors
		// Since we're looking up a specific video ID, any error likely means it doesn't exist
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
		return
	}

	// Check if video is private
	if video.Privacy != domain.PrivacyPublic {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
		return
	}

	// Get uploader info
	uploader, err := h.userRepo.GetByID(r.Context(), video.UserID)
	if err != nil {
		uploader = &domain.User{
			ID:          video.UserID, // Keep the original user ID
			Username:    "Unknown",
			DisplayName: "Unknown User",
		}
	}

	// Build oEmbed response
	width := 640
	height := 360
	if maxWidth != "" {
		if w, err := strconv.Atoi(maxWidth); err == nil && w > 0 {
			width = w
		}
	}
	if maxHeight != "" {
		if h, err := strconv.Atoi(maxHeight); err == nil && h > 0 {
			height = h
		}
	}

	oembedResponse := map[string]interface{}{
		"version":       "1.0",
		"type":          "video",
		"title":         video.Title,
		"author_name":   uploader.DisplayName,
		"author_url":    fmt.Sprintf("%s/users/%s", r.Host, uploader.ID),
		"provider_name": "Athena Video Platform",
		"provider_url":  fmt.Sprintf("https://%s", r.Host),
		"width":         width,
		"height":        height,
		"html": fmt.Sprintf(`<iframe width="%d" height="%d" src="%s/embed/%s" frameborder="0" allowfullscreen></iframe>`,
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
		// Simple XML encoding - need to escape HTML content
		xmlResponse := `<?xml version="1.0" encoding="UTF-8"?><oembed>`
		for k, v := range oembedResponse {
			// HTML needs to be escaped in XML
			valueStr := fmt.Sprintf("%v", v)
			if k == "html" {
				// Escape HTML for XML
				valueStr = strings.ReplaceAll(valueStr, "&", "&amp;")
				valueStr = strings.ReplaceAll(valueStr, "<", "&lt;")
				valueStr = strings.ReplaceAll(valueStr, ">", "&gt;")
				valueStr = strings.ReplaceAll(valueStr, "\"", "&quot;")
			}
			xmlResponse += fmt.Sprintf("<%s>%s</%s>", k, valueStr, k)
		}
		xmlResponse += "</oembed>"
		_, _ = w.Write([]byte(xmlResponse))
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(oembedResponse)
	}
}
