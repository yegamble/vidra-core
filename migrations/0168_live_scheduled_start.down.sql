DROP INDEX IF EXISTS live_streams_upcoming_idx;
ALTER TABLE live_streams DROP COLUMN IF EXISTS scheduled_start_at;
