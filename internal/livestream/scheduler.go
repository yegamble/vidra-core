package livestream

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// SchedulerConfig holds configuration for the stream scheduler
type SchedulerConfig struct {
	CheckInterval       time.Duration
	NotificationAdvance time.Duration
}

// DefaultSchedulerConfig returns default scheduler configuration
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		CheckInterval:       1 * time.Minute,
		NotificationAdvance: 15 * time.Minute,
	}
}

// StreamScheduler handles scheduling and notifications for streams
type StreamScheduler struct {
	db                 *sqlx.DB
	notificationSender NotificationSender
	config             SchedulerConfig
	ticker             *time.Ticker
	done               chan bool
	wg                 sync.WaitGroup
	mu                 sync.RWMutex
	running            bool
}

// NotificationSender interface for sending notifications
type NotificationSender interface {
	SendStreamStartingNotification(ctx context.Context, streamID uuid.UUID, subscribers []uuid.UUID) error
	SendStreamLiveNotification(ctx context.Context, streamID uuid.UUID, subscribers []uuid.UUID) error
}

// ScheduledStream represents a scheduled stream
type ScheduledStream struct {
	ID                 uuid.UUID  `db:"id"`
	ChannelID          uuid.UUID  `db:"channel_id"`
	Title              string     `db:"title"`
	ScheduledStart     *time.Time `db:"scheduled_start"`
	ScheduledEnd       *time.Time `db:"scheduled_end"`
	ReminderSent       bool       `db:"reminder_sent"`
	Status             string     `db:"status"`
	WaitingRoomEnabled bool       `db:"waiting_room_enabled"`
}

// NewStreamScheduler creates a new stream scheduler
func NewStreamScheduler(db *sqlx.DB, sender NotificationSender, config SchedulerConfig) *StreamScheduler {
	return &StreamScheduler{
		db:                 db,
		notificationSender: sender,
		config:             config,
		done:               make(chan bool),
	}
}

// Start begins the scheduler background process
func (s *StreamScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("scheduler already running")
	}

	s.ticker = time.NewTicker(s.config.CheckInterval)
	s.running = true

	s.wg.Add(1)
	go s.run(ctx)

	log.Printf("Stream scheduler started with check interval: %v", s.config.CheckInterval)
	return nil
}

// Stop gracefully stops the scheduler
func (s *StreamScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	log.Println("Stopping stream scheduler...")

	if s.ticker != nil {
		s.ticker.Stop()
	}

	close(s.done)
	s.wg.Wait()
	s.running = false

	log.Println("Stream scheduler stopped")
}

// IsRunning returns whether the scheduler is running
func (s *StreamScheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// run is the main scheduler loop
func (s *StreamScheduler) run(ctx context.Context) {
	defer s.wg.Done()

	// Check immediately on start
	if err := s.checkScheduledStreams(ctx); err != nil {
		log.Printf("Error checking scheduled streams: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("Scheduler context cancelled")
			return
		case <-s.done:
			log.Println("Scheduler received stop signal")
			return
		case <-s.ticker.C:
			if err := s.checkScheduledStreams(ctx); err != nil {
				log.Printf("Error checking scheduled streams: %v", err)
			}
		}
	}
}

// checkScheduledStreams checks for streams that need notifications or status updates
func (s *StreamScheduler) checkScheduledStreams(ctx context.Context) error {
	// Check for streams needing reminder notifications
	if err := s.sendReminders(ctx); err != nil {
		log.Printf("Error sending reminders: %v", err)
	}

	// Check for streams that should transition to waiting room
	if err := s.transitionToWaitingRoom(ctx); err != nil {
		log.Printf("Error transitioning streams to waiting room: %v", err)
	}

	// Check for streams that should go live
	if err := s.transitionToLive(ctx); err != nil {
		log.Printf("Error transitioning streams to live: %v", err)
	}

	return nil
}

// sendReminders sends reminder notifications for upcoming streams
func (s *StreamScheduler) sendReminders(ctx context.Context) error {
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

	for _, stream := range streams {
		// Get subscribers for the channel
		subscribers, err := s.getChannelSubscribers(ctx, stream.ChannelID)
		if err != nil {
			log.Printf("Failed to get subscribers for channel %s: %v", stream.ChannelID, err)
			continue
		}

		// Send notifications
		if s.notificationSender != nil && len(subscribers) > 0 {
			if err := s.notificationSender.SendStreamStartingNotification(ctx, stream.ID, subscribers); err != nil {
				log.Printf("Failed to send notifications for stream %s: %v", stream.ID, err)
				continue
			}
		}

		// Mark reminder as sent
		if err := s.markReminderSent(ctx, stream.ID); err != nil {
			log.Printf("Failed to mark reminder sent for stream %s: %v", stream.ID, err)
			continue
		}

		log.Printf("Sent reminder notifications for stream %s (%s)", stream.ID, stream.Title)
	}

	return nil
}

// transitionToWaitingRoom transitions eligible streams to waiting room status
func (s *StreamScheduler) transitionToWaitingRoom(ctx context.Context) error {
	query := `
		UPDATE live_streams
		SET status = 'waiting_room', updated_at = NOW()
		WHERE status = 'scheduled'
		AND waiting_room_enabled = true
		AND scheduled_start <= NOW()
		RETURNING id, title
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to transition streams to waiting room: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Failed to close rows in transitionToWaitingRoom: %v", err)
		}
	}()

	for rows.Next() {
		var id uuid.UUID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			log.Printf("Error scanning stream: %v", err)
			continue
		}
		log.Printf("Stream %s (%s) transitioned to waiting room", id, title)
	}

	return rows.Err()
}

// transitionToLive transitions streams from waiting room or scheduled to live
func (s *StreamScheduler) transitionToLive(ctx context.Context) error {
	// For streams without waiting room, go directly to live
	query := `
		UPDATE live_streams
		SET status = 'live', updated_at = NOW()
		WHERE status = 'scheduled'
		AND waiting_room_enabled = false
		AND scheduled_start <= NOW()
		RETURNING id, title
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to transition scheduled streams to live: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Failed to close rows in transitionToLive: %v", err)
		}
	}()

	for rows.Next() {
		var id uuid.UUID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			log.Printf("Error scanning stream: %v", err)
			continue
		}
		log.Printf("Stream %s (%s) transitioned from scheduled to live", id, title)
	}

	return rows.Err()
}

