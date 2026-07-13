DROP TRIGGER IF EXISTS caption_jobs_progress_projection ON caption_jobs;
DROP FUNCTION IF EXISTS sync_caption_job_progress();
DROP TRIGGER IF EXISTS transcode_steps_operational_projection ON transcode_steps;
DROP FUNCTION IF EXISTS sync_transcode_step_job_run();
-- Resolution job_runs are projections, not FK children of transcode_steps;
-- remove them explicitly so rollback cannot leave orphaned executions/events.
DELETE FROM job_runs WHERE queue = 'transcode_steps';
DROP TABLE IF EXISTS transcode_steps;
ALTER TABLE caption_jobs DROP COLUMN IF EXISTS progress_percent, DROP COLUMN IF EXISTS stage;
ALTER TABLE video_renditions DROP COLUMN IF EXISTS size_bytes;
ALTER TABLE transcode_jobs DROP COLUMN IF EXISTS transcode_type;
