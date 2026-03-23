package domain

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type StreamAnalytics struct {
	ID          uuid.UUID `json:"id" db:"id"`
	StreamID    uuid.UUID `json:"stream_id" db:"stream_id"`
	CollectedAt time.Time `json:"collected_at" db:"collected_at"`

	ViewerCount      int `json:"viewer_count" db:"viewer_count"`
	PeakViewerCount  int `json:"peak_viewer_count" db:"peak_viewer_count"`
	UniqueViewers    int `json:"unique_viewers" db:"unique_viewers"`
	AverageWatchTime int `json:"average_watch_time" db:"average_watch_time"`

	ChatMessagesCount int `json:"chat_messages_count" db:"chat_messages_count"`
	ChatParticipants  int `json:"chat_participants" db:"chat_participants"`
	LikesCount        int `json:"likes_count" db:"likes_count"`
	SharesCount       int `json:"shares_count" db:"shares_count"`

	Bitrate        *int     `json:"bitrate,omitempty" db:"bitrate"`
	Framerate      *float64 `json:"framerate,omitempty" db:"framerate"`
	Resolution     string   `json:"resolution,omitempty" db:"resolution"`
	BufferingRatio *float64 `json:"buffering_ratio,omitempty" db:"buffering_ratio"`
	AvgLatency     *int     `json:"avg_latency,omitempty" db:"avg_latency"`

	ViewerCountries json.RawMessage `json:"viewer_countries" db:"viewer_countries"`

	ViewerDevices  json.RawMessage `json:"viewer_devices" db:"viewer_devices"`
	ViewerBrowsers json.RawMessage `json:"viewer_browsers" db:"viewer_browsers"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type StreamStatsSummary struct {
	ID       uuid.UUID `json:"id" db:"id"`
	StreamID uuid.UUID `json:"stream_id" db:"stream_id"`

	TotalViewers          int   `json:"total_viewers" db:"total_viewers"`
	PeakConcurrentViewers int   `json:"peak_concurrent_viewers" db:"peak_concurrent_viewers"`
	AverageViewers        int   `json:"average_viewers" db:"average_viewers"`
	TotalWatchTime        int64 `json:"total_watch_time" db:"total_watch_time"`
	AverageWatchDuration  int   `json:"average_watch_duration" db:"average_watch_duration"`

	TotalChatMessages   int     `json:"total_chat_messages" db:"total_chat_messages"`
	TotalUniqueChatters int     `json:"total_unique_chatters" db:"total_unique_chatters"`
	TotalLikes          int     `json:"total_likes" db:"total_likes"`
	TotalShares         int     `json:"total_shares" db:"total_shares"`
	EngagementRate      float64 `json:"engagement_rate" db:"engagement_rate"`

	AverageBitrate   *int     `json:"average_bitrate,omitempty" db:"average_bitrate"`
	AverageFramerate *float64 `json:"average_framerate,omitempty" db:"average_framerate"`
	QualityScore     float64  `json:"quality_score" db:"quality_score"`

	StreamDuration      int        `json:"stream_duration" db:"stream_duration"`
	FirstViewerJoinedAt *time.Time `json:"first_viewer_joined_at,omitempty" db:"first_viewer_joined_at"`
	PeakTime            *time.Time `json:"peak_time,omitempty" db:"peak_time"`

	TopCountries   json.RawMessage `json:"top_countries" db:"top_countries"`
	CountriesCount int             `json:"countries_count" db:"countries_count"`

	TopDevices  json.RawMessage `json:"top_devices" db:"top_devices"`
	TopBrowsers json.RawMessage `json:"top_browsers" db:"top_browsers"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type AnalyticsViewerSession struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	StreamID  uuid.UUID  `json:"stream_id" db:"stream_id"`
	UserID    *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	SessionID string     `json:"session_id" db:"session_id"`

	JoinedAt      time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt        *time.Time `json:"left_at,omitempty" db:"left_at"`
	WatchDuration *int       `json:"watch_duration,omitempty" db:"watch_duration"`

	IPAddress       string `json:"ip_address,omitempty" db:"ip_address"`
	CountryCode     string `json:"country_code,omitempty" db:"country_code"`
	City            string `json:"city,omitempty" db:"city"`
	DeviceType      string `json:"device_type,omitempty" db:"device_type"`
	Browser         string `json:"browser,omitempty" db:"browser"`
	OperatingSystem string `json:"operating_system,omitempty" db:"operating_system"`

	MessagesSent int  `json:"messages_sent" db:"messages_sent"`
	Liked        bool `json:"liked" db:"liked"`
	Shared       bool `json:"shared" db:"shared"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type AnalyticsTimeRange struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Interval  int       `json:"interval_minutes"`
}

type AnalyticsDataPoint struct {
	Time       time.Time `json:"time" db:"time_bucket"`
	AvgViewers int       `json:"avg_viewers" db:"avg_viewers"`
	MaxViewers int       `json:"max_viewers" db:"max_viewers"`
	Messages   int       `json:"messages" db:"messages"`
	AvgBitrate *int      `json:"avg_bitrate,omitempty" db:"avg_bitrate"`
}

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

func (vs *AnalyticsViewerSession) EndSession() {
	now := time.Now()
	vs.LeftAt = &now
}

func (vs *AnalyticsViewerSession) IsActive() bool {
	return vs.LeftAt == nil
}

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

func (s *StreamStatsSummary) CalculateQualityScore() {
	score := 100.0

	if s.AverageBitrate != nil {
		if *s.AverageBitrate < 1000 {
			score -= 30
		} else if *s.AverageBitrate < 2500 {
			score -= 15
		} else if *s.AverageBitrate < 4000 {
			score -= 5
		}
	}

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

func (vs *AnalyticsViewerSession) GetDuration() time.Duration {
	if vs.LeftAt == nil {
		return time.Since(vs.JoinedAt)
	}
	return vs.LeftAt.Sub(vs.JoinedAt)
}

type CountryViewers struct {
	Country string `json:"country"`
	Viewers int    `json:"viewers"`
}

type DeviceBreakdown map[string]int

type BrowserBreakdown map[string]int

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

type VideoDeviceType string

const (
	VideoDeviceTypeDesktop VideoDeviceType = "desktop"
	VideoDeviceTypeMobile  VideoDeviceType = "mobile"
	VideoDeviceTypeTablet  VideoDeviceType = "tablet"
	VideoDeviceTypeTV      VideoDeviceType = "tv"
	VideoDeviceTypeUnknown VideoDeviceType = "unknown"
)

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

func (et EventType) IsValid() bool {
	switch et {
	case EventTypeView, EventTypePlay, EventTypePause, EventTypeSeek,
		EventTypeComplete, EventTypeBuffer, EventTypeError:
		return true
	}
	return false
}

func (dt VideoDeviceType) IsValid() bool {
	switch dt {
	case VideoDeviceTypeDesktop, VideoDeviceTypeMobile, VideoDeviceTypeTablet,
		VideoDeviceTypeTV, VideoDeviceTypeUnknown:
		return true
	}
	return false
}

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

type RetentionData struct {
	ID               uuid.UUID `db:"id" json:"id"`
	VideoID          uuid.UUID `db:"video_id" json:"video_id"`
	TimestampSeconds int       `db:"timestamp_seconds" json:"timestamp_seconds"`
	ViewerCount      int       `db:"viewer_count" json:"viewer_count"`
	Date             time.Time `db:"date" json:"date"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

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

type ActiveViewer struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	VideoID       uuid.UUID  `db:"video_id" json:"video_id"`
	SessionID     string     `db:"session_id" json:"session_id"`
	UserID        *uuid.UUID `db:"user_id" json:"user_id,omitempty"`
	LastHeartbeat time.Time  `db:"last_heartbeat" json:"last_heartbeat"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
}

func (a *ActiveViewer) Validate() error {
	if a.VideoID == uuid.Nil {
		return ErrInvalidVideoID
	}

	if a.SessionID == "" {
		return ErrInvalidSessionID
	}

	return nil
}

func (a *ActiveViewer) IsActiveViewer() bool {
	return time.Since(a.LastHeartbeat) < 30*time.Second
}

type AnalyticsSummary struct {
	VideoID               uuid.UUID        `db:"video_id" json:"video_id"`
	TotalViews            int              `db:"total_views" json:"total_views"`
	TotalUniqueViewers    int              `db:"total_unique_viewers" json:"total_unique_viewers"`
	TotalWatchTimeSeconds int64            `db:"total_watch_time_seconds" json:"total_watch_time_seconds"`
	AvgWatchPercentage    float64          `db:"avg_watch_percentage" json:"avg_watch_percentage"`
	AvgCompletionRate     float64          `db:"avg_completion_rate" json:"avg_completion_rate"`
	TotalLikes            int              `db:"total_likes" json:"total_likes"`
	TotalDislikes         int              `db:"total_dislikes" json:"total_dislikes"`
	TotalComments         int              `db:"total_comments" json:"total_comments"`
	TotalShares           int              `db:"total_shares" json:"total_shares"`
	CurrentViewers        int              `db:"current_viewers" json:"current_viewers"`
	PeakViewers           int              `db:"peak_viewers" json:"peak_viewers"`
	TopCountries          []CountryStat    `json:"top_countries"`
	DeviceBreakdown       []DeviceStat     `json:"device_breakdown"`
	QualityBreakdown      []QualityStat    `json:"quality_breakdown"`
	TrafficSources        []TrafficSource  `json:"traffic_sources"`
	RetentionCurve        []RetentionPoint `json:"retention_curve,omitempty"`
}

type CountryStat struct {
	Country string `json:"country"`
	Views   int    `json:"views"`
}

type DeviceStat struct {
	Device string `json:"device"`
	Views  int    `json:"views"`
}

type QualityStat struct {
	Quality string `json:"quality"`
	Views   int    `json:"views"`
}

type TrafficSource struct {
	Source string `json:"source"`
	Views  int    `json:"views"`
}

type RetentionPoint struct {
	Timestamp int `json:"timestamp"`
	Viewers   int `json:"viewers"`
}

type DateRange struct {
	StartDate time.Time
	EndDate   time.Time
}

func (dr *DateRange) Validate() error {
	if dr.StartDate.IsZero() || dr.EndDate.IsZero() {
		return ErrInvalidDate
	}

	if dr.EndDate.Before(dr.StartDate) {
		return ErrInvalidDateRange
	}

	return nil
}

func (dr *DateRange) Days() int {
	return int(dr.EndDate.Sub(dr.StartDate).Hours()/24) + 1
}
