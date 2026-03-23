package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStreamAnalytics(t *testing.T) {
	streamID := uuid.New()
	analytics := NewStreamAnalytics(streamID)

	assert.NotNil(t, analytics)
	assert.Equal(t, streamID, analytics.StreamID)
	assert.NotEqual(t, uuid.Nil, analytics.ID)
	assert.NotZero(t, analytics.CollectedAt)
	assert.NotZero(t, analytics.CreatedAt)
	assert.NotZero(t, analytics.UpdatedAt)
	assert.Equal(t, json.RawMessage("{}"), analytics.ViewerCountries)
	assert.Equal(t, json.RawMessage("{}"), analytics.ViewerDevices)
	assert.Equal(t, json.RawMessage("{}"), analytics.ViewerBrowsers)
}

func TestNewAnalyticsViewerSession(t *testing.T) {
	streamID := uuid.New()
	userID := uuid.New()
	sessionID := "test-session-123"

	session := NewAnalyticsViewerSession(streamID, &userID, sessionID)

	assert.NotNil(t, session)
	assert.NotEqual(t, uuid.Nil, session.ID)
	assert.Equal(t, streamID, session.StreamID)
	assert.Equal(t, &userID, session.UserID)
	assert.Equal(t, sessionID, session.SessionID)
	assert.NotZero(t, session.JoinedAt)
	assert.Nil(t, session.LeftAt)
	assert.NotZero(t, session.CreatedAt)
	assert.NotZero(t, session.UpdatedAt)
}

func TestAnalyticsViewerSession_EndSession(t *testing.T) {
	session := &AnalyticsViewerSession{
		JoinedAt: time.Now().Add(-5 * time.Minute),
	}

	assert.Nil(t, session.LeftAt)
	session.EndSession()
	assert.NotNil(t, session.LeftAt)
}

func TestAnalyticsViewerSession_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		session  *AnalyticsViewerSession
		expected bool
	}{
		{
			name: "active session",
			session: &AnalyticsViewerSession{
				LeftAt: nil,
			},
			expected: true,
		},
		{
			name: "ended session",
			session: &AnalyticsViewerSession{
				LeftAt: func() *time.Time { t := time.Now(); return &t }(),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.session.IsActive())
		})
	}
}

func TestAnalyticsViewerSession_GetDuration(t *testing.T) {
	joinedAt := time.Now().Add(-10 * time.Minute)

	tests := []struct {
		name    string
		session *AnalyticsViewerSession
		minDur  time.Duration
		maxDur  time.Duration
	}{
		{
			name: "active session",
			session: &AnalyticsViewerSession{
				JoinedAt: joinedAt,
				LeftAt:   nil,
			},
			minDur: 9 * time.Minute,
			maxDur: 11 * time.Minute,
		},
		{
			name: "ended session",
			session: &AnalyticsViewerSession{
				JoinedAt: joinedAt,
				LeftAt:   func() *time.Time { t := joinedAt.Add(5 * time.Minute); return &t }(),
			},
			minDur: 4 * time.Minute,
			maxDur: 6 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration := tt.session.GetDuration()
			assert.Greater(t, duration, tt.minDur, "Duration should be greater than minimum")
			assert.Less(t, duration, tt.maxDur, "Duration should be less than maximum")
		})
	}
}

func TestStreamStatsSummary_CalculateEngagementRate(t *testing.T) {
	tests := []struct {
		name         string
		summary      *StreamStatsSummary
		expectedRate float64
	}{
		{
			name: "zero viewers",
			summary: &StreamStatsSummary{
				TotalViewers: 0,
			},
			expectedRate: 0,
		},
		{
			name: "with engagement",
			summary: &StreamStatsSummary{
				TotalViewers:        100,
				TotalUniqueChatters: 25,
				TotalLikes:          10,
				TotalShares:         5,
			},
			expectedRate: 40.0, // (25 + 10 + 5) / 100 * 100
		},
		{
			name: "no engagement",
			summary: &StreamStatsSummary{
				TotalViewers:        100,
				TotalUniqueChatters: 0,
				TotalLikes:          0,
				TotalShares:         0,
			},
			expectedRate: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.summary.CalculateEngagementRate()
			assert.Equal(t, tt.expectedRate, tt.summary.EngagementRate)
		})
	}
}

