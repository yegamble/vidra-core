package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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
