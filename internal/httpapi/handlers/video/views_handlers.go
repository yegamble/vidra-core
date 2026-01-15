package video

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	ucviews "athena/internal/usecase/views"
)

// ViewsHandler handles all views tracking and analytics endpoints
type ViewsHandler struct {
	viewsService *ucviews.Service
}

// NewViewsHandler creates a new views handler
func NewViewsHandler(viewsService *ucviews.Service) *ViewsHandler {
	return &ViewsHandler{
		viewsService: viewsService,
	}
}

// TrackView handles POST /api/v1/videos/{videoId}/views
// Tracks a user view with comprehensive metrics and deduplication
func (h *ViewsHandler) TrackView(w http.ResponseWriter, r *http.Request) {
	videoID, ok := shared.RequireUUIDParam(w, r, "videoId", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
	if !ok {
		return
	}

	var request domain.ViewTrackingRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	// Set video ID from URL parameter
	request.VideoID = videoID

	// Validate the request
	if err := ucviews.ValidateTrackingRequest(&request); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", err.Error()))
		return
	}

	// Get user ID from context (will be nil for anonymous users)
	var userID *string
	if userIDValue := r.Context().Value(middleware.UserIDKey); userIDValue != nil {
		if uid, ok := userIDValue.(string); ok && uid != "" {
			userID = &uid
		}
	}

	// Track the view
	err := h.viewsService.TrackView(r.Context(), userID, &request)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TRACKING_FAILED", "Failed to track view"))
		return
	}

	// Return success response
	response := map[string]interface{}{
		"success":  true,
		"message":  "View tracked successfully",
		"video_id": videoID,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetVideoAnalytics handles GET /api/v1/videos/{videoId}/analytics
// Returns comprehensive analytics for a specific video
func (h *ViewsHandler) GetVideoAnalytics(w http.ResponseWriter, r *http.Request) {
	videoID, ok := shared.RequireUUIDParam(w, r, "videoId", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
	if !ok {
		return
	}

	// Parse query parameters for filtering
	filter := &domain.ViewAnalyticsFilter{
		VideoID: videoID,
		Limit:   100, // Default limit
		Offset:  0,
	}

	// Parse date range
	if startDateStr := r.URL.Query().Get("start_date"); startDateStr != "" {
		startDate, err := time.Parse("2006-01-02", startDateStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DATE", "Invalid start_date format. Use YYYY-MM-DD"))
			return
		}
		filter.StartDate = &startDate
	}

	if endDateStr := r.URL.Query().Get("end_date"); endDateStr != "" {
		endDate, err := time.Parse("2006-01-02", endDateStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DATE", "Invalid end_date format. Use YYYY-MM-DD"))
			return
		}
		filter.EndDate = &endDate
	}

	// Parse device type filter
	if deviceType := r.URL.Query().Get("device_type"); deviceType != "" {
		filter.DeviceType = deviceType
	}

	// Parse country code filter
	if countryCode := r.URL.Query().Get("country_code"); countryCode != "" {
		filter.CountryCode = countryCode
	}

	// Parse anonymous filter
	if isAnonymousStr := r.URL.Query().Get("is_anonymous"); isAnonymousStr != "" {
		isAnonymous, err := strconv.ParseBool(isAnonymousStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_BOOLEAN", "Invalid is_anonymous value"))
			return
		}
		filter.IsAnonymous = &isAnonymous
	}

	analytics, err := h.viewsService.GetVideoAnalytics(r.Context(), filter)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("ANALYTICS_FAILED", "Failed to get video analytics"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, analytics)
}

// GetDailyStats handles GET /api/v1/videos/{videoId}/stats/daily
// Returns pre-aggregated daily statistics for a video
func (h *ViewsHandler) GetDailyStats(w http.ResponseWriter, r *http.Request) {
	videoID, ok := shared.RequireUUIDParam(w, r, "videoId", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
	if !ok {
		return
	}

	// Parse days parameter (default to 30 days)
	days := 30
	if daysStr := r.URL.Query().Get("days"); daysStr != "" {
		parsedDays, err := strconv.Atoi(daysStr)
		if err != nil || parsedDays <= 0 || parsedDays > 365 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DAYS", "Days must be between 1 and 365"))
			return
		}
		days = parsedDays
	}

	stats, err := h.viewsService.GetDailyStats(r.Context(), videoID, days)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STATS_FAILED", "Failed to get daily stats"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"video_id": videoID,
		"days":     days,
		"stats":    stats,
	})
}

