-- Reverse 0120: back to synchronous upload completion.
--
-- Any session parked in one of the new states is folded onto the pre-0120
-- vocabulary before the CHECK narrows again, otherwise the ADD CONSTRAINT would
-- fail on live rows: a session mid-finalize is, from the old code's point of
-- view, simply an unfinished upload ('active'), and a dead-lettered one is a
-- session that will never finish ('cancelled', which the sweeper collects).
DROP TRIGGER IF EXISTS upload_finalize_jobs_operational_projection ON upload_finalize_jobs;
DROP FUNCTION IF EXISTS sync_upload_finalize_job_run();
DROP TABLE IF EXISTS upload_finalize_jobs;
DELETE FROM job_events WHERE job_id IN (SELECT id FROM job_runs WHERE queue = 'upload_finalize_jobs');
DELETE FROM job_runs WHERE queue = 'upload_finalize_jobs';

UPDATE upload_sessions SET state = 'active' WHERE state IN ('queued', 'processing');
UPDATE upload_sessions SET state = 'cancelled' WHERE state = 'failed';
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_state_check;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_state_check
    CHECK (state IN ('active', 'completed', 'cancelled'));
ALTER TABLE upload_sessions DROP COLUMN IF EXISTS failure_reason;
