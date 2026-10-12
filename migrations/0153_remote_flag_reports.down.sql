-- 0153 down: remote reports have no local reporter, so they go first.
DROP INDEX IF EXISTS reports_remote_flag_target_idx;
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_reporter_kind_check;
DELETE FROM reports WHERE reporter_id IS NULL;
ALTER TABLE reports DROP COLUMN IF EXISTS remote_flag_activity_id,
    DROP COLUMN IF EXISTS remote_reporter_domain, DROP COLUMN IF EXISTS remote_reporter_actor_url;
ALTER TABLE reports ALTER COLUMN reporter_id SET NOT NULL;