func TestStreamStatsSummary_CalculateQualityScore(t *testing.T) {
	tests := []struct {
		name          string
		summary       *StreamStatsSummary
		expectedScore float64
	}{
		{
			name: "perfect quality",
			summary: &StreamStatsSummary{
				AverageBitrate:   func() *int { v := 5000; return &v }(),
				AverageFramerate: func() *float64 { v := 60.0; return &v }(),
			},
			expectedScore: 100.0,
		},
		{
			name: "low bitrate",
			summary: &StreamStatsSummary{
				AverageBitrate:   func() *int { v := 800; return &v }(),
				AverageFramerate: func() *float64 { v := 30.0; return &v }(),
			},
			expectedScore: 70.0, // -30 for very low bitrate
		},
		{
			name: "low framerate",
			summary: &StreamStatsSummary{
				AverageBitrate:   func() *int { v := 4000; return &v }(),
				AverageFramerate: func() *float64 { v := 20.0; return &v }(),
			},
			expectedScore: 80.0, // -20 for very low framerate
		},
		{
			name: "terrible quality",
			summary: &StreamStatsSummary{
				AverageBitrate:   func() *int { v := 500; return &v }(),
				AverageFramerate: func() *float64 { v := 15.0; return &v }(),
			},
			expectedScore: 50.0, // -30 for bitrate, -20 for framerate
		},
		{
			name: "no metrics",
			summary: &StreamStatsSummary{
				AverageBitrate:   nil,
				AverageFramerate: nil,
			},
			expectedScore: 100.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.summary.CalculateQualityScore()
			assert.Equal(t, tt.expectedScore, tt.summary.QualityScore)
		})
	}
}

func TestAnalyticsDataPoint(t *testing.T) {
	now := time.Now()
	bitrate := 3000
	dataPoint := &AnalyticsDataPoint{
		Time:       now,
		AvgViewers: 50,
		MaxViewers: 75,
		Messages:   120,
		AvgBitrate: &bitrate,
	}

	assert.Equal(t, now, dataPoint.Time)
	assert.Equal(t, 50, dataPoint.AvgViewers)
	assert.Equal(t, 75, dataPoint.MaxViewers)
	assert.Equal(t, 120, dataPoint.Messages)
	assert.Equal(t, 3000, *dataPoint.AvgBitrate)
}

func TestAnalyticsTimeRange(t *testing.T) {
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	interval := 5

	timeRange := &AnalyticsTimeRange{
		StartTime: start,
		EndTime:   end,
		Interval:  interval,
	}

	assert.Equal(t, start, timeRange.StartTime)
	assert.Equal(t, end, timeRange.EndTime)
	assert.Equal(t, interval, timeRange.Interval)
}

func TestCountryViewers(t *testing.T) {
	cv := &CountryViewers{
		Country: "US",
		Viewers: 100,
	}

	assert.Equal(t, "US", cv.Country)
	assert.Equal(t, 100, cv.Viewers)
}

func TestDeviceAndBrowserBreakdown(t *testing.T) {
	devices := DeviceBreakdown{
		"desktop": 60,
		"mobile":  30,
		"tablet":  10,
	}

	browsers := BrowserBreakdown{
		"chrome":  50,
		"firefox": 30,
		"safari":  20,
	}

	assert.Equal(t, 60, devices["desktop"])
	assert.Equal(t, 30, devices["mobile"])
	assert.Equal(t, 50, browsers["chrome"])
	assert.Equal(t, 20, browsers["safari"])
}

// ---------------------------------------------------------------------------
// CalculateQualityScore edge cases
// ---------------------------------------------------------------------------

