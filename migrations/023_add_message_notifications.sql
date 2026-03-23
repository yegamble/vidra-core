-- +goose Up
-- +goose StatementBegin
-- Add trigger to create notifications for new messages
-- This ensures recipients are notified when they receive a new message

-- Function to create notification when a new message is received
CREATE OR REPLACE FUNCTION notify_on_new_message()
RETURNS TRIGGER AS $$
DECLARE
    sender_username VARCHAR(255);
    message_preview TEXT;
BEGIN
    -- Only create notification for non-system messages
    IF NEW.message_type != 'system' THEN
        -- Get sender username
        SELECT username INTO sender_username
        FROM users
        WHERE id = NEW.sender_id;

        -- Create a preview of the message (first 100 chars)
        message_preview := CASE
            WHEN LENGTH(NEW.content) > 100 THEN
                SUBSTRING(NEW.content FROM 1 FOR 97) || '...'
            ELSE
                NEW.content
        END;

        -- Create notification for the recipient
        INSERT INTO notifications (user_id, type, title, message, data)
        VALUES (
            NEW.recipient_id,
            'new_message',
            'New message from ' || COALESCE(sender_username, 'Unknown'),
            message_preview,
            jsonb_build_object(
                'message_id', NEW.id,
                'sender_id', NEW.sender_id,
                'sender_name', COALESCE(sender_username, 'Unknown'),
                'message_preview', message_preview,
                'conversation_id', NEW.id -- In this simple case, using message ID as conversation reference
            )
        );
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for new messages
DROP TRIGGER IF EXISTS trg_notify_new_message ON messages;
CREATE TRIGGER trg_notify_new_message
AFTER INSERT ON messages
FOR EACH ROW
EXECUTE FUNCTION notify_on_new_message();

-- Optional: Function to create read receipt notifications
-- This can be used if you want to notify senders when their messages are read
CREATE OR REPLACE FUNCTION notify_on_message_read()
RETURNS TRIGGER AS $$
DECLARE
    recipient_username VARCHAR(255);
    sender_id UUID;
BEGIN
    -- Only proceed if message is being marked as read (not already read)
    IF NEW.is_read = TRUE AND OLD.is_read = FALSE THEN
        -- Get recipient username
        SELECT username INTO recipient_username
        FROM users
        WHERE id = NEW.recipient_id;

        -- Get sender ID
        sender_id := NEW.sender_id;

        -- Create read receipt notification for the sender (optional - can be enabled/disabled)
        -- Uncomment the following if you want read receipts
        /*
        INSERT INTO notifications (user_id, type, title, message, data)
        VALUES (
            sender_id,
            'message_read',
            'Message read',
            COALESCE(recipient_username, 'Unknown') || ' read your message',
            jsonb_build_object(
                'message_id', NEW.id,
                'reader_id', NEW.recipient_id,
                'reader_name', COALESCE(recipient_username, 'Unknown'),
                'read_at', NEW.read_at
            )
        );
        */
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for message read events (optional)
DROP TRIGGER IF EXISTS trg_notify_message_read ON messages;
CREATE TRIGGER trg_notify_message_read
AFTER UPDATE OF is_read ON messages
FOR EACH ROW
WHEN (NEW.is_read = TRUE AND OLD.is_read = FALSE)
EXECUTE FUNCTION notify_on_message_read();

-- Add index for better performance on message queries
CREATE INDEX IF NOT EXISTS idx_messages_recipient_unread
    ON messages(recipient_id, created_at DESC)
    WHERE is_read = FALSE;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
