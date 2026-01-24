package livestream

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockNotificationSender is a mock implementation of NotificationSender
type MockNotificationSender struct {
	mock.Mock
}

func (m *MockNotificationSender) SendStreamStartingNotification(ctx context.Context, streamID uuid.UUID, subscribers []uuid.UUID) error {
	args := m.Called(ctx, streamID, subscribers)
	return args.Error(0)
}

func (m *MockNotificationSender) SendStreamLiveNotification(ctx context.Context, streamID uuid.UUID, subscribers []uuid.UUID) error {
	args := m.Called(ctx, streamID, subscribers)
	return args.Error(0)
}

func TestNewStreamScheduler(t *testing.T) {
	db := &sqlx.DB{}
	sender := &MockNotificationSender{}
	config := DefaultSchedulerConfig()

	scheduler := NewStreamScheduler(db, sender, config)

	assert.NotNil(t, scheduler)
	assert.Equal(t, db, scheduler.db)
	assert.Equal(t, sender, scheduler.notificationSender)
	assert.Equal(t, config, scheduler.config)
	assert.False(t, scheduler.running)
}

func TestStreamScheduler_StartStop(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer mockDB.Close()

	db := sqlx.NewDb(mockDB, "sqlmock")
	sender := &MockNotificationSender{}
	config := SchedulerConfig{
		CheckInterval:       100 * time.Millisecond,
		NotificationAdvance: 15 * time.Minute,
	}

	scheduler := NewStreamScheduler(db, sender, config)

	// Mock the queries that will be executed during the check - expect multiple runs
	// The scheduler checks immediately and then every 100ms
	for i := 0; i < 3; i++ {
		// First check: sendReminders
		mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
			WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"}))

		// Second check: transitionToWaitingRoom
		mock.ExpectQuery("UPDATE live_streams").
			WillReturnRows(sqlmock.NewRows([]string{"id", "title"}))

		// Third check: transitionToLive
		mock.ExpectQuery("UPDATE live_streams").
			WillReturnRows(sqlmock.NewRows([]string{"id", "title"}))
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start scheduler
	err = scheduler.Start(ctx)
	assert.NoError(t, err)
	assert.True(t, scheduler.IsRunning())

	// Try to start again - should fail
	err = scheduler.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")

	// Let it run for a short time
	time.Sleep(150 * time.Millisecond)

	// Stop scheduler explicitly
	scheduler.Stop()

	// Cancel context
	cancel()

	// Verify stopped
	assert.False(t, scheduler.IsRunning())
}

func TestStreamScheduler_Stop(t *testing.T) {
	mockDB, _, err := sqlmock.New()
	assert.NoError(t, err)
	defer mockDB.Close()

	db := sqlx.NewDb(mockDB, "sqlmock")
	sender := &MockNotificationSender{}
	config := DefaultSchedulerConfig()

	scheduler := NewStreamScheduler(db, sender, config)

	// Stop when not running - should not panic
	scheduler.Stop()
	assert.False(t, scheduler.IsRunning())
}

func TestStreamScheduler_sendReminders(t *testing.T) {
	tests := []struct {
		name             string
		setupMock        func(sqlmock.Sqlmock, *MockNotificationSender)
		expectedError    bool
		expectedErrorMsg string
	}{
		{
			name: "successful reminder send",
			setupMock: func(mock sqlmock.Sqlmock, sender *MockNotificationSender) {
				streamID := uuid.New()
				channelID := uuid.New()
				subscriberID1 := uuid.New()
				subscriberID2 := uuid.New()
				scheduledStart := time.Now().Add(10 * time.Minute)

				// Get streams needing reminders
				mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
					WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"}).
						AddRow(streamID, channelID, "Test Stream", scheduledStart, false, "scheduled"))

				// Get subscribers for channel
				mock.ExpectQuery("SELECT channel_id, subscriber_id FROM channel_subscriptions").
					WithArgs(channelID).
					WillReturnRows(sqlmock.NewRows([]string{"channel_id", "subscriber_id"}).
						AddRow(channelID, subscriberID1).
						AddRow(channelID, subscriberID2))

				// Send notification
				ctx := context.Background()
				sender.On("SendStreamStartingNotification", ctx,
					streamID, []uuid.UUID{subscriberID1, subscriberID2}).
					Return(nil)

				// Mark reminder as sent
				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(streamID).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedError: false,
		},
		{
			name: "no streams needing reminders",
			setupMock: func(mock sqlmock.Sqlmock, sender *MockNotificationSender) {
				mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
					WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"}))
			},
			expectedError: false,
		},
		{
			name: "error getting streams",
			setupMock: func(mock sqlmock.Sqlmock, sender *MockNotificationSender) {
				mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
					WillReturnError(errors.New("database error"))
			},
			expectedError:    true,
			expectedErrorMsg: "failed to get streams needing reminders",
		},
		{
			name: "no subscribers for channel",
			setupMock: func(mock sqlmock.Sqlmock, sender *MockNotificationSender) {
				streamID := uuid.New()
				channelID := uuid.New()
				scheduledStart := time.Now().Add(10 * time.Minute)

				mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
					WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"}).
						AddRow(streamID, channelID, "Test Stream", scheduledStart, false, "scheduled"))

				mock.ExpectQuery("SELECT channel_id, subscriber_id FROM channel_subscriptions").
					WithArgs(channelID).
					WillReturnRows(sqlmock.NewRows([]string{"channel_id", "subscriber_id"}))

				// Mark reminder as sent even with no subscribers
				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(streamID).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedError: false,
		},
		{
			name: "multiple streams needing reminders (batch optimization)",
			setupMock: func(mock sqlmock.Sqlmock, sender *MockNotificationSender) {
				stream1ID := uuid.New()
				channel1ID := uuid.New()
				sub1ID := uuid.New()

				stream2ID := uuid.New()
				channel2ID := uuid.New()
				sub2ID := uuid.New()
				sub3ID := uuid.New()

				scheduledStart := time.Now().Add(10 * time.Minute)

				// 1. Get streams query (returns 2 streams)
				mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
					WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"}).
						AddRow(stream1ID, channel1ID, "Stream 1", scheduledStart, false, "scheduled").
						AddRow(stream2ID, channel2ID, "Stream 2", scheduledStart, false, "scheduled"))

				// 2. Batch get subscribers query (Expect 1 query, not 2)
				// The query uses IN (?) which expands to multiple params.
				mock.ExpectQuery("SELECT channel_id, subscriber_id FROM channel_subscriptions").
					WithArgs(channel1ID, channel2ID).
					WillReturnRows(sqlmock.NewRows([]string{"channel_id", "subscriber_id"}).
						AddRow(channel1ID, sub1ID).
						AddRow(channel2ID, sub2ID).
						AddRow(channel2ID, sub3ID))

				// 3. Send notifications (loop)
				ctx := context.Background()
				sender.On("SendStreamStartingNotification", ctx, stream1ID, []uuid.UUID{sub1ID}).Return(nil)
				sender.On("SendStreamStartingNotification", ctx, stream2ID, []uuid.UUID{sub2ID, sub3ID}).Return(nil)

				// 4. Mark reminders sent (loop)
				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(stream1ID).
					WillReturnResult(sqlmock.NewResult(1, 1))

				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(stream2ID).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer mockDB.Close()

			db := sqlx.NewDb(mockDB, "sqlmock")
			sender := &MockNotificationSender{}
			config := DefaultSchedulerConfig()

			scheduler := NewStreamScheduler(db, sender, config)

			tt.setupMock(mock, sender)

			err = scheduler.sendReminders(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
				if tt.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tt.expectedErrorMsg)
				}
			} else {
				assert.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
			sender.AssertExpectations(t)
		})
	}
}

