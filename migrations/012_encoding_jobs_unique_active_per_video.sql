-- +goose Up
-- +goose StatementBegin
-- Ensure only one active (pending/processing) encoding job per video
CREATE UNIQUE INDEX IF NOT EXISTS uq_encoding_jobs_active_video
ON encoding_jobs (video_id)
WHERE status IN ('pending','processing');
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
