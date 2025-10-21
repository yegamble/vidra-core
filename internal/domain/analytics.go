package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StreamAnalytics represents time-series analytics data for a stream
type StreamAnalytics struct {
	ID          uuid.UUID `json:"id" db:"id"`
	StreamID    uuid.UUID `json:"stream_id" db:"stream_id"`
	CollectedAt time.Time `json:"collected_at" db:"collected_at"`

	// Viewer metrics
	ViewerCount      int `json:"viewer_count" db:"viewer_count"`
	PeakViewerCount  int `json:"peak_viewer_count" db:"peak_viewer_count"`
	UniqueViewers    int `json:"unique_viewers" db:"unique_viewers"`
	AverageWatchTime int `json:"average_watch_time" db:"average_watch_time"` // seconds

	// Engagement metrics
	ChatMessagesCount int `json:"chat_messages_count" db:"chat_messages_count"`
	ChatParticipants  int `json:"chat_participants" db:"chat_participants"`
	LikesCount        int `json:"likes_count" db:"likes_count"`
	SharesCount       int `json:"shares_count" db:"shares_count"`

	// Technical metrics
	Bitrate        *int     `json:"bitrate,omitempty" db:"bitrate"`                 // kbps
	Framerate      *float64 `json:"framerate,omitempty" db:"framerate"`             // fps
	Resolution     string   `json:"resolution,omitempty" db:"resolution"`           // e.g., "1920x1080"
	BufferingRatio *float64 `json:"buffering_ratio,omitempty" db:"buffering_ratio"` // 0-1
	AvgLatency     *int     `json:"avg_latency,omitempty" db:"avg_latency"`         // milliseconds

	// Geographic distribution
	ViewerCountries json.RawMessage `json:"viewer_countries" db:"viewer_countries"` // {"US": 45, "UK": 20}

	// Device/Platform breakdown
	ViewerDevices  json.RawMessage `json:"viewer_devices" db:"viewer_devices"`   // {"desktop": 60, "mobile": 30}
	ViewerBrowsers json.RawMessage `json:"viewer_browsers" db:"viewer_browsers"` // {"chrome": 50, "firefox": 25}

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// StreamStatsSummary represents aggregated statistics for a stream
type StreamStatsSummary struct {
	ID       uuid.UUID `json:"id" db:"id"`
	StreamID uuid.UUID `json:"stream_id" db:"stream_id"`

	// Aggregate metrics
	TotalViewers          int   `json:"total_viewers" db:"total_viewers"`
	PeakConcurrentViewers int   `json:"peak_concurrent_viewers" db:"peak_concurrent_viewers"`
	AverageViewers        int   `json:"average_viewers" db:"average_viewers"`
	TotalWatchTime        int64 `json:"total_watch_time" db:"total_watch_time"`             // seconds
	AverageWatchDuration  int   `json:"average_watch_duration" db:"average_watch_duration"` // seconds

	// Engagement totals
	TotalChatMessages   int     `json:"total_chat_messages" db:"total_chat_messages"`
	TotalUniqueChatters int     `json:"total_unique_chatters" db:"total_unique_chatters"`
	TotalLikes          int     `json:"total_likes" db:"total_likes"`
	TotalShares         int     `json:"total_shares" db:"total_shares"`
	EngagementRate      float64 `json:"engagement_rate" db:"engagement_rate"` // percentage

	// Stream quality metrics
	AverageBitrate   *int     `json:"average_bitrate,omitempty" db:"average_bitrate"`
	AverageFramerate *float64 `json:"average_framerate,omitempty" db:"average_framerate"`
	QualityScore     float64  `json:"quality_score" db:"quality_score"` // 0-100

	// Time-based metrics
	StreamDuration      int        `json:"stream_duration" db:"stream_duration"` // seconds
	FirstViewerJoinedAt *time.Time `json:"first_viewer_joined_at,omitempty" db:"first_viewer_joined_at"`
	PeakTime            *time.Time `json:"peak_time,omitempty" db:"peak_time"`

	// Geographic summary
	TopCountries   json.RawMessage `json:"top_countries" db:"top_countries"` // [{"country": "US", "viewers": 100}]
	CountriesCount int             `json:"countries_count" db:"countries_count"`

	// Platform summary
	TopDevices  json.RawMessage `json:"top_devices" db:"top_devices"`
	TopBrowsers json.RawMessage `json:"top_browsers" db:"top_browsers"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// AnalyticsViewerSession represents an individual viewer session for analytics
type AnalyticsViewerSession struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	StreamID  uuid.UUID  `json:"stream_id" db:"stream_id"`
	UserID    *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID string     `json:"session_id" db:"session_id"`

	JoinedAt      time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt        *time.Time `json:"left_at,omitempty" db:"left_at"`
	WatchDuration *int       `json:"watch_duration,omitempty" db:"watch_duration"` // seconds (computed)

	// Session details
	IPAddress       string `json:"ip_address,omitempty" db:"ip_address"`
	CountryCode     string `json:"country_code,omitempty" db:"country_code"`
	City            string `json:"city,omitempty" db:"city"`
	DeviceType      string `json:"device_type,omitempty" db:"device_type"`
	Browser         string `json:"browser,omitempty" db:"browser"`
	OperatingSystem string `json:"operating_system,omitempty" db:"operating_system"`

	// Engagement during session
	MessagesSent int  `json:"messages_sent" db:"messages_sent"`
	Liked        bool `json:"liked" db:"liked"`
	Shared       bool `json:"shared" db:"shared"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// AnalyticsTimeRange represents a time range for analytics queries
type AnalyticsTimeRange struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Interval  int       `json:"interval_minutes"` // aggregation interval in minutes
}

// AnalyticsDataPoint represents a single data point in time-series analytics
type AnalyticsDataPoint struct {
	Time       time.Time `json:"time" db:"time_bucket"`
	AvgViewers int       `json:"avg_viewers" db:"avg_viewers"`
	MaxViewers int       `json:"max_viewers" db:"max_viewers"`
	Messages   int       `json:"messages" db:"messages"`
	AvgBitrate *int      `json:"avg_bitrate,omitempty" db:"avg_bitrate"`
}

// NewStreamAnalytics creates a new StreamAnalytics instance
func NewStreamAnalytics(streamID uuid.UUID) *StreamAnalytics {
	now := time.Now()
	return &StreamAnalytics{
		ID:              uuid.New(),
		StreamID:        streamID,
		CollectedAt:     now,
		ViewerCountries: json.RawMessage("{}"),
		ViewerDevices:   json.RawMessage("{}"),
		ViewerBrowsers:  json.RawMessage("{}"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// NewAnalyticsViewerSession creates a new viewer session for analytics
func NewAnalyticsViewerSession(streamID uuid.UUID, userID *uuid.UUID, sessionID string) *AnalyticsViewerSession {
	now := time.Now()
	return &AnalyticsViewerSession{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		SessionID: sessionID,
		JoinedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// EndSession marks the session as ended and calculates duration
func (vs *AnalyticsViewerSession) EndSession() {
	now := time.Now()
	vs.LeftAt = &now
	// WatchDuration is computed by the database, no need to set it manually
}

// IsActive returns true if the session is still active
func (vs *AnalyticsViewerSession) IsActive() bool {
	return vs.LeftAt == nil
}

// CalculateEngagementRate calculates the engagement rate as a percentage
func (s *StreamStatsSummary) CalculateEngagementRate() {
	if s.TotalViewers == 0 {
		s.EngagementRate = 0
		return
	}

	engagedUsers := 0
	if s.TotalUniqueChatters > 0 {
		engagedUsers = s.TotalUniqueChatters
	}
	if s.TotalLikes > 0 {
		engagedUsers += s.TotalLikes
	}
	if s.TotalShares > 0 {
		engagedUsers += s.TotalShares
	}

	s.EngagementRate = (float64(engagedUsers) / float64(s.TotalViewers)) * 100
}

// CalculateQualityScore calculates a quality score based on technical metrics
func (s *StreamStatsSummary) CalculateQualityScore() {
	score := 100.0

	// Deduct points for low bitrate
	if s.AverageBitrate != nil {
		if *s.AverageBitrate < 1000 {
			score -= 30
		} else if *s.AverageBitrate < 2500 {
			score -= 15
		} else if *s.AverageBitrate < 4000 {
			score -= 5
		}
	}

	// Deduct points for low framerate
	if s.AverageFramerate != nil {
		if *s.AverageFramerate < 24 {
			score -= 20
		} else if *s.AverageFramerate < 30 {
			score -= 10
		}
	}

	if score < 0 {
		score = 0
	}

	s.QualityScore = score
}

// GetDuration returns the duration of the viewer session
func (vs *AnalyticsViewerSession) GetDuration() time.Duration {
	if vs.LeftAt == nil {
		return time.Since(vs.JoinedAt)
	}
	return vs.LeftAt.Sub(vs.JoinedAt)
}

// CountryViewers represents viewer count by country
type CountryViewers struct {
	Country string `json:"country"`
	Viewers int    `json:"viewers"`
}

// DeviceBreakdown represents viewer device distribution
type DeviceBreakdown map[string]int

// BrowserBreakdown represents viewer browser distribution
type BrowserBreakdown map[string]int
