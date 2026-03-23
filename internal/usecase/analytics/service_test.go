package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type MockVideoAnalyticsRepository struct {
	mock.Mock
}

func (m *MockVideoAnalyticsRepository) CreateEvent(ctx context.Context, event *domain.AnalyticsEvent) error {
	args := m.Called(ctx, event)
	return args.Error(0)
}

func (m *MockVideoAnalyticsRepository) CreateEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error {
	args := m.Called(ctx, events)
	return args.Error(0)
}

func (m *MockVideoAnalyticsRepository) GetEventsByVideoID(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time, limit, offset int) ([]*domain.AnalyticsEvent, error) {
	args := m.Called(ctx, videoID, startDate, endDate, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AnalyticsEvent), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetEventsBySessionID(ctx context.Context, sessionID string) ([]*domain.AnalyticsEvent, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AnalyticsEvent), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) DeleteOldEvents(ctx context.Context, retentionDays int) (int64, error) {
	args := m.Called(ctx, retentionDays)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) UpsertActiveViewer(ctx context.Context, viewer *domain.ActiveViewer) error {
	args := m.Called(ctx, viewer)
	return args.Error(0)
}

func (m *MockVideoAnalyticsRepository) UpsertActiveViewersBatch(ctx context.Context, viewers []*domain.ActiveViewer) error {
	args := m.Called(ctx, viewers)
	return args.Error(0)
}

func (m *MockVideoAnalyticsRepository) GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error) {
	args := m.Called(ctx, videoID)
	return args.Get(0).(int), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetActiveViewersForVideo(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ActiveViewer), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) CleanupInactiveViewers(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) AggregateDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) error {
	args := m.Called(ctx, videoID, date)
	return args.Error(0)
}

func (m *MockVideoAnalyticsRepository) GetDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) (*domain.DailyAnalytics, error) {
	args := m.Called(ctx, videoID, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.DailyAnalytics), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
	args := m.Called(ctx, videoID, startDate, endDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.DailyAnalytics), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) CalculateRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) error {
	args := m.Called(ctx, videoID, date)
	return args.Error(0)
}

