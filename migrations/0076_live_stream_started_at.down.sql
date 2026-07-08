-- 0076 down: drop the live-session start time (and its partial listing index).
DROP INDEX IF EXISTS live_streams_live_public_idx;
ALTER TABLE live_streams
    DROP COLUMN IF EXISTS started_at;