// GetUserEngagement handles GET /api/v1/users/{userId}/engagement
// Returns user-level engagement statistics (requires authentication)
func (h *ViewsHandler) GetUserEngagement(w http.ResponseWriter, r *http.Request) {
	userID, ok := shared.RequireUUIDParam(w, r, "userId", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
	if !ok {
		return
	}

	// Check if the authenticated user is requesting their own data or is an admin
	authUserID := r.Context().Value(middleware.UserIDKey)
	if authUserID == nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	authUserRole := r.Context().Value(middleware.UserRoleKey)
	isAdmin := authUserRole != nil && authUserRole.(string) == "admin"

	if authUserID.(string) != userID && !isAdmin {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot access other user's engagement data"))
		return
	}

	// Parse days parameter (default to 30 days)
	days := 30
	if daysStr := r.URL.Query().Get("days"); daysStr != "" {
		parsedDays, err := strconv.Atoi(daysStr)
		if err != nil || parsedDays <= 0 || parsedDays > 365 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DAYS", "Days must be between 1 and 365"))
			return
		}
		days = parsedDays
	}

	stats, err := h.viewsService.GetUserEngagement(r.Context(), userID, days)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("ENGAGEMENT_FAILED", "Failed to get user engagement"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":    userID,
		"days":       days,
		"engagement": stats,
	})
}

// GetTrendingVideos handles GET /api/v1/trending
// Returns currently trending videos
func (h *ViewsHandler) GetTrendingVideos(w http.ResponseWriter, r *http.Request) {
	// Unified page/pageSize while keeping limit for backward compatibility.
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	limit := 0
	if pageSize <= 0 || pageSize > 100 {
		// fallback to limit
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			parsedLimit, err := strconv.Atoi(limitStr)
			if err != nil || parsedLimit <= 0 {
				shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_LIMIT", "Limit must be a positive integer"))
				return
			}
			if parsedLimit > 100 {
				parsedLimit = 100
			}
			pageSize = parsedLimit
		} else {
			pageSize = 50
		}
	}
	if page <= 0 {
		page = 1 // trending repository currently does not support offset
	}
	limit = pageSize

	// Check if detailed response is requested
	includeDetails := r.URL.Query().Get("include_details") == "true"

	if includeDetails {
		trendingWithDetails, err := h.viewsService.GetTrendingVideosWithDetails(r.Context(), limit)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TRENDING_FAILED", "Failed to get trending videos"))
			return
		}
		// Attach meta with page/pageSize for consistency (total unknown)
		meta := &shared.Meta{Total: 0, Limit: limit, Offset: 0, Page: page, PageSize: pageSize}
		shared.WriteJSONWithMeta(w, http.StatusOK, trendingWithDetails, meta)
	} else {
		trending, err := h.viewsService.GetTrendingVideos(r.Context(), limit)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TRENDING_FAILED", "Failed to get trending videos"))
			return
		}
		data := map[string]interface{}{
			"videos":     trending,
			"limit":      limit,
			"updated_at": time.Now(),
		}
		meta := &shared.Meta{Total: 0, Limit: limit, Offset: 0, Page: page, PageSize: pageSize}
		shared.WriteJSONWithMeta(w, http.StatusOK, data, meta)
	}
}

// GetTopVideos handles GET /api/v1/videos/top
// Returns most viewed videos in a time period
func (h *ViewsHandler) GetTopVideos(w http.ResponseWriter, r *http.Request) {
	// Parse days parameter (default to 7 days)
	days := 7
	if daysStr := r.URL.Query().Get("days"); daysStr != "" {
		parsedDays, err := strconv.Atoi(daysStr)
		if err != nil || parsedDays <= 0 || parsedDays > 365 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DAYS", "Days must be between 1 and 365"))
			return
		}
		days = parsedDays
	}

	// Parse limit parameter (default to 20, max 100)
	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_LIMIT", "Limit must be a positive integer"))
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	topVideos, err := h.viewsService.GetTopVideos(r.Context(), days, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("TOP_VIDEOS_FAILED", "Failed to get top videos"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"videos":      topVideos,
		"period_days": days,
		"limit":       limit,
	})
}

