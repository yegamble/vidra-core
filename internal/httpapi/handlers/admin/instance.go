package admin

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"vidra-core/internal/httpapi/shared"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	"vidra-core/internal/usecase"

	"github.com/go-chi/chi/v5"
)

// InstanceHandlers handles instance-related HTTP requests
type InstanceHandlers struct {
	moderationRepo *repository.ModerationRepository
	userRepo       usecase.UserRepository
	videoRepo      usecase.VideoRepository
}

// OEmbedResponse defines the structure for oEmbed responses (both JSON and XML)
type OEmbedResponse struct {
	XMLName         xml.Name `json:"-" xml:"oembed"`
	Version         string   `json:"version" xml:"version"`
	Type            string   `json:"type" xml:"type"`
	Title           string   `json:"title" xml:"title"`
	AuthorName      string   `json:"author_name" xml:"author_name"`
	AuthorURL       string   `json:"author_url" xml:"author_url"`
	ProviderName    string   `json:"provider_name" xml:"provider_name"`
	ProviderURL     string   `json:"provider_url" xml:"provider_url"`
	Width           int      `json:"width" xml:"width"`
	Height          int      `json:"height" xml:"height"`
	HTML            string   `json:"html" xml:"html"`
	ThumbnailURL    string   `json:"thumbnail_url,omitempty" xml:"thumbnail_url,omitempty"`
	ThumbnailWidth  int      `json:"thumbnail_width,omitempty" xml:"thumbnail_width,omitempty"`
	ThumbnailHeight int      `json:"thumbnail_height,omitempty" xml:"thumbnail_height,omitempty"`
	Duration        int      `json:"duration,omitempty" xml:"duration,omitempty"`
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
		Name:               "Vidra Core Instance",
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
		info.Name = "Vidra Core Instance"
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

	resp := OEmbedResponse{
		Version:      "1.0",
		Type:         "video",
		Title:        video.Title,
		AuthorName:   uploader.DisplayName,
		AuthorURL:    fmt.Sprintf("%s/users/%s", r.Host, uploader.ID),
		ProviderName: "Vidra Core Video Platform",
		ProviderURL:  fmt.Sprintf("https://%s", r.Host),
		Width:        width,
		Height:       height,
		HTML: fmt.Sprintf(`<iframe width="%d" height="%d" src="%s/embed/%s" frameborder="0" allowfullscreen></iframe>`,
			width, height, r.Host, video.ID),
	}

	// Add thumbnail if available
	if video.ThumbnailCID != "" {
		resp.ThumbnailURL = fmt.Sprintf("%s/ipfs/%s", r.Host, video.ThumbnailCID)
		resp.ThumbnailWidth = 640
		resp.ThumbnailHeight = 360
	}

	// Add duration if available
	if video.Duration > 0 {
		resp.Duration = video.Duration
	}

	// Handle format
	if format == "xml" {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(xml.Header))
		if err := xml.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("Failed to encode XML response: %v", err)
		}
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("Failed to encode JSON response: %v", err)
		}
	}
}
