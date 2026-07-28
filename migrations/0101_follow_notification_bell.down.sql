DROP INDEX IF EXISTS notifications_new_video_unique_idx;

DROP INDEX IF EXISTS channel_follows_notify_idx;

ALTER TABLE channel_follows
    DROP COLUMN notification_setting;
