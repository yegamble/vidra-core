package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserView represents a single view session for detailed tracking and analytics
type UserView struct {
	ID      string  `json:"id" db:"id"`
	VideoID string  `json:"video_id" db:"video_id"`
	UserID  *string `json:"user_id,omitempty" db:"user_id"` // Nullable for anonymous views

	// Session and deduplication tracking
	SessionID       string `json:"session_id" db:"session_id"`             // Client-generated UUID
	FingerprintHash string `json:"fingerprint_hash" db:"fingerprint_hash"` // Hash for anonymous deduplication

	// Engagement metrics
	WatchDuration        int     `json:"watch_duration" db:"watch_duration"`               // Seconds watched
	VideoDuration        int     `json:"video_duration" db:"video_duration"`               // Total video duration
	CompletionPercentage float64 `json:"completion_percentage" db:"completion_percentage"` // 0.00-100.00
	IsCompleted          bool    `json:"is_completed" db:"is_completed"`                   // Watched >= 95%

	// Interaction metrics
	SeekCount      int `json:"seek_count" db:"seek_count"`           // Number of seeks/jumps
	PauseCount     int `json:"pause_count" db:"pause_count"`         // Number of pauses
	ReplayCount    int `json:"replay_count" db:"replay_count"`       // Number of replays
	QualityChanges int `json:"quality_changes" db:"quality_changes"` // Quality setting changes

	// Technical metrics
	InitialLoadTime *int    `json:"initial_load_time,omitempty" db:"initial_load_time"` // Milliseconds to first frame
	BufferEvents    int     `json:"buffer_events" db:"buffer_events"`                   // Number of buffering events
	ConnectionType  *string `json:"connection_type,omitempty" db:"connection_type"`     // 'wifi', 'cellular', 'ethernet'
	VideoQuality    *string `json:"video_quality,omitempty" db:"video_quality"`         // '360p', '720p', '1080p'

	// Context and attribution
	ReferrerURL  string `json:"referrer_url,omitempty" db:"referrer_url"`   // Truncated for privacy
	ReferrerType string `json:"referrer_type,omitempty" db:"referrer_type"` // 'search', 'social', 'direct'
	UTMSource    string `json:"utm_source,omitempty" db:"utm_source"`       // Marketing attribution
	UTMMedium    string `json:"utm_medium,omitempty" db:"utm_medium"`
	UTMCampaign  string `json:"utm_campaign,omitempty" db:"utm_campaign"`

	// Device and environment
	DeviceType       string `json:"device_type,omitempty" db:"device_type"`             // 'desktop', 'mobile', 'tablet', 'tv'
	OSName           string `json:"os_name,omitempty" db:"os_name"`                     // 'Windows', 'macOS', 'iOS', 'Android'
	BrowserName      string `json:"browser_name,omitempty" db:"browser_name"`           // 'Chrome', 'Firefox', 'Safari'
	ScreenResolution string `json:"screen_resolution,omitempty" db:"screen_resolution"` // '1920x1080'
	IsMobile         bool   `json:"is_mobile" db:"is_mobile"`

	// Geographic data
	CountryCode string `json:"country_code,omitempty" db:"country_code"` // ISO 3166-1 alpha-2
	RegionCode  string `json:"region_code,omitempty" db:"region_code"`   // State/province code
	CityName    string `json:"city_name,omitempty" db:"city_name"`       // City name (optional)
	Timezone    string `json:"timezone,omitempty" db:"timezone"`         // IANA timezone

	// Privacy and consent
	IsAnonymous     bool  `json:"is_anonymous" db:"is_anonymous"`           // User opted for anonymous tracking
	TrackingConsent bool  `json:"tracking_consent" db:"tracking_consent"`   // User consented to tracking
	GDPRConsent     *bool `json:"gdpr_consent,omitempty" db:"gdpr_consent"` // GDPR consent where applicable

	// Temporal data for analytics
	ViewDate time.Time `json:"view_date" db:"view_date"` // Partitioning key
	ViewHour int       `json:"view_hour" db:"view_hour"` // 0-23 for hourly analytics
	Weekday  int       `json:"weekday" db:"weekday"`     // 0-6 (Sunday-Saturday)

	// Metadata
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// DailyVideoStats represents aggregated daily statistics for a video
type DailyVideoStats struct {
	ID       string    `json:"id" db:"id"`
	VideoID  string    `json:"video_id" db:"video_id"`
	StatDate time.Time `json:"stat_date" db:"stat_date"`

	// Core metrics
	TotalViews         int64 `json:"total_views" db:"total_views"`
	UniqueViews        int64 `json:"unique_views" db:"unique_views"`
	AuthenticatedViews int64 `json:"authenticated_views" db:"authenticated_views"`
	AnonymousViews     int64 `json:"anonymous_views" db:"anonymous_views"`

	// Engagement metrics
	TotalWatchTime          int64   `json:"total_watch_time" db:"total_watch_time"`
	AvgWatchDuration        float64 `json:"avg_watch_duration" db:"avg_watch_duration"`
	AvgCompletionPercentage float64 `json:"avg_completion_percentage" db:"avg_completion_percentage"`
	CompletedViews          int64   `json:"completed_views" db:"completed_views"`

	// Quality metrics
	AvgInitialLoadTime *float64 `json:"avg_initial_load_time,omitempty" db:"avg_initial_load_time"`
	TotalBufferEvents  int64    `json:"total_buffer_events" db:"total_buffer_events"`
	AvgSeekCount       float64  `json:"avg_seek_count" db:"avg_seek_count"`

	// Device breakdown
	DesktopViews int64 `json:"desktop_views" db:"desktop_views"`
	MobileViews  int64 `json:"mobile_views" db:"mobile_views"`
	TabletViews  int64 `json:"tablet_views" db:"tablet_views"`
	TVViews      int64 `json:"tv_views" db:"tv_views"`

	// Geographic and traffic data (stored as JSON)
	TopCountries      interface{} `json:"top_countries" db:"top_countries"`           // JSONB array
	TopRegions        interface{} `json:"top_regions" db:"top_regions"`               // JSONB array
	ReferrerBreakdown interface{} `json:"referrer_breakdown" db:"referrer_breakdown"` // JSONB object

	// Metadata
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// UserEngagementStats represents user-level engagement metrics
type UserEngagementStats struct {
	ID       string    `json:"id" db:"id"`
	UserID   string    `json:"user_id" db:"user_id"`
	StatDate time.Time `json:"stat_date" db:"stat_date"`

	// Viewing behavior
	VideosWatched       int64   `json:"videos_watched" db:"videos_watched"`
	TotalWatchTime      int64   `json:"total_watch_time" db:"total_watch_time"`
	AvgSessionDuration  float64 `json:"avg_session_duration" db:"avg_session_duration"`
	UniqueVideosWatched int64   `json:"unique_videos_watched" db:"unique_videos_watched"`

	// Engagement patterns
	AvgCompletionRate float64 `json:"avg_completion_rate" db:"avg_completion_rate"`
	CompletedVideos   int64   `json:"completed_videos" db:"completed_videos"`
	SessionsCount     int64   `json:"sessions_count" db:"sessions_count"`

	// Behavioral metrics
	TotalSeeks   int64 `json:"total_seeks" db:"total_seeks"`
	TotalPauses  int64 `json:"total_pauses" db:"total_pauses"`
	TotalReplays int64 `json:"total_replays" db:"total_replays"`

	// Device preferences
	PreferredDevice string `json:"preferred_device,omitempty" db:"preferred_device"`
	DeviceDiversity int    `json:"device_diversity" db:"device_diversity"`

	// Content preferences (stored as JSON)
	TopCategories              interface{} `json:"top_categories" db:"top_categories"`
	AvgVideoDurationPreference *float64    `json:"avg_video_duration_preference,omitempty" db:"avg_video_duration_preference"`

	// Metadata
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// TrendingVideo represents real-time trending data
type TrendingVideo struct {
	VideoID string `json:"video_id" db:"video_id"`

	// Trending metrics
	ViewsLastHour int64 `json:"views_last_hour" db:"views_last_hour"`
	ViewsLast24h  int64 `json:"views_last_24h" db:"views_last_24h"`
	ViewsLast7d   int64 `json:"views_last_7d" db:"views_last_7d"`

	// Engagement velocity
	EngagementScore float64 `json:"engagement_score" db:"engagement_score"`
	VelocityScore   float64 `json:"velocity_score" db:"velocity_score"`

	// Rankings
	HourlyRank *int `json:"hourly_rank,omitempty" db:"hourly_rank"`
	DailyRank  *int `json:"daily_rank,omitempty" db:"daily_rank"`
	WeeklyRank *int `json:"weekly_rank,omitempty" db:"weekly_rank"`

	// Metadata
	LastUpdated time.Time `json:"last_updated" db:"last_updated"`
	IsTrending  bool      `json:"is_trending" db:"is_trending"`
}

// ViewAnalyticsFilter represents filters for analytics queries
type ViewAnalyticsFilter struct {
	VideoID     string     `json:"video_id,omitempty"`
	UserID      string     `json:"user_id,omitempty"`
	StartDate   *time.Time `json:"start_date,omitempty"`
	EndDate     *time.Time `json:"end_date,omitempty"`
	CountryCode string     `json:"country_code,omitempty"`
	DeviceType  string     `json:"device_type,omitempty"`
	IsAnonymous *bool      `json:"is_anonymous,omitempty"`
	Limit       int        `json:"limit,omitempty"`
	Offset      int        `json:"offset,omitempty"`
}

// ViewAnalyticsResponse represents the response for analytics queries
type ViewAnalyticsResponse struct {
	TotalViews        int64            `json:"total_views"`
	UniqueViews       int64            `json:"unique_views"`
	AvgDuration       float64          `json:"avg_duration"`
	AvgWatchDuration  float64          `json:"avg_watch_duration"`
	Completion        float64          `json:"completion_rate"`
	AvgCompletionRate float64          `json:"avg_completion_rate"`
	CompletedViews    int64            `json:"completed_views"`
	TotalWatchTime    int64            `json:"total_watch_time"`
	DeviceStats       map[string]int64 `json:"device_stats"`
	DeviceBreakdown   map[string]int64 `json:"device_breakdown"`
	GeoStats          map[string]int64 `json:"geo_stats"`
	CountryBreakdown  map[string]int64 `json:"country_breakdown"`
	HourlyStats       map[int]int64    `json:"hourly_stats"`
	DailyStats        []DailyViewStats `json:"daily_stats"`
}

// DailyViewStats represents daily view statistics for charts
type DailyViewStats struct {
	Date        time.Time `json:"date"`
	Views       int64     `json:"views"`
	UniqueViews int64     `json:"unique_views"`
	WatchTime   int64     `json:"watch_time"`
	Completion  float64   `json:"completion"`
}

// CreateUserViewRequest represents the request to create a new view
type CreateUserViewRequest struct {
	VideoID     string  `json:"video_id" validate:"required"`
	UserID      *string `json:"user_id,omitempty"`
	SessionID   string  `json:"session_id,omitempty"`
	Fingerprint string  `json:"fingerprint" validate:"required"`
	Timestamp   int64   `json:"timestamp,omitempty"`

	// Optional initial context
	DeviceType     string  `json:"device_type,omitempty"`
	CountryCode    string  `json:"country_code,omitempty"`
	ReferrerType   string  `json:"referrer_type,omitempty"`
	ConnectionType *string `json:"connection_type,omitempty"`
}

// UpdateViewSessionRequest represents updates to a view session
type UpdateViewSessionRequest struct {
	SessionID            string   `json:"session_id" validate:"required"`
	VideoID              string   `json:"video_id" validate:"required"`
	WatchDuration        *int     `json:"watch_duration,omitempty"`
	CompletionPercentage *float64 `json:"completion_percentage,omitempty"`
	SeekCount            *int     `json:"seek_count,omitempty"`
	PauseCount           *int     `json:"pause_count,omitempty"`
	QualityChanges       *int     `json:"quality_changes,omitempty"`
	BufferEvents         *int     `json:"buffer_events,omitempty"`
}

// ViewTrackingResponse represents the response after tracking a view
type ViewTrackingResponse struct {
	Success   bool   `json:"success"`
	ViewID    string `json:"view_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	IsNewView bool   `json:"is_new_view"`
	Message   string `json:"message,omitempty"`
}

// AnalyticsDashboardResponse represents dashboard analytics
type AnalyticsDashboardResponse struct {
	TotalVideos     int64            `json:"total_videos"`
	TotalViews      int64            `json:"total_views"`
	UniqueViewers   int64            `json:"unique_viewers"`
	TotalWatchTime  int64            `json:"total_watch_time"` // In seconds
	AvgViewDuration float64          `json:"avg_view_duration"`
	CompletionRate  float64          `json:"completion_rate"`
	TopVideos       []VideoStats     `json:"top_videos"`
	RecentActivity  []DailyViewStats `json:"recent_activity"`
	DeviceBreakdown map[string]int64 `json:"device_breakdown"`
	GeoBreakdown    map[string]int64 `json:"geo_breakdown"`
	TrafficSources  map[string]int64 `json:"traffic_sources"`
}

// VideoStats represents statistics for a single video
type VideoStats struct {
	VideoID         string  `json:"video_id" db:"video_id"`
	Title           string  `json:"title,omitempty"`
	TotalViews      int64   `json:"total_views" db:"total_views"`
	UniqueViews     int64   `json:"unique_views" db:"unique_views"`
	AvgDuration     float64 `json:"avg_duration" db:"avg_duration"`
	CompletionRate  float64 `json:"completion_rate" db:"completion_rate"`
	EngagementScore float64 `json:"engagement_score,omitempty"`
}

// VideoTrendingStats holds raw metrics for trending calculation
type VideoTrendingStats struct {
	VideoID       string  `db:"video_id"`
	ViewsLastHour int64   `db:"views_last_hour"`
	ViewsLast24h  int64   `db:"views_last_24h"`
	ViewsLast7d   int64   `db:"views_last_7d"`
	Score1h       float64 `db:"score_1h"`
	Score24h      float64 `db:"score_24h"`
	Score7d       float64 `db:"score_7d"`
}

// Helper methods for UserView

// GenerateSessionID creates a new session ID if not provided
func (uv *UserView) GenerateSessionID() {
	if uv.SessionID == "" {
		uv.SessionID = uuid.New().String()
	}
}

// CalculateCompletion calculates completion percentage from watch duration
func (uv *UserView) CalculateCompletion() {
	if uv.VideoDuration > 0 {
		uv.CompletionPercentage = (float64(uv.WatchDuration) / float64(uv.VideoDuration)) * 100.0
		if uv.CompletionPercentage > 100.0 {
			uv.CompletionPercentage = 100.0
		}
		uv.IsCompleted = uv.CompletionPercentage >= 95.0
	}
}

// SetViewDate sets the view date and related temporal fields
func (uv *UserView) SetViewDate(t time.Time) {
	uv.ViewDate = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	uv.ViewHour = t.Hour()
	uv.Weekday = int(t.Weekday())
}

// ViewTrackingRequest represents the request to track a view
type ViewTrackingRequest struct {
	VideoID         string  `json:"video_id" validate:"required"`
	UserID          *string `json:"user_id,omitempty"`
	SessionID       string  `json:"session_id,omitempty"`
	FingerprintHash string  `json:"fingerprint" validate:"required"`

	// Engagement metrics
	WatchDuration        int     `json:"watch_duration,omitempty"`
	VideoDuration        int     `json:"video_duration,omitempty"`
	CompletionPercentage float64 `json:"completion_percentage,omitempty"`
	IsCompleted          bool    `json:"is_completed,omitempty"`

	// Interaction metrics
	SeekCount      int `json:"seek_count,omitempty"`
	PauseCount     int `json:"pause_count,omitempty"`
	ReplayCount    int `json:"replay_count,omitempty"`
	QualityChanges int `json:"quality_changes,omitempty"`
	BufferEvents   int `json:"buffer_events,omitempty"`

	// Technical metrics
	InitialLoadTime *int   `json:"initial_load_time,omitempty"`
	VideoQuality    string `json:"video_quality,omitempty"`

	// Context and attribution
	ReferrerURL  string `json:"referrer_url,omitempty"`
	ReferrerType string `json:"referrer_type,omitempty"`
	UTMSource    string `json:"utm_source,omitempty"`
	UTMMedium    string `json:"utm_medium,omitempty"`
	UTMCampaign  string `json:"utm_campaign,omitempty"`

	// Device and environment
	DeviceType       string `json:"device_type,omitempty"`
	OSName           string `json:"os_name,omitempty"`
	BrowserName      string `json:"browser_name,omitempty"`
	ScreenResolution string `json:"screen_resolution,omitempty"`
	IsMobile         bool   `json:"is_mobile,omitempty"`

	// Geographic data
	CountryCode string `json:"country_code,omitempty"`
	RegionCode  string `json:"region_code,omitempty"`
	CityName    string `json:"city_name,omitempty"`
	Timezone    string `json:"timezone,omitempty"`

	// Privacy and consent
	IsAnonymous     bool  `json:"is_anonymous,omitempty"`
	TrackingConsent bool  `json:"tracking_consent,omitempty"`
	GDPRConsent     *bool `json:"gdpr_consent,omitempty"`

	ConnectionType *string `json:"connection_type,omitempty"`
	Timestamp      int64   `json:"timestamp,omitempty"`
}

// TrendingVideosResponse represents the response for trending videos
type TrendingVideosResponse struct {
	Videos     []TrendingVideoWithDetails `json:"videos"`
	TotalCount int                        `json:"total_count"`
	Page       int                        `json:"page"`
	Limit      int                        `json:"limit"`
	UpdatedAt  time.Time                  `json:"updated_at"`
}

// TrendingVideoWithDetails represents a trending video with additional details
type TrendingVideoWithDetails struct {
	VideoID         string         `json:"video_id"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	ThumbnailURL    string         `json:"thumbnail_url"`
	Duration        int            `json:"duration"`
	Views           int64          `json:"views"`
	EngagementScore float64        `json:"engagement_score"`
	VelocityScore   float64        `json:"velocity_score"`
	Rank            int            `json:"rank"`
	CreatedAt       time.Time      `json:"created_at"`
	TrendingVideo   *TrendingVideo `json:"trending_video,omitempty"`
	Video           *Video         `json:"video,omitempty"`
}

// IsValidForTracking checks if the view has required fields for tracking
func (uv *UserView) IsValidForTracking() bool {
	return uv.VideoID != "" && uv.FingerprintHash != ""
}
