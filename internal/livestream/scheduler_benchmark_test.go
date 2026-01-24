package livestream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/mock"
)

// sendRemindersLegacy simulates the N+1 query behavior
func (s *StreamScheduler) sendRemindersLegacy(ctx context.Context) error {
	query := `
		SELECT id, channel_id, title, scheduled_start, reminder_sent, status
		FROM live_streams
		WHERE status = 'scheduled'
		AND reminder_sent = false
		AND scheduled_start IS NOT NULL
		AND scheduled_start BETWEEN NOW() AND NOW() + INTERVAL '1 minute' * $1
	`

	var streams []ScheduledStream
	if err := s.db.SelectContext(ctx, &streams, query, int(s.config.NotificationAdvance.Minutes())); err != nil {
		return fmt.Errorf("failed to get streams needing reminders: %w", err)
	}

	if len(streams) == 0 {
		return nil
	}

	for _, stream := range streams {
		// Get subscribers for the channel - N+1 Query
		subscribers, err := s.getChannelSubscribers(ctx, stream.ChannelID)
		if err != nil {
			// In real code we'd log or return, here just continue for bench
			continue
		}

		// Send notifications
		if s.notificationSender != nil && len(subscribers) > 0 {
			if err := s.notificationSender.SendStreamStartingNotification(ctx, stream.ID, subscribers); err != nil {
				continue
			}
		}

		// Mark reminder as sent
		if err := s.markReminderSent(ctx, stream.ID); err != nil {
			continue
		}
	}

	return nil
}

func setupBenchmarkData(numStreams int) ([]ScheduledStream, map[uuid.UUID][]uuid.UUID) {
	streams := make([]ScheduledStream, numStreams)
	subscribers := make(map[uuid.UUID][]uuid.UUID)

	for i := 0; i < numStreams; i++ {
		streamID := uuid.New()
		channelID := uuid.New()
		streams[i] = ScheduledStream{
			ID:             streamID,
			ChannelID:      channelID,
			Title:          fmt.Sprintf("Stream %d", i),
			ScheduledStart: nil, // irrelevant for scan unless we scan it
			Status:         "scheduled",
		}

		// 5 subscribers per channel
		subs := make([]uuid.UUID, 5)
		for j := 0; j < 5; j++ {
			subs[j] = uuid.New()
		}
		subscribers[channelID] = subs
	}
	return streams, subscribers
}

func BenchmarkSendReminders_Current(b *testing.B) {
	mockDB, dbMock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	sender := &MockNotificationSender{}
	config := DefaultSchedulerConfig()
	scheduler := NewStreamScheduler(db, sender, config)

	numStreams := 50
	streams, subscribers := setupBenchmarkData(numStreams)

	// Pre-configure mock to allow infinite calls (not possible with strict matching easily)
	// So we will rebuild expectations each time.

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Reset sender expectations
		sender = &MockNotificationSender{}
		scheduler.notificationSender = sender

		// Setup Expectations for ONE run

		// 1. Select streams
		rows := sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"})
		for _, s := range streams {
			rows.AddRow(s.ID, s.ChannelID, s.Title, time.Now().Add(1*time.Hour), false, "scheduled")
		}
		dbMock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
			WillReturnRows(rows)

		// 2. Batch select subscribers
		// Logic in scheduler: SELECT ... WHERE channel_id IN (?)
		// We need to match the arguments.
		// Since map iteration order is random, the IN clause order is random.
		// We use NewRows to return all subscribers.

		// To avoid exact SQL string matching issues with IN (?),
		// we match by regex.
		subRows := sqlmock.NewRows([]string{"channel_id", "subscriber_id"})
		for _, s := range streams {
			for _, subID := range subscribers[s.ChannelID] {
				subRows.AddRow(s.ChannelID, subID)
			}
		}

		dbMock.ExpectQuery("SELECT channel_id, subscriber_id FROM channel_subscriptions").
			WillReturnRows(subRows)

		// 3. Send notifications & Mark sent (loop)
		for _, s := range streams {
			sender.On("SendStreamStartingNotification", mock.Anything, s.ID, mock.Anything).
				Return(nil).Once()

			dbMock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
				WithArgs(s.ID).
				WillReturnResult(sqlmock.NewResult(1, 1))
		}

		b.StartTimer()

		// Execute
		if err := scheduler.sendReminders(context.Background()); err != nil {
			b.Errorf("sendReminders failed: %v", err)
		}
	}
}

func BenchmarkSendReminders_Legacy(b *testing.B) {
	mockDB, dbMock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()
	db := sqlx.NewDb(mockDB, "sqlmock")
	sender := &MockNotificationSender{}
	config := DefaultSchedulerConfig()
	scheduler := NewStreamScheduler(db, sender, config)

	numStreams := 50
	streams, subscribers := setupBenchmarkData(numStreams)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		sender = &MockNotificationSender{}
		scheduler.notificationSender = sender

		// 1. Select streams
		rows := sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"})
		for _, s := range streams {
			rows.AddRow(s.ID, s.ChannelID, s.Title, time.Now().Add(1*time.Hour), false, "scheduled")
		}
		dbMock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
			WillReturnRows(rows)

		// 2. Loop over streams
		for _, s := range streams {
			// a. Get subscribers (Query)
			subRows := sqlmock.NewRows([]string{"subscriber_id"})
			for _, subID := range subscribers[s.ChannelID] {
				subRows.AddRow(subID)
			}
			dbMock.ExpectQuery("SELECT subscriber_id FROM channel_subscriptions").
				WithArgs(s.ChannelID).
				WillReturnRows(subRows)

			// b. Send notification
			sender.On("SendStreamStartingNotification", mock.Anything, s.ID, mock.Anything).
				Return(nil).Once()

			// c. Mark sent
			dbMock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
				WithArgs(s.ID).
				WillReturnResult(sqlmock.NewResult(1, 1))
		}

		b.StartTimer()

		if err := scheduler.sendRemindersLegacy(context.Background()); err != nil {
			b.Errorf("sendRemindersLegacy failed: %v", err)
		}
	}
}
