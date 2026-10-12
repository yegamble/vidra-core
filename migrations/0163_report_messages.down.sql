DROP INDEX IF EXISTS notifications_report_message_unique_idx;
ALTER TABLE notifications DROP COLUMN IF EXISTS report_message_id;
DROP TABLE IF EXISTS report_messages;
