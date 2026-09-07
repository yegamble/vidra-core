-- Reverse 0135. Dropping the counter loses only the in-flight backoff position;
-- next_run_at keeps whatever the last run scheduled, so the older code resumes
-- at its plain cadence from there.
ALTER TABLE channel_syncs
    DROP COLUMN IF EXISTS failure_count;