func TestStreamScheduler_transitionToWaitingRoom(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(sqlmock.Sqlmock)
		expectedError bool
	}{
		{
			name: "successful transition",
			setupMock: func(mock sqlmock.Sqlmock) {
				streamID := uuid.New()
				mock.ExpectQuery("UPDATE live_streams SET status = 'waiting_room'").
					WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).
						AddRow(streamID, "Test Stream"))
			},
			expectedError: false,
		},
		{
			name: "no streams to transition",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("UPDATE live_streams SET status = 'waiting_room'").
					WillReturnRows(sqlmock.NewRows([]string{"id", "title"}))
			},
			expectedError: false,
		},
		{
			name: "database error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("UPDATE live_streams SET status = 'waiting_room'").
					WillReturnError(errors.New("database error"))
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer mockDB.Close()

			db := sqlx.NewDb(mockDB, "sqlmock")
			scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

			tt.setupMock(mock)

			err = scheduler.transitionToWaitingRoom(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStreamScheduler_transitionToLive(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(sqlmock.Sqlmock)
		expectedError bool
	}{
		{
			name: "successful transition",
			setupMock: func(mock sqlmock.Sqlmock) {
				streamID := uuid.New()
				mock.ExpectQuery("UPDATE live_streams SET status = 'live'").
					WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).
						AddRow(streamID, "Test Stream"))
			},
			expectedError: false,
		},
		{
			name: "no streams to transition",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("UPDATE live_streams SET status = 'live'").
					WillReturnRows(sqlmock.NewRows([]string{"id", "title"}))
			},
			expectedError: false,
		},
		{
			name: "database error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("UPDATE live_streams SET status = 'live'").
					WillReturnError(errors.New("database error"))
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer mockDB.Close()

			db := sqlx.NewDb(mockDB, "sqlmock")
			scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

			tt.setupMock(mock)

			err = scheduler.transitionToLive(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStreamScheduler_markReminderSent(t *testing.T) {
	tests := []struct {
		name          string
		streamID      uuid.UUID
		setupMock     func(sqlmock.Sqlmock, uuid.UUID)
		expectedError bool
	}{
		{
			name:     "successful mark",
			streamID: uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock, streamID uuid.UUID) {
				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(streamID).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedError: false,
		},
		{
			name:     "stream not found",
			streamID: uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock, streamID uuid.UUID) {
				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(streamID).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			expectedError: true,
		},
		{
			name:     "database error",
			streamID: uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock, streamID uuid.UUID) {
				mock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
					WithArgs(streamID).
					WillReturnError(errors.New("database error"))
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer mockDB.Close()

			db := sqlx.NewDb(mockDB, "sqlmock")
			scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

			tt.setupMock(mock, tt.streamID)

			err = scheduler.markReminderSent(context.Background(), tt.streamID)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStreamScheduler_getChannelSubscribers(t *testing.T) {
	tests := []struct {
		name          string
		channelID     uuid.UUID
		setupMock     func(sqlmock.Sqlmock, uuid.UUID)
		expected      []uuid.UUID
		expectedError bool
	}{
		{
			name:      "successful get with subscribers",
			channelID: uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock, channelID uuid.UUID) {
				sub1 := uuid.New()
				sub2 := uuid.New()
				mock.ExpectQuery("SELECT subscriber_id FROM channel_subscriptions").
					WithArgs(channelID).
					WillReturnRows(sqlmock.NewRows([]string{"subscriber_id"}).
						AddRow(sub1).
						AddRow(sub2))
			},
			expected:      []uuid.UUID{},
			expectedError: false,
		},
		{
			name:      "no subscribers",
			channelID: uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock, channelID uuid.UUID) {
				mock.ExpectQuery("SELECT subscriber_id FROM channel_subscriptions").
					WithArgs(channelID).
					WillReturnError(sql.ErrNoRows)
			},
			expected:      []uuid.UUID{},
			expectedError: false,
		},
		{
			name:      "database error",
			channelID: uuid.New(),
			setupMock: func(mock sqlmock.Sqlmock, channelID uuid.UUID) {
				mock.ExpectQuery("SELECT subscriber_id FROM channel_subscriptions").
					WithArgs(channelID).
					WillReturnError(errors.New("database error"))
			},
			expected:      nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer mockDB.Close()

			db := sqlx.NewDb(mockDB, "sqlmock")
			scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

			tt.setupMock(mock, tt.channelID)

			result, err := scheduler.getChannelSubscribers(context.Background(), tt.channelID)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestStreamScheduler_GetUpcomingStreams(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer mockDB.Close()

	db := sqlx.NewDb(mockDB, "sqlmock")
	scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

	streamID := uuid.New()
	channelID := uuid.New()
	scheduledStart := time.Now().Add(1 * time.Hour)

	mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
		WithArgs(10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "scheduled_end", "reminder_sent", "status", "waiting_room_enabled"}).
			AddRow(streamID, channelID, "Test Stream", scheduledStart, nil, false, "scheduled", true))

	result, err := scheduler.GetUpcomingStreams(context.Background(), 10)

	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, streamID, result[0].ID)
	assert.Equal(t, "Test Stream", result[0].Title)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamScheduler_GetStreamsByTimeRange(t *testing.T) {
	mockDB, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer mockDB.Close()

	db := sqlx.NewDb(mockDB, "sqlmock")
	scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

	start := time.Now()
	end := start.Add(24 * time.Hour)
	streamID := uuid.New()
	channelID := uuid.New()
	scheduledStart := start.Add(2 * time.Hour)

	mock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "scheduled_end", "reminder_sent", "status", "waiting_room_enabled"}).
			AddRow(streamID, channelID, "Test Stream", scheduledStart, nil, false, "scheduled", false))

	result, err := scheduler.GetStreamsByTimeRange(context.Background(), start, end)

	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, streamID, result[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamScheduler_ScheduleStream(t *testing.T) {
	tests := []struct {
		name               string
		streamID           uuid.UUID
		scheduledStart     time.Time
		waitingRoomEnabled bool
		waitingRoomMessage string
		setupMock          func(sqlmock.Sqlmock, uuid.UUID, time.Time, bool, string)
		expectedError      bool
	}{
		{
			name:               "successful schedule",
			streamID:           uuid.New(),
			scheduledStart:     time.Now().Add(2 * time.Hour),
			waitingRoomEnabled: true,
			waitingRoomMessage: "Stream starting soon!",
			setupMock: func(mock sqlmock.Sqlmock, streamID uuid.UUID, start time.Time, enabled bool, message string) {
				mock.ExpectExec("UPDATE live_streams SET scheduled_start").
					WithArgs(streamID, start, enabled, message).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			expectedError: false,
		},
		{
			name:               "stream not found",
			streamID:           uuid.New(),
			scheduledStart:     time.Now().Add(2 * time.Hour),
			waitingRoomEnabled: false,
			waitingRoomMessage: "",
			setupMock: func(mock sqlmock.Sqlmock, streamID uuid.UUID, start time.Time, enabled bool, message string) {
				mock.ExpectExec("UPDATE live_streams SET scheduled_start").
					WithArgs(streamID, start, enabled, message).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			expectedError: true,
		},
		{
			name:               "database error",
			streamID:           uuid.New(),
			scheduledStart:     time.Now().Add(2 * time.Hour),
			waitingRoomEnabled: true,
			waitingRoomMessage: "Starting soon",
			setupMock: func(mock sqlmock.Sqlmock, streamID uuid.UUID, start time.Time, enabled bool, message string) {
				mock.ExpectExec("UPDATE live_streams SET scheduled_start").
					WithArgs(streamID, start, enabled, message).
					WillReturnError(errors.New("database error"))
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB, mock, err := sqlmock.New()
			assert.NoError(t, err)
			defer mockDB.Close()

			db := sqlx.NewDb(mockDB, "sqlmock")
			scheduler := NewStreamScheduler(db, nil, DefaultSchedulerConfig())

			tt.setupMock(mock, tt.streamID, tt.scheduledStart, tt.waitingRoomEnabled, tt.waitingRoomMessage)

			err = scheduler.ScheduleStream(context.Background(), tt.streamID, tt.scheduledStart,
				tt.waitingRoomEnabled, tt.waitingRoomMessage)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDefaultSchedulerConfig(t *testing.T) {
	config := DefaultSchedulerConfig()

	assert.Equal(t, 1*time.Minute, config.CheckInterval)
	assert.Equal(t, 15*time.Minute, config.NotificationAdvance)
}
