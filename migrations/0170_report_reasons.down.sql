DROP INDEX IF EXISTS reports_predefined_reasons_idx;
ALTER TABLE reports
    DROP CONSTRAINT IF EXISTS reports_video_range_chk,
    DROP COLUMN IF EXISTS video_end_seconds,
    DROP COLUMN IF EXISTS video_start_seconds,
    DROP COLUMN IF EXISTS predefined_reasons;
