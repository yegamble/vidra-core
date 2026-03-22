-- +goose Up
-- +goose StatementBegin
-- Fix subscription-side notification triggers to match the current notifications schema
-- and channel-based subscription model.

CREATE OR REPLACE FUNCTION increment_channel_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE channels
    SET subscriber_count = subscriber_count + 1,
        updated_at = NOW()
    WHERE id = NEW.channel_id;

    INSERT INTO notifications (user_id, type, title, message, data, created_at)
    SELECT
        c.account_id,
        'new_subscriber',
        'New subscriber',
        'Someone subscribed to your channel ' || c.display_name,
        jsonb_build_object(
            'subscriber_id', NEW.subscriber_id,
            'channel_id', NEW.channel_id,
            'channel_name', c.display_name
        ),
        NOW()
    FROM channels c
    WHERE c.id = NEW.channel_id;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION notify_subscribers_on_video_upload()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'completed' AND NEW.privacy = 'public' THEN
        INSERT INTO notifications (user_id, type, title, message, data)
        SELECT
            s.subscriber_id,
            'new_video',
            'New video from ' || COALESCE(c.display_name, u.username),
            COALESCE(c.display_name, u.username) || ' uploaded: ' || NEW.title,
            jsonb_build_object(
                'video_id', NEW.id,
                'channel_id', NEW.channel_id,
                'channel_name', COALESCE(c.display_name, u.username),
                'video_title', NEW.title,
                'thumbnail_cid', NEW.thumbnail_cid
            )
        FROM subscriptions s
        JOIN channels c ON c.id = NEW.channel_id
        JOIN users u ON u.id = c.account_id
        WHERE s.channel_id = NEW.channel_id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Forward-only migration.
