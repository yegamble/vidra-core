-- Reverse 0064.
DROP INDEX IF EXISTS reports_reporter_message_idx;
ALTER TABLE reports
    DROP COLUMN IF EXISTS message_body_snapshot,
    DROP COLUMN IF EXISTS message_id;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('video', 'comment', 'account', 'remote_video'));

DROP TABLE IF EXISTS message_attachments;

ALTER TABLE conversation_participants DROP COLUMN IF EXISTS last_read_message_id;

ALTER TABLE messages
    DROP COLUMN IF EXISTS link_preview_url_hash,
    DROP COLUMN IF EXISTS deleted_at;

DROP TABLE IF EXISTS link_previews;
