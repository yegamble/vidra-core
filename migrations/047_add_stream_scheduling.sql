-- +goose Up
-- +goose StatementBegin
-- Add stream scheduling and waiting room support to live_streams table
-- Migration: 047_add_stream_scheduling.sql
-- Sprint 7 - Phase 2: Stream Scheduling & Waiting Rooms

-- Add scheduling and waiting room columns to live_streams table
ALTER TABLE live_streams
ADD COLUMN IF NOT EXISTS scheduled_start TIMESTAMP,
ADD COLUMN IF NOT EXISTS scheduled_end TIMESTAMP,
ADD COLUMN IF NOT EXISTS waiting_room_enabled BOOLEAN DEFAULT false,
ADD COLUMN IF NOT EXISTS waiting_room_message TEXT,
ADD COLUMN IF NOT EXISTS reminder_sent BOOLEAN DEFAULT false;

-- Add check constraint for scheduled times
ALTER TABLE live_streams
ADD CONSTRAINT check_scheduled_times
CHECK (scheduled_end IS NULL OR scheduled_end > scheduled_start);

-- Create table for tracking notification sent status
CREATE TABLE IF NOT EXISTS stream_notifications_sent (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_type VARCHAR(50) NOT NULL, -- 'scheduled', 'starting_soon', 'live'
    sent_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(stream_id, user_id, notification_type)
);

-- Create indexes for scheduled stream queries
CREATE INDEX IF NOT EXISTS idx_live_streams_scheduled_start
ON live_streams(scheduled_start)
WHERE scheduled_start IS NOT NULL AND status = 'scheduled';

CREATE INDEX IF NOT EXISTS idx_live_streams_reminder_sent
ON live_streams(scheduled_start, reminder_sent)
WHERE scheduled_start IS NOT NULL AND reminder_sent = false AND status = 'scheduled';

CREATE INDEX IF NOT EXISTS idx_stream_notifications_sent_stream_user
ON stream_notifications_sent(stream_id, user_id);

-- Update the CHECK constraint to include 'scheduled' and 'waiting_room' status
ALTER TABLE live_streams DROP CONSTRAINT IF EXISTS live_streams_status_check;
ALTER TABLE live_streams ADD CONSTRAINT live_streams_status_check
    CHECK (status IN ('waiting', 'live', 'ended', 'error', 'scheduled', 'waiting_room'));

-- Update the valid_status_times constraint to handle new statuses
ALTER TABLE live_streams DROP CONSTRAINT IF EXISTS valid_status_times;
ALTER TABLE live_streams ADD CONSTRAINT valid_status_times CHECK (
    (status = 'live' AND started_at IS NOT NULL) OR
    (status = 'ended' AND started_at IS NOT NULL AND ended_at IS NOT NULL) OR
    (status IN ('waiting', 'error', 'scheduled', 'waiting_room'))
);

-- Function to get upcoming scheduled streams for a user
CREATE OR REPLACE FUNCTION get_upcoming_streams_for_user(p_user_id UUID)
RETURNS TABLE (
    stream_id UUID,
    title VARCHAR(255),
    channel_id UUID,
    channel_name VARCHAR(100),
    scheduled_start TIMESTAMP,
    scheduled_end TIMESTAMP,
    waiting_room_enabled BOOLEAN,
    waiting_room_message TEXT
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        ls.id AS stream_id,
        ls.title,
        ls.channel_id,
        c.name AS channel_name,
        ls.scheduled_start,
        ls.scheduled_end,
        ls.waiting_room_enabled,
        ls.waiting_room_message
    FROM live_streams ls
    JOIN channels c ON ls.channel_id = c.id
    JOIN channel_subscriptions cs ON cs.channel_id = c.id
    WHERE cs.subscriber_id = p_user_id
    AND ls.status = 'scheduled'
    AND ls.scheduled_start > NOW()
    ORDER BY ls.scheduled_start ASC;
END;
$$ LANGUAGE plpgsql;

-- Function to get streams that need reminder notifications
CREATE OR REPLACE FUNCTION get_streams_needing_reminders(p_minutes_before INT DEFAULT 15)
RETURNS TABLE (
    stream_id UUID,
    channel_id UUID,
    title VARCHAR(255),
    scheduled_start TIMESTAMP
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        ls.id AS stream_id,
        ls.channel_id,
        ls.title,
        ls.scheduled_start
    FROM live_streams ls
    WHERE ls.status = 'scheduled'
    AND ls.reminder_sent = false
    AND ls.scheduled_start IS NOT NULL
    AND ls.scheduled_start BETWEEN NOW() AND NOW() + INTERVAL '1 minute' * p_minutes_before;
END;
$$ LANGUAGE plpgsql;

-- Function to mark reminder as sent
CREATE OR REPLACE FUNCTION mark_reminder_sent(p_stream_id UUID)
RETURNS BOOLEAN AS $$
DECLARE
    v_row_count INTEGER;
BEGIN
    UPDATE live_streams
    SET reminder_sent = true
    WHERE id = p_stream_id;

    GET DIAGNOSTICS v_row_count = ROW_COUNT;
    RETURN v_row_count > 0;
END;
$$ LANGUAGE plpgsql;

-- Function to transition stream from scheduled to waiting room
CREATE OR REPLACE FUNCTION transition_to_waiting_room(p_stream_id UUID)
RETURNS BOOLEAN AS $$
DECLARE
    v_row_count INTEGER;
BEGIN
    UPDATE live_streams
    SET status = 'waiting_room'
    WHERE id = p_stream_id
    AND status = 'scheduled'
    AND waiting_room_enabled = true
    AND scheduled_start <= NOW();

    GET DIAGNOSTICS v_row_count = ROW_COUNT;
    RETURN v_row_count > 0;
END;
$$ LANGUAGE plpgsql;

-- The status constraint is already updated above to include 'waiting_room'

-- Add comment explaining the new columns
COMMENT ON COLUMN live_streams.scheduled_start IS 'When the stream is scheduled to start';
COMMENT ON COLUMN live_streams.scheduled_end IS 'When the stream is scheduled to end (optional)';
COMMENT ON COLUMN live_streams.waiting_room_enabled IS 'Whether to show a waiting room before stream start';
COMMENT ON COLUMN live_streams.waiting_room_message IS 'Custom message to display in the waiting room';
COMMENT ON COLUMN live_streams.reminder_sent IS 'Whether the pre-stream reminder notification has been sent';

COMMENT ON TABLE stream_notifications_sent IS 'Track which notifications have been sent to users for each stream';
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