// GetViewHistory handles GET /api/v1/views/history
// Returns view history with filtering options (admin only or own data)
func (h *ViewsHandler) GetViewHistory(w http.ResponseWriter, r *http.Request) {
	// Parse filtering options
	filter := &domain.ViewAnalyticsFilter{
		Limit:  100, // Default limit
		Offset: 0,
	}

	// Parse video ID filter
	if videoID := r.URL.Query().Get("video_id"); videoID != "" {
		filter.VideoID = videoID
	}

	// Parse user ID filter (with authorization check)
	if userIDFilter := r.URL.Query().Get("user_id"); userIDFilter != "" {
		authUserID := r.Context().Value(middleware.UserIDKey)
		if authUserID == nil {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		if authUserID.(string) != userIDFilter {
			// TODO: Add admin role check here if needed
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Cannot access other user's view history"))
			return
		}
		filter.UserID = userIDFilter
	}

	// Parse date range
	if startDateStr := r.URL.Query().Get("start_date"); startDateStr != "" {
		startDate, err := time.Parse("2006-01-02", startDateStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DATE", "Invalid start_date format. Use YYYY-MM-DD"))
			return
		}
		filter.StartDate = &startDate
	}

	if endDateStr := r.URL.Query().Get("end_date"); endDateStr != "" {
		endDate, err := time.Parse("2006-01-02", endDateStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DATE", "Invalid end_date format. Use YYYY-MM-DD"))
			return
		}
		filter.EndDate = &endDate
	}

	// Parse pagination
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_LIMIT", "Limit must be a positive integer"))
			return
		}
		if limit > 1000 {
			limit = 1000 // Cap at 1000
		}
		filter.Limit = limit
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_OFFSET", "Offset must be a non-negative integer"))
			return
		}
		filter.Offset = offset
	}

	views, err := h.viewsService.GetViewHistory(r.Context(), filter)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("HISTORY_FAILED", "Failed to get view history"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"views":  views,
		"filter": filter,
		"count":  len(views),
	})
}

// GenerateFingerprint handles POST /api/v1/views/fingerprint
// Generates a privacy-compliant fingerprint for view deduplication
func (h *ViewsHandler) GenerateFingerprint(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IP        string `json:"ip"`
		UserAgent string `json:"user_agent"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if request.IP == "" || request.UserAgent == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "IP and user_agent are required"))
		return
	}

	fingerprint := ucviews.GenerateFingerprint(request.IP, request.UserAgent)

	response := map[string]interface{}{
		"fingerprint_hash": fingerprint,
		"created_at":       time.Now(),
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// AdminAggregateStats handles POST /api/v1/admin/views/aggregate
// Triggers manual daily stats aggregation (admin only)
func (h *ViewsHandler) AdminAggregateStats(w http.ResponseWriter, r *http.Request) {
	// TODO: Add admin authorization check here

	var request struct {
		Date *string `json:"date,omitempty"` // Optional date in YYYY-MM-DD format
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	var aggregateDate *time.Time
	if request.Date != nil && *request.Date != "" {
		date, err := time.Parse("2006-01-02", *request.Date)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DATE", "Invalid date format. Use YYYY-MM-DD"))
			return
		}
		aggregateDate = &date
	}

	err := h.viewsService.AggregateStats(r.Context(), aggregateDate)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("AGGREGATION_FAILED", "Failed to aggregate stats"))
		return
	}

	targetDate := time.Now().AddDate(0, 0, -1) // Yesterday by default
	if aggregateDate != nil {
		targetDate = *aggregateDate
	}

	response := map[string]interface{}{
		"success":       true,
		"message":       "Stats aggregated successfully",
		"date":          targetDate.Format("2006-01-02"),
		"aggregated_at": time.Now(),
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// AdminCleanupOldData handles POST /api/v1/admin/views/cleanup
// Triggers cleanup of old view data (admin only)
func (h *ViewsHandler) AdminCleanupOldData(w http.ResponseWriter, r *http.Request) {
	// TODO: Add admin authorization check here

	var request struct {
		DaysToKeep int `json:"days_to_keep"` // Number of days to keep
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if request.DaysToKeep <= 0 || request.DaysToKeep > 3650 { // Max 10 years
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DAYS", "Days to keep must be between 1 and 3650"))
		return
	}

	err := h.viewsService.CleanupOldData(r.Context(), request.DaysToKeep)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CLEANUP_FAILED", "Failed to cleanup old data"))
		return
	}

	response := map[string]interface{}{
		"success":    true,
		"message":    fmt.Sprintf("Old data cleanup completed. Kept last %d days", request.DaysToKeep),
		"days_kept":  request.DaysToKeep,
		"cleaned_at": time.Now(),
	}

	shared.WriteJSON(w, http.StatusOK, response)
}