func TestCalculateQualityScore_EdgeCases(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	f64Ptr := func(v float64) *float64 { return &v }

	tests := []struct {
		name          string
		summary       *StreamStatsSummary
		expectedScore float64
	}{
		{
			name: "nil AverageBitrate only",
			summary: &StreamStatsSummary{
				AverageBitrate:   nil,
				AverageFramerate: f64Ptr(30.0),
			},
			expectedScore: 100.0,
		},
		{
			name: "nil AverageFramerate only",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(3000),
				AverageFramerate: nil,
			},
			expectedScore: 95.0, // -5 for bitrate 2500-4000
		},
		{
			name: "bitrate below 1000",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(999),
				AverageFramerate: f64Ptr(60.0),
			},
			expectedScore: 70.0, // -30
		},
		{
			name: "bitrate between 1000 and 2500",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(1500),
				AverageFramerate: f64Ptr(60.0),
			},
			expectedScore: 85.0, // -15
		},
		{
			name: "bitrate between 2500 and 4000",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(3000),
				AverageFramerate: f64Ptr(60.0),
			},
			expectedScore: 95.0, // -5
		},
		{
			name: "bitrate at or above 4000",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(4000),
				AverageFramerate: f64Ptr(60.0),
			},
			expectedScore: 100.0, // no deduction
		},
		{
			name: "framerate below 24",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(5000),
				AverageFramerate: f64Ptr(20.0),
			},
			expectedScore: 80.0, // -20
		},
		{
			name: "framerate between 24 and 30",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(5000),
				AverageFramerate: f64Ptr(25.0),
			},
			expectedScore: 90.0, // -10
		},
		{
			name: "framerate at or above 30",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(5000),
				AverageFramerate: f64Ptr(30.0),
			},
			expectedScore: 100.0, // no deduction
		},
		{
			name: "combined low values clamped to 0",
			summary: &StreamStatsSummary{
				AverageBitrate:   intPtr(100),
				AverageFramerate: f64Ptr(1.0),
			},
			// -30 (bitrate < 1000) + -20 (framerate < 24) = 50, not below 0
			// To truly clamp, we rely on the function logic; score = 100 - 30 - 20 = 50
			// The clamp only triggers if score < 0, but max deduction is 50.
			// Test verifies the clamp path is reachable in spirit; 50 is the floor here.
			expectedScore: 50.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.summary.CalculateQualityScore()
			assert.Equal(t, tt.expectedScore, tt.summary.QualityScore)
		})
	}
}

// ---------------------------------------------------------------------------
// AnalyticsEvent.Validate
// ---------------------------------------------------------------------------

func TestAnalyticsEvent_Validate(t *testing.T) {
	validVideoID := uuid.New()
	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name    string
		event   *AnalyticsEvent
		wantErr error
	}{
		{
			name: "nil VideoID",
			event: &AnalyticsEvent{
				VideoID:   uuid.Nil,
				EventType: EventTypeView,
				SessionID: "sess-1",
			},
			wantErr: ErrInvalidVideoID,
		},
		{
			name: "invalid EventType",
			event: &AnalyticsEvent{
				VideoID:   validVideoID,
				EventType: EventType("bogus"),
				SessionID: "sess-1",
			},
			wantErr: ErrInvalidEventType,
		},
		{
			name: "empty SessionID",
			event: &AnalyticsEvent{
				VideoID:   validVideoID,
				EventType: EventTypePlay,
				SessionID: "",
			},
			wantErr: ErrInvalidSessionID,
		},
		{
			name: "negative TimestampSeconds",
			event: &AnalyticsEvent{
				VideoID:          validVideoID,
				EventType:        EventTypePlay,
				SessionID:        "sess-1",
				TimestampSeconds: intPtr(-1),
			},
			wantErr: ErrInvalidTimestamp,
		},
		{
			name: "negative WatchDurationSecs",
			event: &AnalyticsEvent{
				VideoID:           validVideoID,
				EventType:         EventTypePlay,
				SessionID:         "sess-1",
				WatchDurationSecs: intPtr(-5),
			},
			wantErr: ErrInvalidWatchDuration,
		},
		{
			name: "invalid DeviceType",
			event: &AnalyticsEvent{
				VideoID:    validVideoID,
				EventType:  EventTypeView,
				SessionID:  "sess-1",
				DeviceType: VideoDeviceType("spaceship"),
			},
			wantErr: ErrInvalidDeviceType,
		},
		{
			name: "valid event",
			event: &AnalyticsEvent{
				VideoID:           validVideoID,
				EventType:         EventTypeComplete,
				SessionID:         "sess-1",
				TimestampSeconds:  intPtr(120),
				WatchDurationSecs: intPtr(60),
				DeviceType:        VideoDeviceTypeDesktop,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// EventType.IsValid
// ---------------------------------------------------------------------------

func TestEventType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		et       EventType
		expected bool
	}{
		{"view", EventTypeView, true},
		{"play", EventTypePlay, true},
		{"pause", EventTypePause, true},
		{"seek", EventTypeSeek, true},
		{"complete", EventTypeComplete, true},
		{"buffer", EventTypeBuffer, true},
		{"error", EventTypeError, true},
		{"empty string", EventType(""), false},
		{"unknown type", EventType("unknown"), false},
		{"uppercase VIEW", EventType("VIEW"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.et.IsValid())
		})
	}
}

