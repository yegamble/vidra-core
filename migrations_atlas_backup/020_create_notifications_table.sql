-- Create notifications table for user notifications
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    message TEXT,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    read_at TIMESTAMP WITH TIME ZONE
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON notifications(user_id, read);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications(type);

-- Composite index for common query pattern (user's unread notifications ordered by date)
CREATE INDEX IF NOT EXISTS idx_notifications_user_unread_recent
    ON notifications(user_id, created_at DESC)
    WHERE read = FALSE;

-- Function to auto-create notifications for new video uploads
CREATE OR REPLACE FUNCTION notify_subscribers_on_video_upload()
RETURNS TRIGGER AS $$
BEGIN
    -- Only create notifications for completed public videos
    IF NEW.status = 'completed' AND NEW.privacy = 'public' THEN
        INSERT INTO notifications (user_id, type, title, message, data)
        SELECT
            s.subscriber_id,
            'new_video',
            'New video from ' || u.username,
            u.username || ' uploaded: ' || NEW.title,
            jsonb_build_object(
                'video_id', NEW.id,
                'channel_id', NEW.user_id,
                'channel_name', u.username,
                'video_title', NEW.title,
                'thumbnail_cid', NEW.thumbnail_cid
            )
        FROM subscriptions s
        JOIN users u ON u.id = NEW.user_id
        WHERE s.channel_id = NEW.user_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger to create notifications when video status changes to completed
DROP TRIGGER IF EXISTS trg_notify_video_upload ON videos;
CREATE TRIGGER trg_notify_video_upload
AFTER UPDATE OF status ON videos
FOR EACH ROW
WHEN (NEW.status = 'completed' AND OLD.status != 'completed')
EXECUTE FUNCTION notify_subscribers_on_video_upload();

-- Function to clean up old read notifications (optional, can be called periodically)
CREATE OR REPLACE FUNCTION cleanup_old_notifications(days_to_keep INTEGER DEFAULT 30)
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM notifications
    WHERE read = TRUE
    AND read_at < NOW() - INTERVAL '1 day' * days_to_keep;

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;
