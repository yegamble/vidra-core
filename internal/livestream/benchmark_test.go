package livestream

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/mock"
)

func BenchmarkSendReminders(b *testing.B) {
	// Setup
	mockDB, sqlMock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer mockDB.Close()

	db := sqlx.NewDb(mockDB, "sqlmock")
	sender := &MockNotificationSender{}
	config := DefaultSchedulerConfig()
	scheduler := NewStreamScheduler(db, sender, config)

	// Number of streams to process per iteration
	numStreams := 100
	streams := make([]ScheduledStream, numStreams)
	channelIDs := make([]uuid.UUID, numStreams)
	subscriberID := uuid.New()

	for i := 0; i < numStreams; i++ {
		channelIDs[i] = uuid.New()
		t := time.Now().Add(10 * time.Minute)
		streams[i] = ScheduledStream{
			ID:             uuid.New(),
			ChannelID:      channelIDs[i],
			Title:          fmt.Sprintf("Stream %d", i),
			ScheduledStart: &t,
			ReminderSent:   false,
			Status:         "scheduled",
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()

		// Setup expectations for this run
		// 1. Get streams
		rows := sqlmock.NewRows([]string{"id", "channel_id", "title", "scheduled_start", "reminder_sent", "status"})
		for _, s := range streams {
			rows.AddRow(s.ID, s.ChannelID, s.Title, s.ScheduledStart, s.ReminderSent, s.Status)
		}
		// Match any query starting with SELECT id...
		sqlMock.ExpectQuery("SELECT id, channel_id, title, scheduled_start").
			WillReturnRows(rows)

		// 2. Get subscribers (Batch)
		subRows := sqlmock.NewRows([]string{"channel_id", "subscriber_id"})
		for _, cid := range channelIDs {
			subRows.AddRow(cid, subscriberID)
		}
		// Match IN query
		sqlMock.ExpectQuery("SELECT channel_id, subscriber_id FROM channel_subscriptions").
			WillReturnRows(subRows)

		// 3. Mock Sender
		// We clear previous calls to avoid memory buildup
		sender.Calls = nil
		sender.ExpectedCalls = nil
		sender.On("SendStreamStartingNotification", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// 4. Mark reminder sent (Batch)
		// We expect ONE update with all IDs
		// Construct args for match
		args := make([]driver.Value, numStreams)
		for k, s := range streams {
			args[k] = s.ID
		}

		sqlMock.ExpectExec("UPDATE live_streams SET reminder_sent = true").
			WithArgs(args...).
			WillReturnResult(sqlmock.NewResult(int64(numStreams), int64(numStreams)))

		b.StartTimer()

		// Run the function
		err := scheduler.sendReminders(context.Background())
		if err != nil {
			b.Fatalf("sendReminders failed: %v", err)
		}

		b.StopTimer()

		// Verify expectations
		if err := sqlMock.ExpectationsWereMet(); err != nil {
			b.Fatalf("there were unfulfilled expectations: %s", err)
		}
	}
}