// markReminderSent marks that a reminder has been sent for a stream
func (s *StreamScheduler) markReminderSent(ctx context.Context, streamID uuid.UUID) error {
	query := `
		UPDATE live_streams
		SET reminder_sent = true, updated_at = NOW()
		WHERE id = $1
	`

	result, err := s.db.ExecContext(ctx, query, streamID)
	if err != nil {
		return fmt.Errorf("failed to mark reminder sent: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("stream not found or already marked: %s", streamID)
	}

	return nil
}

// getChannelSubscribers gets all subscribers for a channel
func (s *StreamScheduler) getChannelSubscribers(ctx context.Context, channelID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		SELECT subscriber_id
		FROM channel_subscriptions
		WHERE channel_id = $1
		AND subscribed_at IS NOT NULL
		ORDER BY subscribed_at DESC
	`

	var subscribers []uuid.UUID
	if err := s.db.SelectContext(ctx, &subscribers, query, channelID); err != nil {
		if err == sql.ErrNoRows {
			return []uuid.UUID{}, nil
		}
		return nil, fmt.Errorf("failed to get channel subscribers: %w", err)
	}

	return subscribers, nil
}

// GetUpcomingStreams gets all upcoming scheduled streams
func (s *StreamScheduler) GetUpcomingStreams(ctx context.Context, limit int) ([]ScheduledStream, error) {
	query := `
		SELECT id, channel_id, title, scheduled_start, scheduled_end,
			   reminder_sent, status, waiting_room_enabled
		FROM live_streams
		WHERE status IN ('scheduled', 'waiting_room')
		AND scheduled_start IS NOT NULL
		ORDER BY scheduled_start ASC
		LIMIT $1
	`

	var streams []ScheduledStream
	if err := s.db.SelectContext(ctx, &streams, query, limit); err != nil {
		return nil, fmt.Errorf("failed to get upcoming streams: %w", err)
	}

	return streams, nil
}

// GetStreamsByTimeRange gets streams scheduled within a time range
func (s *StreamScheduler) GetStreamsByTimeRange(ctx context.Context, start, end time.Time) ([]ScheduledStream, error) {
	query := `
		SELECT id, channel_id, title, scheduled_start, scheduled_end,
			   reminder_sent, status, waiting_room_enabled
		FROM live_streams
		WHERE scheduled_start >= $1
		AND scheduled_start <= $2
		ORDER BY scheduled_start ASC
	`

	var streams []ScheduledStream
	if err := s.db.SelectContext(ctx, &streams, query, start, end); err != nil {
		return nil, fmt.Errorf("failed to get streams by time range: %w", err)
	}

	return streams, nil
}

// ScheduleStream schedules a new stream
func (s *StreamScheduler) ScheduleStream(ctx context.Context, streamID uuid.UUID, scheduledStart time.Time, waitingRoomEnabled bool, waitingRoomMessage string) error {
	query := `
		UPDATE live_streams
		SET scheduled_start = $2,
			waiting_room_enabled = $3,
			waiting_room_message = $4,
			status = 'scheduled',
			updated_at = NOW()
		WHERE id = $1
	`

	result, err := s.db.ExecContext(ctx, query, streamID, scheduledStart, waitingRoomEnabled, waitingRoomMessage)
	if err != nil {
		return fmt.Errorf("failed to schedule stream: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("stream not found: %s", streamID)
	}

	log.Printf("Stream %s scheduled for %v", streamID, scheduledStart)
	return nil
}
