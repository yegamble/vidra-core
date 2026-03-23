-- Ensure only one active (pending/processing) encoding job per video
CREATE UNIQUE INDEX IF NOT EXISTS uq_encoding_jobs_active_video
ON encoding_jobs (video_id)
WHERE status IN ('pending','processing');