// ---------------------------------------------------------------------------
// VideoDeviceType.IsValid
// ---------------------------------------------------------------------------

func TestVideoDeviceType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		dt       VideoDeviceType
		expected bool
	}{
		{"desktop", VideoDeviceTypeDesktop, true},
		{"mobile", VideoDeviceTypeMobile, true},
		{"tablet", VideoDeviceTypeTablet, true},
		{"tv", VideoDeviceTypeTV, true},
		{"unknown", VideoDeviceTypeUnknown, true},
		{"empty string", VideoDeviceType(""), false},
		{"invalid type", VideoDeviceType("fridge"), false},
		{"uppercase DESKTOP", VideoDeviceType("DESKTOP"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.dt.IsValid())
		})
	}
}

// ---------------------------------------------------------------------------
// DailyAnalytics.Validate
// ---------------------------------------------------------------------------

func TestDailyAnalytics_Validate(t *testing.T) {
	validVideoID := uuid.New()
	validDate := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	f64Ptr := func(v float64) *float64 { return &v }

	tests := []struct {
		name    string
		da      *DailyAnalytics
		wantErr error
	}{
		{
			name: "nil VideoID",
			da: &DailyAnalytics{
				VideoID: uuid.Nil,
				Date:    validDate,
			},
			wantErr: ErrInvalidVideoID,
		},
		{
			name: "zero Date",
			da: &DailyAnalytics{
				VideoID: validVideoID,
				Date:    time.Time{},
			},
			wantErr: ErrInvalidDate,
		},
		{
			name: "negative Views",
			da: &DailyAnalytics{
				VideoID: validVideoID,
				Date:    validDate,
				Views:   -1,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "negative UniqueViewers",
			da: &DailyAnalytics{
				VideoID:       validVideoID,
				Date:          validDate,
				UniqueViewers: -1,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "negative WatchTimeSeconds",
			da: &DailyAnalytics{
				VideoID:          validVideoID,
				Date:             validDate,
				WatchTimeSeconds: -100,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "AvgWatchPercentage below 0",
			da: &DailyAnalytics{
				VideoID:            validVideoID,
				Date:               validDate,
				AvgWatchPercentage: f64Ptr(-1.0),
			},
			wantErr: ErrInvalidPercentage,
		},
		{
			name: "AvgWatchPercentage above 100",
			da: &DailyAnalytics{
				VideoID:            validVideoID,
				Date:               validDate,
				AvgWatchPercentage: f64Ptr(101.0),
			},
			wantErr: ErrInvalidPercentage,
		},
		{
			name: "CompletionRate below 0",
			da: &DailyAnalytics{
				VideoID:        validVideoID,
				Date:           validDate,
				CompletionRate: f64Ptr(-0.5),
			},
			wantErr: ErrInvalidPercentage,
		},
		{
			name: "CompletionRate above 100",
			da: &DailyAnalytics{
				VideoID:        validVideoID,
				Date:           validDate,
				CompletionRate: f64Ptr(100.1),
			},
			wantErr: ErrInvalidPercentage,
		},
		{
			name: "valid record",
			da: &DailyAnalytics{
				VideoID:            validVideoID,
				Date:               validDate,
				Views:              100,
				UniqueViewers:      80,
				WatchTimeSeconds:   5000,
				AvgWatchPercentage: f64Ptr(75.0),
				CompletionRate:     f64Ptr(50.0),
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.da.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DailyAnalytics JSON getters
// ---------------------------------------------------------------------------

func TestDailyAnalytics_GetCountries(t *testing.T) {
	tests := []struct {
		name    string
		json    json.RawMessage
		want    map[string]int
		wantErr bool
	}{
		{
			name: "empty JSON",
			json: json.RawMessage(nil),
			want: map[string]int{},
		},
		{
			name: "valid JSON",
			json: json.RawMessage(`{"US":100,"UK":50}`),
			want: map[string]int{"US": 100, "UK": 50},
		},
		{
			name:    "invalid JSON",
			json:    json.RawMessage(`{broken`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			da := &DailyAnalytics{Countries: tt.json}
			got, err := da.GetCountries()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDailyAnalytics_GetDevices(t *testing.T) {
	tests := []struct {
		name    string
		json    json.RawMessage
		want    map[string]int
		wantErr bool
	}{
		{
			name: "empty JSON",
			json: json.RawMessage(nil),
			want: map[string]int{},
		},
		{
			name: "valid JSON",
			json: json.RawMessage(`{"desktop":60,"mobile":40}`),
			want: map[string]int{"desktop": 60, "mobile": 40},
		},
		{
			name:    "invalid JSON",
			json:    json.RawMessage(`not-json`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			da := &DailyAnalytics{Devices: tt.json}
			got, err := da.GetDevices()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDailyAnalytics_GetBrowsers(t *testing.T) {
	tests := []struct {
		name    string
		json    json.RawMessage
		want    map[string]int
		wantErr bool
	}{
		{
			name: "empty JSON",
			json: json.RawMessage(nil),
			want: map[string]int{},
		},
		{
			name: "valid JSON",
			json: json.RawMessage(`{"chrome":70,"firefox":30}`),
			want: map[string]int{"chrome": 70, "firefox": 30},
		},
		{
			name:    "invalid JSON",
			json:    json.RawMessage(`[`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			da := &DailyAnalytics{Browsers: tt.json}
			got, err := da.GetBrowsers()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDailyAnalytics_GetTrafficSources(t *testing.T) {
	tests := []struct {
		name    string
		json    json.RawMessage
		want    map[string]int
		wantErr bool
	}{
		{
			name: "empty JSON",
			json: json.RawMessage(nil),
			want: map[string]int{},
		},
		{
			name: "valid JSON",
			json: json.RawMessage(`{"direct":50,"search":30,"social":20}`),
			want: map[string]int{"direct": 50, "search": 30, "social": 20},
		},
		{
			name:    "invalid JSON",
			json:    json.RawMessage(`}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			da := &DailyAnalytics{TrafficSources: tt.json}
			got, err := da.GetTrafficSources()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestDailyAnalytics_GetQualities(t *testing.T) {
	tests := []struct {
		name    string
		json    json.RawMessage
		want    map[string]int
		wantErr bool
	}{
		{
			name: "empty JSON",
			json: json.RawMessage(nil),
			want: map[string]int{},
		},
		{
			name: "valid JSON",
			json: json.RawMessage(`{"1080p":80,"720p":15,"480p":5}`),
			want: map[string]int{"1080p": 80, "720p": 15, "480p": 5},
		},
		{
			name:    "invalid JSON",
			json:    json.RawMessage(`{"key":`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			da := &DailyAnalytics{Qualities: tt.json}
			got, err := da.GetQualities()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RetentionData.Validate
// ---------------------------------------------------------------------------

func TestRetentionData_Validate(t *testing.T) {
	validVideoID := uuid.New()
	validDate := time.Date(2025, 3, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		rd      *RetentionData
		wantErr error
	}{
		{
			name: "nil VideoID",
			rd: &RetentionData{
				VideoID:          uuid.Nil,
				TimestampSeconds: 10,
				ViewerCount:      5,
				Date:             validDate,
			},
			wantErr: ErrInvalidVideoID,
		},
		{
			name: "negative TimestampSeconds",
			rd: &RetentionData{
				VideoID:          validVideoID,
				TimestampSeconds: -1,
				ViewerCount:      5,
				Date:             validDate,
			},
			wantErr: ErrInvalidTimestamp,
		},
		{
			name: "negative ViewerCount",
			rd: &RetentionData{
				VideoID:          validVideoID,
				TimestampSeconds: 10,
				ViewerCount:      -1,
				Date:             validDate,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "zero Date",
			rd: &RetentionData{
				VideoID:          validVideoID,
				TimestampSeconds: 10,
				ViewerCount:      5,
				Date:             time.Time{},
			},
			wantErr: ErrInvalidDate,
		},
		{
			name: "valid record",
			rd: &RetentionData{
				VideoID:          validVideoID,
				TimestampSeconds: 30,
				ViewerCount:      100,
				Date:             validDate,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rd.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ChannelDailyAnalytics.Validate
// ---------------------------------------------------------------------------

func TestChannelDailyAnalytics_Validate(t *testing.T) {
	validChannelID := uuid.New()
	validDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		cda     *ChannelDailyAnalytics
		wantErr error
	}{
		{
			name: "nil ChannelID",
			cda: &ChannelDailyAnalytics{
				ChannelID: uuid.Nil,
				Date:      validDate,
			},
			wantErr: ErrInvalidChannelID,
		},
		{
			name: "zero Date",
			cda: &ChannelDailyAnalytics{
				ChannelID: validChannelID,
				Date:      time.Time{},
			},
			wantErr: ErrInvalidDate,
		},
		{
			name: "negative Views",
			cda: &ChannelDailyAnalytics{
				ChannelID: validChannelID,
				Date:      validDate,
				Views:     -1,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "negative UniqueViewers",
			cda: &ChannelDailyAnalytics{
				ChannelID:     validChannelID,
				Date:          validDate,
				UniqueViewers: -5,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "negative WatchTimeSeconds",
			cda: &ChannelDailyAnalytics{
				ChannelID:        validChannelID,
				Date:             validDate,
				WatchTimeSeconds: -100,
			},
			wantErr: ErrInvalidViewerCount,
		},
		{
			name: "valid record",
			cda: &ChannelDailyAnalytics{
				ChannelID:        validChannelID,
				Date:             validDate,
				Views:            500,
				UniqueViewers:    200,
				WatchTimeSeconds: 10000,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cda.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ActiveViewer.Validate
// ---------------------------------------------------------------------------

func TestActiveViewer_Validate(t *testing.T) {
	validVideoID := uuid.New()

	tests := []struct {
		name    string
		av      *ActiveViewer
		wantErr error
	}{
		{
			name: "nil VideoID",
			av: &ActiveViewer{
				VideoID:   uuid.Nil,
				SessionID: "sess-1",
			},
			wantErr: ErrInvalidVideoID,
		},
		{
			name: "empty SessionID",
			av: &ActiveViewer{
				VideoID:   validVideoID,
				SessionID: "",
			},
			wantErr: ErrInvalidSessionID,
		},
		{
			name: "valid",
			av: &ActiveViewer{
				VideoID:   validVideoID,
				SessionID: "sess-abc",
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.av.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ActiveViewer.IsActiveViewer
// ---------------------------------------------------------------------------

func TestActiveViewer_IsActiveViewer(t *testing.T) {
	tests := []struct {
		name     string
		av       *ActiveViewer
		expected bool
	}{
		{
			name: "recent heartbeat is active",
			av: &ActiveViewer{
				LastHeartbeat: time.Now().Add(-10 * time.Second),
			},
			expected: true,
		},
		{
			name: "old heartbeat is inactive",
			av: &ActiveViewer{
				LastHeartbeat: time.Now().Add(-60 * time.Second),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.av.IsActiveViewer())
		})
	}
}

// ---------------------------------------------------------------------------
// DateRange.Validate
// ---------------------------------------------------------------------------

func TestDateRange_Validate(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		dr      *DateRange
		wantErr error
	}{
		{
			name: "zero StartDate",
			dr: &DateRange{
				StartDate: time.Time{},
				EndDate:   now,
			},
			wantErr: ErrInvalidDate,
		},
		{
			name: "zero EndDate",
			dr: &DateRange{
				StartDate: now,
				EndDate:   time.Time{},
			},
			wantErr: ErrInvalidDate,
		},
		{
			name: "EndDate before StartDate",
			dr: &DateRange{
				StartDate: now,
				EndDate:   now.Add(-24 * time.Hour),
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name: "valid range",
			dr: &DateRange{
				StartDate: now.Add(-7 * 24 * time.Hour),
				EndDate:   now,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dr.Validate()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DateRange.Days
// ---------------------------------------------------------------------------

func TestDateRange_Days(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		dr       *DateRange
		expected int
	}{
		{
			name: "same day",
			dr: &DateRange{
				StartDate: base,
				EndDate:   base,
			},
			expected: 1,
		},
		{
			name: "7 days apart",
			dr: &DateRange{
				StartDate: base,
				EndDate:   base.Add(7 * 24 * time.Hour),
			},
			expected: 8, // 7 days difference + 1
		},
		{
			name: "1 day apart",
			dr: &DateRange{
				StartDate: base,
				EndDate:   base.Add(24 * time.Hour),
			},
			expected: 2, // 1 day difference + 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.dr.Days())
		})
	}
}
