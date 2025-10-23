package domain

import (
	"encoding/json"
	"errors"
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

// ======================================================================
// Video Analytics (VOD) Models
// ======================================================================

// Video analytics domain errors
var (
	ErrInvalidEventType       = errors.New("invalid event type")
	ErrInvalidDeviceType      = errors.New("invalid device type")
	ErrInvalidSessionID       = errors.New("session ID cannot be empty")
	ErrInvalidTimestamp       = errors.New("timestamp cannot be negative")
	ErrInvalidWatchDuration   = errors.New("watch duration cannot be negative")
	ErrInvalidPercentage      = errors.New("percentage must be between 0 and 100")
	ErrInvalidViewerCount     = errors.New("viewer count cannot be negative")
	ErrInvalidDate            = errors.New("invalid date")
	ErrAnalyticsEventNotFound = errors.New("analytics event not found")
	ErrAnalyticsDailyNotFound = errors.New("daily analytics not found")
	ErrRetentionDataNotFound  = errors.New("retention data not found")
	ErrInvalidDateRange       = errors.New("invalid date range: end date must be after start date")
	ErrInvalidChannelID       = errors.New("invalid channel ID")
)

// EventType represents the type of analytics event
type EventType string

const (
	EventTypeView     EventType = "view"
	EventTypePlay     EventType = "play"
	EventTypePause    EventType = "pause"
	EventTypeSeek     EventType = "seek"
	EventTypeComplete EventType = "complete"
	EventTypeBuffer   EventType = "buffer"
	EventTypeError    EventType = "error"
)

// VideoDeviceType represents the type of device used
type VideoDeviceType string

const (
	VideoDeviceTypeDesktop VideoDeviceType = "desktop"
	VideoDeviceTypeMobile  VideoDeviceType = "mobile"
	VideoDeviceTypeTablet  VideoDeviceType = "tablet"
	VideoDeviceTypeTV      VideoDeviceType = "tv"
	VideoDeviceTypeUnknown VideoDeviceType = "unknown"
)

// AnalyticsEvent represents a raw analytics event
type AnalyticsEvent struct {
	ID                uuid.UUID       `db:"id" json:"id"`
	VideoID           uuid.UUID       `db:"video_id" json:"video_id"`
	EventType         EventType       `db:"event_type" json:"event_type"`
	UserID            *uuid.UUID      `db:"user_id" json:"user_id,omitempty"`
	SessionID         string          `db:"session_id" json:"session_id"`
	TimestampSeconds  *int            `db:"timestamp_seconds" json:"timestamp_seconds,omitempty"`
	WatchDurationSecs *int            `db:"watch_duration_seconds" json:"watch_duration_seconds,omitempty"`
	IPAddress         *string         `db:"ip_address" json:"ip_address,omitempty"`
	UserAgent         string          `db:"user_agent" json:"user_agent,omitempty"`
	CountryCode       string          `db:"country_code" json:"country_code,omitempty"`
	Region            string          `db:"region" json:"region,omitempty"`
	City              string          `db:"city" json:"city,omitempty"`
	DeviceType        VideoDeviceType `db:"device_type" json:"device_type"`
	Browser           string          `db:"browser" json:"browser,omitempty"`
	OS                string          `db:"os" json:"os,omitempty"`
	Referrer          string          `db:"referrer" json:"referrer,omitempty"`
	Quality           string          `db:"quality" json:"quality,omitempty"`
	PlayerVersion     string          `db:"player_version" json:"player_version,omitempty"`
	CreatedAt         time.Time       `db:"created_at" json:"created_at"`
}

// Validate validates an analytics event
func (e *AnalyticsEvent) Validate() error {
	if e.VideoID == uuid.Nil {
		return ErrInvalidVideoID
	}

	if !e.EventType.IsValid() {
		return ErrInvalidEventType
	}

	if e.SessionID == "" {
		return ErrInvalidSessionID
	}

	if e.TimestampSeconds != nil && *e.TimestampSeconds < 0 {
		return ErrInvalidTimestamp
	}

	if e.WatchDurationSecs != nil && *e.WatchDurationSecs < 0 {
		return ErrInvalidWatchDuration
	}

	if e.DeviceType != "" && !e.DeviceType.IsValid() {
		return ErrInvalidDeviceType
	}

	return nil
}

// IsValid checks if an event type is valid
func (et EventType) IsValid() bool {
	switch et {
	case EventTypeView, EventTypePlay, EventTypePause, EventTypeSeek,
		EventTypeComplete, EventTypeBuffer, EventTypeError:
		return true
	}
	return false
}

// IsValid checks if a device type is valid
func (dt VideoDeviceType) IsValid() bool {
	switch dt {
	case VideoDeviceTypeDesktop, VideoDeviceTypeMobile, VideoDeviceTypeTablet,
		VideoDeviceTypeTV, VideoDeviceTypeUnknown:
		return true
	}
	return false
}

// DailyAnalytics represents aggregated daily analytics for a video
type DailyAnalytics struct {
	ID                       uuid.UUID       `db:"id" json:"id"`
	VideoID                  uuid.UUID       `db:"video_id" json:"video_id"`
	Date                     time.Time       `db:"date" json:"date"`
	Views                    int             `db:"views" json:"views"`
	UniqueViewers            int             `db:"unique_viewers" json:"unique_viewers"`
	WatchTimeSeconds         int64           `db:"watch_time_seconds" json:"watch_time_seconds"`
	AvgWatchPercentage       *float64        `db:"avg_watch_percentage" json:"avg_watch_percentage,omitempty"`
	CompletionRate           *float64        `db:"completion_rate" json:"completion_rate,omitempty"`
	Likes                    int             `db:"likes" json:"likes"`
	Dislikes                 int             `db:"dislikes" json:"dislikes"`
	Comments                 int             `db:"comments" json:"comments"`
	Shares                   int             `db:"shares" json:"shares"`
	Downloads                int             `db:"downloads" json:"downloads"`
	Countries                json.RawMessage `db:"countries" json:"countries"`
	Devices                  json.RawMessage `db:"devices" json:"devices"`
	Browsers                 json.RawMessage `db:"browsers" json:"browsers"`
	TrafficSources           json.RawMessage `db:"traffic_sources" json:"traffic_sources"`
	Qualities                json.RawMessage `db:"qualities" json:"qualities"`
	PeakConcurrentViewers    int             `db:"peak_concurrent_viewers" json:"peak_concurrent_viewers"`
	Errors                   int             `db:"errors" json:"errors"`
	BufferingEvents          int             `db:"buffering_events" json:"buffering_events"`
	AvgBufferingDurationSecs *float64        `db:"avg_buffering_duration_seconds" json:"avg_buffering_duration_seconds,omitempty"`
	CreatedAt                time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt                time.Time       `db:"updated_at" json:"updated_at"`
}

// Validate validates daily analytics
func (d *DailyAnalytics) Validate() error {
	if d.VideoID == uuid.Nil {
		return ErrInvalidVideoID
	}

	if d.Date.IsZero() {
		return ErrInvalidDate
	}

	if d.Views < 0 || d.UniqueViewers < 0 || d.WatchTimeSeconds < 0 {
		return ErrInvalidViewerCount
	}

	if d.AvgWatchPercentage != nil && (*d.AvgWatchPercentage < 0 || *d.AvgWatchPercentage > 100) {
		return ErrInvalidPercentage
	}

	if d.CompletionRate != nil && (*d.CompletionRate < 0 || *d.CompletionRate > 100) {
		return ErrInvalidPercentage
	}

	return nil
}

// GetCountries returns the countries map
func (d *DailyAnalytics) GetCountries() (map[string]int, error) {
	if len(d.Countries) == 0 {
		return make(map[string]int), nil
	}
	var countries map[string]int
	if err := json.Unmarshal(d.Countries, &countries); err != nil {
		return nil, err
	}
	return countries, nil
}

// GetDevices returns the devices map
func (d *DailyAnalytics) GetDevices() (map[string]int, error) {
	if len(d.Devices) == 0 {
		return make(map[string]int), nil
	}
	var devices map[string]int
	if err := json.Unmarshal(d.Devices, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// GetBrowsers returns the browsers map
func (d *DailyAnalytics) GetBrowsers() (map[string]int, error) {
	if len(d.Browsers) == 0 {
		return make(map[string]int), nil
	}
	var browsers map[string]int
	if err := json.Unmarshal(d.Browsers, &browsers); err != nil {
		return nil, err
	}
	return browsers, nil
}

// GetTrafficSources returns the traffic sources map
func (d *DailyAnalytics) GetTrafficSources() (map[string]int, error) {
	if len(d.TrafficSources) == 0 {
		return make(map[string]int), nil
	}
	var sources map[string]int
	if err := json.Unmarshal(d.TrafficSources, &sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// GetQualities returns the qualities map
func (d *DailyAnalytics) GetQualities() (map[string]int, error) {
	if len(d.Qualities) == 0 {
		return make(map[string]int), nil
	}
	var qualities map[string]int
	if err := json.Unmarshal(d.Qualities, &qualities); err != nil {
		return nil, err
	}
	return qualities, nil
}

// RetentionData represents viewer retention at a specific timestamp
type RetentionData struct {
	ID               uuid.UUID `db:"id" json:"id"`
	VideoID          uuid.UUID `db:"video_id" json:"video_id"`
	TimestampSeconds int       `db:"timestamp_seconds" json:"timestamp_seconds"`
	ViewerCount      int       `db:"viewer_count" json:"viewer_count"`
	Date             time.Time `db:"date" json:"date"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

// Validate validates retention data
func (r *RetentionData) Validate() error {
	if r.VideoID == uuid.Nil {
		return ErrInvalidVideoID
	}

	if r.TimestampSeconds < 0 {
		return ErrInvalidTimestamp
	}

	if r.ViewerCount < 0 {
		return ErrInvalidViewerCount
	}

	if r.Date.IsZero() {
		return ErrInvalidDate
	}

	return nil
}

// ChannelDailyAnalytics represents aggregated daily analytics for a channel
type ChannelDailyAnalytics struct {
	ID                uuid.UUID `db:"id" json:"id"`
	ChannelID         uuid.UUID `db:"channel_id" json:"channel_id"`
	Date              time.Time `db:"date" json:"date"`
	Views             int       `db:"views" json:"views"`
	UniqueViewers     int       `db:"unique_viewers" json:"unique_viewers"`
	WatchTimeSeconds  int64     `db:"watch_time_seconds" json:"watch_time_seconds"`
	SubscribersGained int       `db:"subscribers_gained" json:"subscribers_gained"`
	SubscribersLost   int       `db:"subscribers_lost" json:"subscribers_lost"`
	TotalSubscribers  int       `db:"total_subscribers" json:"total_subscribers"`
	Likes             int       `db:"likes" json:"likes"`
	Comments          int       `db:"comments" json:"comments"`
	Shares            int       `db:"shares" json:"shares"`
	VideosPublished   int       `db:"videos_published" json:"videos_published"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time `db:"updated_at" json:"updated_at"`
}

// Validate validates channel daily analytics
func (c *ChannelDailyAnalytics) Validate() error {
	if c.ChannelID == uuid.Nil {
		return ErrInvalidChannelID
	}

	if c.Date.IsZero() {
		return ErrInvalidDate
	}

	if c.Views < 0 || c.UniqueViewers < 0 || c.WatchTimeSeconds < 0 {
		return ErrInvalidViewerCount
	}

	return nil
}

// ActiveViewer represents a currently active viewer
type ActiveViewer struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	VideoID       uuid.UUID  `db:"video_id" json:"video_id"`
	SessionID     string     `db:"session_id" json:"session_id"`
	UserID        *uuid.UUID `db:"user_id" json:"user_id,omitempty"`
	LastHeartbeat time.Time  `db:"last_heartbeat" json:"last_heartbeat"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
}

// Validate validates an active viewer
func (a *ActiveViewer) Validate() error {
	if a.VideoID == uuid.Nil {
		return ErrInvalidVideoID
	}

	if a.SessionID == "" {
		return ErrInvalidSessionID
	}

	return nil
}

// IsActiveViewer checks if the viewer is still active (heartbeat within 30 seconds)
func (a *ActiveViewer) IsActiveViewer() bool {
	return time.Since(a.LastHeartbeat) < 30*time.Second
}

// AnalyticsSummary represents a summary of analytics for a video
type AnalyticsSummary struct {
	VideoID               uuid.UUID        `json:"video_id"`
	TotalViews            int              `json:"total_views"`
	TotalUniqueViewers    int              `json:"total_unique_viewers"`
	TotalWatchTimeSeconds int64            `json:"total_watch_time_seconds"`
	AvgWatchPercentage    float64          `json:"avg_watch_percentage"`
	AvgCompletionRate     float64          `json:"avg_completion_rate"`
	TotalLikes            int              `json:"total_likes"`
	TotalDislikes         int              `json:"total_dislikes"`
	TotalComments         int              `json:"total_comments"`
	TotalShares           int              `json:"total_shares"`
	CurrentViewers        int              `json:"current_viewers"`
	PeakViewers           int              `json:"peak_viewers"`
	TopCountries          []CountryStat    `json:"top_countries"`
	DeviceBreakdown       []DeviceStat     `json:"device_breakdown"`
	QualityBreakdown      []QualityStat    `json:"quality_breakdown"`
	TrafficSources        []TrafficSource  `json:"traffic_sources"`
	RetentionCurve        []RetentionPoint `json:"retention_curve,omitempty"`
}

// CountryStat represents analytics for a country
type CountryStat struct {
	Country string `json:"country"`
	Views   int    `json:"views"`
}

// DeviceStat represents analytics for a device type
type DeviceStat struct {
	Device string `json:"device"`
	Views  int    `json:"views"`
}

// QualityStat represents analytics for a quality level
type QualityStat struct {
	Quality string `json:"quality"`
	Views   int    `json:"views"`
}

// TrafficSource represents a traffic source
type TrafficSource struct {
	Source string `json:"source"`
	Views  int    `json:"views"`
}

// RetentionPoint represents a point on the retention curve
type RetentionPoint struct {
	Timestamp int `json:"timestamp"`
	Viewers   int `json:"viewers"`
}

// DateRange represents a date range for analytics queries
type DateRange struct {
	StartDate time.Time
	EndDate   time.Time
}

// Validate validates a date range
func (dr *DateRange) Validate() error {
	if dr.StartDate.IsZero() || dr.EndDate.IsZero() {
		return ErrInvalidDate
	}

	if dr.EndDate.Before(dr.StartDate) {
		return ErrInvalidDateRange
	}

	return nil
}

// Days returns the number of days in the range
func (dr *DateRange) Days() int {
	return int(dr.EndDate.Sub(dr.StartDate).Hours()/24) + 1
}