func (m *MockVideoAnalyticsRepository) GetRetentionData(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
	args := m.Called(ctx, videoID, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RetentionData), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
	args := m.Called(ctx, videoID, startDate, endDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.AnalyticsSummary), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetTotalViewsForVideo(ctx context.Context, videoID uuid.UUID) (int, error) {
	args := m.Called(ctx, videoID)
	return args.Get(0).(int), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetChannelDailyAnalytics(ctx context.Context, channelID uuid.UUID, date time.Time) (*domain.ChannelDailyAnalytics, error) {
	args := m.Called(ctx, channelID, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChannelDailyAnalytics), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
	args := m.Called(ctx, channelID, startDate, endDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ChannelDailyAnalytics), args.Error(1)
}

func (m *MockVideoAnalyticsRepository) GetTotalViewsForChannel(ctx context.Context, channelID uuid.UUID) (int, error) {
	args := m.Called(ctx, channelID)
	return args.Get(0).(int), args.Error(1)
}

func newTestService() (*Service, *MockVideoAnalyticsRepository) {
	mockRepo := new(MockVideoAnalyticsRepository)
	svc := NewService(mockRepo, nil)
	return svc, mockRepo
}

func validEvent(videoID uuid.UUID, eventType domain.EventType, sessionID string) *domain.AnalyticsEvent {
	return &domain.AnalyticsEvent{
		ID:        uuid.New(),
		VideoID:   videoID,
		EventType: eventType,
		SessionID: sessionID,
	}
}

func TestTrackEvent(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	tests := []struct {
		name        string
		event       *domain.AnalyticsEvent
		setupMock   func(*MockVideoAnalyticsRepository)
		wantErr     bool
		errContains string
	}{
		{
			name:  "success with view event upserts active viewer",
			event: validEvent(videoID, domain.EventTypeView, "sess-1"),
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(nil)
				m.On("UpsertActiveViewer", mock.Anything, mock.AnythingOfType("*domain.ActiveViewer")).Return(nil)
			},
			wantErr: false,
		},
		{
			name:  "success with play event does not upsert active viewer",
			event: validEvent(videoID, domain.EventTypePlay, "sess-2"),
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "success with view event and user ID",
			event: func() *domain.AnalyticsEvent {
				e := validEvent(videoID, domain.EventTypeView, "sess-3")
				e.UserID = &userID
				return e
			}(),
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(nil)
				m.On("UpsertActiveViewer", mock.Anything, mock.MatchedBy(func(v *domain.ActiveViewer) bool {
					return v.VideoID == videoID && v.SessionID == "sess-3" && v.UserID != nil && *v.UserID == userID
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "validation error with nil video ID",
			event: &domain.AnalyticsEvent{
				VideoID:   uuid.Nil,
				EventType: domain.EventTypeView,
				SessionID: "sess-4",
			},
			setupMock:   func(m *MockVideoAnalyticsRepository) {},
			wantErr:     true,
			errContains: "invalid event",
		},
		{
			name: "validation error with empty session ID",
			event: &domain.AnalyticsEvent{
				VideoID:   videoID,
				EventType: domain.EventTypeView,
				SessionID: "",
			},
			setupMock:   func(m *MockVideoAnalyticsRepository) {},
			wantErr:     true,
			errContains: "invalid event",
		},
		{
			name: "validation error with invalid event type",
			event: &domain.AnalyticsEvent{
				VideoID:   videoID,
				EventType: "invalid_type",
				SessionID: "sess-5",
			},
			setupMock:   func(m *MockVideoAnalyticsRepository) {},
			wantErr:     true,
			errContains: "invalid event",
		},
		{
			name:  "repo CreateEvent error",
			event: validEvent(videoID, domain.EventTypePlay, "sess-6"),
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(errors.New("db error"))
			},
			wantErr:     true,
			errContains: "failed to create event",
		},
		{
			name:  "view event with UpsertActiveViewer error is silently ignored",
			event: validEvent(videoID, domain.EventTypeView, "sess-7"),
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(nil)
				m.On("UpsertActiveViewer", mock.Anything, mock.AnythingOfType("*domain.ActiveViewer")).Return(errors.New("upsert error"))
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			err := svc.TrackEvent(context.Background(), tc.event)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestTrackEvent_EnrichesUserAgent(t *testing.T) {
	svc, mockRepo := newTestService()
	videoID := uuid.New()

	event := validEvent(videoID, domain.EventTypePlay, "sess-ua")
	event.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	mockRepo.On("CreateEvent", mock.Anything, mock.MatchedBy(func(e *domain.AnalyticsEvent) bool {
		return e.Browser != "" && e.OS != "" && e.DeviceType == domain.VideoDeviceTypeDesktop
	})).Return(nil)

	err := svc.TrackEvent(context.Background(), event)
	require.NoError(t, err)

	assert.NotEmpty(t, event.Browser)
	assert.NotEmpty(t, event.OS)
	assert.Equal(t, domain.VideoDeviceTypeDesktop, event.DeviceType)
	mockRepo.AssertExpectations(t)
}

func TestTrackEvent_EnrichesMobileUserAgent(t *testing.T) {
	svc, mockRepo := newTestService()
	videoID := uuid.New()

	event := validEvent(videoID, domain.EventTypePlay, "sess-mobile")
	event.UserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1"

	mockRepo.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(nil)

	err := svc.TrackEvent(context.Background(), event)
	require.NoError(t, err)

	assert.Equal(t, domain.VideoDeviceTypeMobile, event.DeviceType)
	mockRepo.AssertExpectations(t)
}

func TestTrackEvent_EmptyUserAgentSkipsEnrichment(t *testing.T) {
	svc, mockRepo := newTestService()
	videoID := uuid.New()

	event := validEvent(videoID, domain.EventTypePlay, "sess-no-ua")
	event.UserAgent = ""

	mockRepo.On("CreateEvent", mock.Anything, mock.AnythingOfType("*domain.AnalyticsEvent")).Return(nil)

	err := svc.TrackEvent(context.Background(), event)
	require.NoError(t, err)

	assert.Empty(t, event.Browser)
	assert.Empty(t, event.OS)
	assert.Equal(t, domain.VideoDeviceType(""), event.DeviceType)
	mockRepo.AssertExpectations(t)
}

func TestTrackEventsBatch(t *testing.T) {
	videoID := uuid.New()

	tests := []struct {
		name        string
		events      []*domain.AnalyticsEvent
		setupMock   func(*MockVideoAnalyticsRepository)
		wantErr     bool
		errContains string
	}{
		{
			name:      "empty batch returns nil",
			events:    []*domain.AnalyticsEvent{},
			setupMock: func(m *MockVideoAnalyticsRepository) {},
			wantErr:   false,
		},
		{
			name: "success with mixed event types",
			events: []*domain.AnalyticsEvent{
				validEvent(videoID, domain.EventTypeView, "sess-b1"),
				validEvent(videoID, domain.EventTypePlay, "sess-b2"),
				validEvent(videoID, domain.EventTypeView, "sess-b3"),
			},
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEventsBatch", mock.Anything, mock.AnythingOfType("[]*domain.AnalyticsEvent")).Return(nil)
				m.On("UpsertActiveViewersBatch", mock.Anything, mock.MatchedBy(func(viewers []*domain.ActiveViewer) bool {
					return len(viewers) == 2
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "success with no view events skips active viewer upsert",
			events: []*domain.AnalyticsEvent{
				validEvent(videoID, domain.EventTypePlay, "sess-b4"),
				validEvent(videoID, domain.EventTypePause, "sess-b5"),
			},
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEventsBatch", mock.Anything, mock.AnythingOfType("[]*domain.AnalyticsEvent")).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "validation error in batch",
			events: []*domain.AnalyticsEvent{
				validEvent(videoID, domain.EventTypeView, "sess-b6"),
				{
					VideoID:   uuid.Nil,
					EventType: domain.EventTypeView,
					SessionID: "sess-b7",
				},
			},
			setupMock:   func(m *MockVideoAnalyticsRepository) {},
			wantErr:     true,
			errContains: "invalid event in batch",
		},
		{
			name: "repo CreateEventsBatch error",
			events: []*domain.AnalyticsEvent{
				validEvent(videoID, domain.EventTypeView, "sess-b8"),
			},
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEventsBatch", mock.Anything, mock.AnythingOfType("[]*domain.AnalyticsEvent")).Return(errors.New("batch error"))
			},
			wantErr:     true,
			errContains: "failed to create events batch",
		},
		{
			name: "UpsertActiveViewersBatch error is silently ignored",
			events: []*domain.AnalyticsEvent{
				validEvent(videoID, domain.EventTypeView, "sess-b9"),
			},
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CreateEventsBatch", mock.Anything, mock.AnythingOfType("[]*domain.AnalyticsEvent")).Return(nil)
				m.On("UpsertActiveViewersBatch", mock.Anything, mock.AnythingOfType("[]*domain.ActiveViewer")).Return(errors.New("upsert error"))
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			err := svc.TrackEventsBatch(context.Background(), tc.events)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestTrackViewerHeartbeat(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	tests := []struct {
		name        string
		videoID     uuid.UUID
		sessionID   string
		userID      *uuid.UUID
		setupMock   func(*MockVideoAnalyticsRepository)
		wantErr     bool
		errContains string
	}{
		{
			name:      "success without user ID",
			videoID:   videoID,
			sessionID: "sess-hb1",
			userID:    nil,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("UpsertActiveViewer", mock.Anything, mock.MatchedBy(func(v *domain.ActiveViewer) bool {
					return v.VideoID == videoID && v.SessionID == "sess-hb1" && v.UserID == nil
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name:      "success with user ID",
			videoID:   videoID,
			sessionID: "sess-hb2",
			userID:    &userID,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("UpsertActiveViewer", mock.Anything, mock.MatchedBy(func(v *domain.ActiveViewer) bool {
					return v.VideoID == videoID && v.SessionID == "sess-hb2" && v.UserID != nil && *v.UserID == userID
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name:      "repo error",
			videoID:   videoID,
			sessionID: "sess-hb3",
			userID:    nil,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("UpsertActiveViewer", mock.Anything, mock.AnythingOfType("*domain.ActiveViewer")).Return(errors.New("upsert error"))
			},
			wantErr:     true,
			errContains: "upsert error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			err := svc.TrackViewerHeartbeat(context.Background(), tc.videoID, tc.sessionID, tc.userID)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetActiveViewerCount(t *testing.T) {
	videoID := uuid.New()

	tests := []struct {
		name      string
		setupMock func(*MockVideoAnalyticsRepository)
		wantCount int
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetActiveViewerCount", mock.Anything, videoID).Return(42, nil)
			},
			wantCount: 42,
			wantErr:   false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetActiveViewerCount", mock.Anything, videoID).Return(0, errors.New("db error"))
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			count, err := svc.GetActiveViewerCount(context.Background(), videoID)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantCount, count)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetActiveViewers(t *testing.T) {
	videoID := uuid.New()

	viewers := []*domain.ActiveViewer{
		{ID: uuid.New(), VideoID: videoID, SessionID: "sess-av1"},
		{ID: uuid.New(), VideoID: videoID, SessionID: "sess-av2"},
	}

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		wantViewers []*domain.ActiveViewer
		wantErr     bool
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetActiveViewersForVideo", mock.Anything, videoID).Return(viewers, nil)
			},
			wantViewers: viewers,
			wantErr:     false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetActiveViewersForVideo", mock.Anything, videoID).Return(nil, errors.New("db error"))
			},
			wantViewers: nil,
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetActiveViewers(context.Background(), videoID)

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantViewers, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestAggregateDailyAnalytics(t *testing.T) {
	videoID := uuid.New()
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		wantErr     bool
		errContains string
	}{
		{
			name: "success aggregates daily and calculates retention",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("AggregateDailyAnalytics", mock.Anything, videoID, date).Return(nil)
				m.On("CalculateRetentionCurve", mock.Anything, videoID, date).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "aggregate daily analytics error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("AggregateDailyAnalytics", mock.Anything, videoID, date).Return(errors.New("aggregate error"))
			},
			wantErr:     true,
			errContains: "failed to aggregate daily analytics",
		},
		{
			name: "calculate retention curve error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("AggregateDailyAnalytics", mock.Anything, videoID, date).Return(nil)
				m.On("CalculateRetentionCurve", mock.Anything, videoID, date).Return(errors.New("retention error"))
			},
			wantErr:     true,
			errContains: "failed to calculate retention curve",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			err := svc.AggregateDailyAnalytics(context.Background(), videoID, date)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetDailyAnalytics(t *testing.T) {
	videoID := uuid.New()
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	daily := &domain.DailyAnalytics{
		ID:      uuid.New(),
		VideoID: videoID,
		Date:    date,
		Views:   100,
	}

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		want        *domain.DailyAnalytics
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetDailyAnalytics", mock.Anything, videoID, date).Return(daily, nil)
			},
			want:    daily,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetDailyAnalytics", mock.Anything, videoID, date).Return(nil, errors.New("not found"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get daily analytics",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetDailyAnalytics(context.Background(), videoID, date)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetDailyAnalyticsRange(t *testing.T) {
	videoID := uuid.New()
	startDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	dailyList := []*domain.DailyAnalytics{
		{ID: uuid.New(), VideoID: videoID, Date: startDate, Views: 50},
		{ID: uuid.New(), VideoID: videoID, Date: endDate, Views: 75},
	}

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		want        []*domain.DailyAnalytics
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetDailyAnalyticsRange", mock.Anything, videoID, startDate, endDate).Return(dailyList, nil)
			},
			want:    dailyList,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetDailyAnalyticsRange", mock.Anything, videoID, startDate, endDate).Return(nil, errors.New("range error"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get daily analytics range",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetDailyAnalyticsRange(context.Background(), videoID, startDate, endDate)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetRetentionCurve(t *testing.T) {
	videoID := uuid.New()
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	retentionData := []*domain.RetentionData{
		{ID: uuid.New(), VideoID: videoID, TimestampSeconds: 0, ViewerCount: 100, Date: date},
		{ID: uuid.New(), VideoID: videoID, TimestampSeconds: 30, ViewerCount: 80, Date: date},
		{ID: uuid.New(), VideoID: videoID, TimestampSeconds: 60, ViewerCount: 50, Date: date},
	}

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		want        []*domain.RetentionData
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetRetentionData", mock.Anything, videoID, date).Return(retentionData, nil)
			},
			want:    retentionData,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetRetentionData", mock.Anything, videoID, date).Return(nil, errors.New("retention error"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get retention data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetRetentionCurve(context.Background(), videoID, date)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetVideoAnalyticsSummary(t *testing.T) {
	videoID := uuid.New()
	startDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		wantErr     bool
		errContains string
		validate    func(*testing.T, *domain.AnalyticsSummary)
	}{
		{
			name: "success with retention data",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				summary := &domain.AnalyticsSummary{
					VideoID:    videoID,
					TotalViews: 500,
				}
				retention := []*domain.RetentionData{
					{TimestampSeconds: 0, ViewerCount: 100},
					{TimestampSeconds: 30, ViewerCount: 80},
				}
				m.On("GetVideoAnalyticsSummary", mock.Anything, videoID, startDate, endDate).Return(summary, nil)
				m.On("GetRetentionData", mock.Anything, videoID, endDate).Return(retention, nil)
			},
			wantErr: false,
			validate: func(t *testing.T, s *domain.AnalyticsSummary) {
				assert.Equal(t, 500, s.TotalViews)
				require.Len(t, s.RetentionCurve, 2)
				assert.Equal(t, 0, s.RetentionCurve[0].Timestamp)
				assert.Equal(t, 100, s.RetentionCurve[0].Viewers)
				assert.Equal(t, 30, s.RetentionCurve[1].Timestamp)
				assert.Equal(t, 80, s.RetentionCurve[1].Viewers)
			},
		},
		{
			name: "success without retention data (error silently ignored)",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				summary := &domain.AnalyticsSummary{
					VideoID:    videoID,
					TotalViews: 200,
				}
				m.On("GetVideoAnalyticsSummary", mock.Anything, videoID, startDate, endDate).Return(summary, nil)
				m.On("GetRetentionData", mock.Anything, videoID, endDate).Return(nil, errors.New("no retention"))
			},
			wantErr: false,
			validate: func(t *testing.T, s *domain.AnalyticsSummary) {
				assert.Equal(t, 200, s.TotalViews)
				assert.Empty(t, s.RetentionCurve)
			},
		},
		{
			name: "success with empty retention data",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				summary := &domain.AnalyticsSummary{
					VideoID:    videoID,
					TotalViews: 300,
				}
				m.On("GetVideoAnalyticsSummary", mock.Anything, videoID, startDate, endDate).Return(summary, nil)
				m.On("GetRetentionData", mock.Anything, videoID, endDate).Return([]*domain.RetentionData{}, nil)
			},
			wantErr: false,
			validate: func(t *testing.T, s *domain.AnalyticsSummary) {
				assert.Equal(t, 300, s.TotalViews)
				assert.Empty(t, s.RetentionCurve)
			},
		},
		{
			name: "repo GetVideoAnalyticsSummary error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetVideoAnalyticsSummary", mock.Anything, videoID, startDate, endDate).Return(nil, errors.New("summary error"))
			},
			wantErr:     true,
			errContains: "failed to get video analytics summary",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetVideoAnalyticsSummary(context.Background(), videoID, startDate, endDate)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if tc.validate != nil {
					tc.validate(t, result)
				}
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetTotalViews(t *testing.T) {
	videoID := uuid.New()

	tests := []struct {
		name      string
		setupMock func(*MockVideoAnalyticsRepository)
		want      int
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetTotalViewsForVideo", mock.Anything, videoID).Return(1234, nil)
			},
			want:    1234,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetTotalViewsForVideo", mock.Anything, videoID).Return(0, errors.New("db error"))
			},
			want:    0,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			count, err := svc.GetTotalViews(context.Background(), videoID)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, count)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetChannelDailyAnalytics(t *testing.T) {
	channelID := uuid.New()
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	channelDaily := &domain.ChannelDailyAnalytics{
		ID:        uuid.New(),
		ChannelID: channelID,
		Date:      date,
		Views:     250,
	}

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		want        *domain.ChannelDailyAnalytics
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetChannelDailyAnalytics", mock.Anything, channelID, date).Return(channelDaily, nil)
			},
			want:    channelDaily,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetChannelDailyAnalytics", mock.Anything, channelID, date).Return(nil, errors.New("channel error"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get channel daily analytics",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetChannelDailyAnalytics(context.Background(), channelID, date)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetChannelDailyAnalyticsRange(t *testing.T) {
	channelID := uuid.New()
	startDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	channelList := []*domain.ChannelDailyAnalytics{
		{ID: uuid.New(), ChannelID: channelID, Date: startDate, Views: 100},
		{ID: uuid.New(), ChannelID: channelID, Date: endDate, Views: 150},
	}

	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		want        []*domain.ChannelDailyAnalytics
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetChannelDailyAnalyticsRange", mock.Anything, channelID, startDate, endDate).Return(channelList, nil)
			},
			want:    channelList,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetChannelDailyAnalyticsRange", mock.Anything, channelID, startDate, endDate).Return(nil, errors.New("range error"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get channel daily analytics range",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetChannelDailyAnalyticsRange(context.Background(), channelID, startDate, endDate)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetChannelTotalViews(t *testing.T) {
	channelID := uuid.New()

	tests := []struct {
		name      string
		setupMock func(*MockVideoAnalyticsRepository)
		want      int
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetTotalViewsForChannel", mock.Anything, channelID).Return(5678, nil)
			},
			want:    5678,
			wantErr: false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetTotalViewsForChannel", mock.Anything, channelID).Return(0, errors.New("db error"))
			},
			want:    0,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			count, err := svc.GetChannelTotalViews(context.Background(), channelID)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, count)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestCleanupOldEvents(t *testing.T) {
	tests := []struct {
		name          string
		retentionDays int
		setupMock     func(*MockVideoAnalyticsRepository)
		wantCount     int64
		wantErr       bool
		errContains   string
	}{
		{
			name:          "success",
			retentionDays: 90,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("DeleteOldEvents", mock.Anything, 90).Return(int64(150), nil)
			},
			wantCount: 150,
			wantErr:   false,
		},
		{
			name:          "repo error",
			retentionDays: 30,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("DeleteOldEvents", mock.Anything, 30).Return(int64(0), errors.New("cleanup error"))
			},
			wantCount:   0,
			wantErr:     true,
			errContains: "failed to cleanup old events",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			count, err := svc.CleanupOldEvents(context.Background(), tc.retentionDays)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Equal(t, int64(0), count)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantCount, count)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestCleanupInactiveViewers(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*MockVideoAnalyticsRepository)
		wantCount   int64
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CleanupInactiveViewers", mock.Anything).Return(int64(25), nil)
			},
			wantCount: 25,
			wantErr:   false,
		},
		{
			name: "repo error",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("CleanupInactiveViewers", mock.Anything).Return(int64(0), errors.New("cleanup error"))
			},
			wantCount:   0,
			wantErr:     true,
			errContains: "failed to cleanup inactive viewers",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			count, err := svc.CleanupInactiveViewers(context.Background())

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Equal(t, int64(0), count)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantCount, count)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetEventsBySession(t *testing.T) {
	videoID := uuid.New()

	events := []*domain.AnalyticsEvent{
		{ID: uuid.New(), VideoID: videoID, EventType: domain.EventTypeView, SessionID: "sess-q1"},
		{ID: uuid.New(), VideoID: videoID, EventType: domain.EventTypePlay, SessionID: "sess-q1"},
	}

	tests := []struct {
		name        string
		sessionID   string
		setupMock   func(*MockVideoAnalyticsRepository)
		want        []*domain.AnalyticsEvent
		wantErr     bool
		errContains string
	}{
		{
			name:      "success",
			sessionID: "sess-q1",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetEventsBySessionID", mock.Anything, "sess-q1").Return(events, nil)
			},
			want:    events,
			wantErr: false,
		},
		{
			name:      "repo error",
			sessionID: "sess-q2",
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetEventsBySessionID", mock.Anything, "sess-q2").Return(nil, errors.New("session error"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get events by session",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetEventsBySession(context.Background(), tc.sessionID)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

func TestGetEventsByVideo(t *testing.T) {
	videoID := uuid.New()
	startDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	events := []*domain.AnalyticsEvent{
		{ID: uuid.New(), VideoID: videoID, EventType: domain.EventTypeView, SessionID: "sess-v1"},
	}

	tests := []struct {
		name        string
		limit       int
		offset      int
		setupMock   func(*MockVideoAnalyticsRepository)
		want        []*domain.AnalyticsEvent
		wantErr     bool
		errContains string
	}{
		{
			name:   "success",
			limit:  10,
			offset: 0,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetEventsByVideoID", mock.Anything, videoID, startDate, endDate, 10, 0).Return(events, nil)
			},
			want:    events,
			wantErr: false,
		},
		{
			name:   "success with pagination",
			limit:  5,
			offset: 10,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetEventsByVideoID", mock.Anything, videoID, startDate, endDate, 5, 10).Return(events, nil)
			},
			want:    events,
			wantErr: false,
		},
		{
			name:   "repo error",
			limit:  10,
			offset: 0,
			setupMock: func(m *MockVideoAnalyticsRepository) {
				m.On("GetEventsByVideoID", mock.Anything, videoID, startDate, endDate, 10, 0).Return(nil, errors.New("video error"))
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get events by video",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mockRepo := newTestService()
			tc.setupMock(mockRepo)

			result, err := svc.GetEventsByVideo(context.Background(), videoID, startDate, endDate, tc.limit, tc.offset)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, result)
			}
			mockRepo.AssertExpectations(t)
		})
	}
}

type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *MockVideoRepository) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath).Error(0)
}
func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath, processedCIDs, thumbnailCID, previewCID).Error(0)
}
func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	args := m.Called(ctx, remoteURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}

func (m *MockVideoRepository) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func TestAggregateAllVideosForDate(t *testing.T) {
	svc, _ := newTestService()
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	err := svc.AggregateAllVideosForDate(context.Background(), date)
	require.NoError(t, err)
}

func TestAggregateAllVideosForDate_WithVideos(t *testing.T) {
	mockAnalytics := new(MockVideoAnalyticsRepository)
	mockVideos := new(MockVideoRepository)
	svc := NewService(mockAnalytics, mockVideos)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	vid1 := &domain.Video{ID: uuid.New().String()}
	vid2 := &domain.Video{ID: uuid.New().String()}

	mockVideos.On("List", ctx, &domain.VideoSearchRequest{Limit: aggregateBatchSize, Offset: 0}).
		Return([]*domain.Video{vid1, vid2}, int64(2), nil)
	mockVideos.On("List", ctx, &domain.VideoSearchRequest{Limit: aggregateBatchSize, Offset: aggregateBatchSize}).
		Return([]*domain.Video{}, int64(2), nil)

	vid1UUID, _ := uuid.Parse(vid1.ID)
	vid2UUID, _ := uuid.Parse(vid2.ID)
	mockAnalytics.On("AggregateDailyAnalytics", ctx, vid1UUID, date).Return(nil)
	mockAnalytics.On("CalculateRetentionCurve", ctx, vid1UUID, date).Return(nil)
	mockAnalytics.On("AggregateDailyAnalytics", ctx, vid2UUID, date).Return(nil)
	mockAnalytics.On("CalculateRetentionCurve", ctx, vid2UUID, date).Return(nil)

	err := svc.AggregateAllVideosForDate(ctx, date)
	require.NoError(t, err)
	mockVideos.AssertExpectations(t)
	mockAnalytics.AssertExpectations(t)
}

func TestAggregateAllVideosForDate_EmptyLibrary(t *testing.T) {
	mockAnalytics := new(MockVideoAnalyticsRepository)
	mockVideos := new(MockVideoRepository)
	svc := NewService(mockAnalytics, mockVideos)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	mockVideos.On("List", ctx, &domain.VideoSearchRequest{Limit: aggregateBatchSize, Offset: 0}).
		Return([]*domain.Video{}, int64(0), nil)

	err := svc.AggregateAllVideosForDate(ctx, date)
	require.NoError(t, err)
	mockVideos.AssertExpectations(t)
	mockAnalytics.AssertNotCalled(t, "AggregateDailyAnalytics")
}

func TestNewService(t *testing.T) {
	mockRepo := new(MockVideoAnalyticsRepository)
	svc := NewService(mockRepo, nil)

	require.NotNil(t, svc)
	assert.Equal(t, mockRepo, svc.repo)
}
