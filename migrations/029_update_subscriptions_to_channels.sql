-- +goose Up
-- +goose StatementBegin
-- Migrate subscriptions from user-based to channel-based
-- Since this is a new system, we'll drop and recreate the subscriptions table

-- Step 1: Drop existing triggers and functions
DROP TRIGGER IF EXISTS trg_subscriptions_inc ON subscriptions;
DROP TRIGGER IF EXISTS trg_subscriptions_dec ON subscriptions;
DROP FUNCTION IF EXISTS increment_subscriber_count();
DROP FUNCTION IF EXISTS decrement_subscriber_count();

-- Step 2: Drop the old subscriptions table
DROP TABLE IF EXISTS subscriptions;

-- Step 3: Remove subscriber_count from users (moving to channels)
ALTER TABLE users DROP COLUMN IF EXISTS subscriber_count;

-- Step 4: Add subscriber_count to channels
ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS subscriber_count BIGINT NOT NULL DEFAULT 0;

-- Step 5: Create new subscriptions table referencing channels
CREATE TABLE subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(subscriber_id, channel_id)
);

-- Step 6: Create indexes for performance
CREATE INDEX idx_subscriptions_subscriber ON subscriptions(subscriber_id);
CREATE INDEX idx_subscriptions_channel ON subscriptions(channel_id);
CREATE INDEX idx_subscriptions_created_at ON subscriptions(created_at DESC);

-- Step 7: Create function to prevent self-subscription
CREATE OR REPLACE FUNCTION prevent_self_subscription()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.subscriber_id = (SELECT account_id FROM channels WHERE id = NEW.channel_id) THEN
        RAISE EXCEPTION 'Cannot subscribe to your own channel';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Step 8: Create trigger to enforce no self-subscription
CREATE TRIGGER trg_prevent_self_subscription
BEFORE INSERT ON subscriptions
FOR EACH ROW
EXECUTE FUNCTION prevent_self_subscription();

-- Step 9: Create trigger functions to maintain channels.subscriber_count
CREATE OR REPLACE FUNCTION increment_channel_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE channels
    SET subscriber_count = subscriber_count + 1,
        updated_at = NOW()
    WHERE id = NEW.channel_id;

    -- Create notification for channel owner
    INSERT INTO notifications (user_id, type, data, created_at)
    SELECT
        account_id,
        'new_subscriber',
        jsonb_build_object(
            'subscriber_id', NEW.subscriber_id,
            'channel_id', NEW.channel_id,
            'channel_name', display_name
        ),
        NOW()
    FROM channels
    WHERE id = NEW.channel_id;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION decrement_channel_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE channels
    SET subscriber_count = GREATEST(subscriber_count - 1, 0),
        updated_at = NOW()
    WHERE id = OLD.channel_id;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

-- Step 10: Create triggers
CREATE TRIGGER trg_subscriptions_inc
AFTER INSERT ON subscriptions
FOR EACH ROW EXECUTE FUNCTION increment_channel_subscriber_count();

CREATE TRIGGER trg_subscriptions_dec
AFTER DELETE ON subscriptions
FOR EACH ROW EXECUTE FUNCTION decrement_channel_subscriber_count();

-- Step 11: Create function to get subscription feed
CREATE OR REPLACE FUNCTION get_subscription_feed(
    p_user_id UUID,
    p_limit INTEGER DEFAULT 20,
    p_offset INTEGER DEFAULT 0
)
RETURNS TABLE (
    video_id UUID,
    title VARCHAR,
    description TEXT,
    channel_id UUID,
    channel_name VARCHAR,
    upload_date TIMESTAMP,
    views BIGINT,
    duration INTEGER
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        v.id::UUID as video_id,
        v.title,
        v.description,
        c.id as channel_id,
        c.display_name as channel_name,
        v.upload_date,
        v.views,
        v.duration
    FROM videos v
    INNER JOIN channels c ON v.channel_id = c.id
    INNER JOIN subscriptions s ON s.channel_id = c.id
    WHERE s.subscriber_id = p_user_id
        AND v.privacy = 'public'
        AND v.status = 'ready'
    ORDER BY v.upload_date DESC
    LIMIT p_limit
    OFFSET p_offset;
END;
$$ LANGUAGE plpgsql;

-- Add comments
COMMENT ON TABLE subscriptions IS 'User subscriptions to channels';
COMMENT ON COLUMN subscriptions.subscriber_id IS 'User who is subscribing';
COMMENT ON COLUMN subscriptions.channel_id IS 'Channel being subscribed to';
COMMENT ON FUNCTION get_subscription_feed IS 'Get subscription feed for a user';
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
