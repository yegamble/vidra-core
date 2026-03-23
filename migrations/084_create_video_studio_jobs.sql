-- +goose Up
CREATE TABLE IF NOT EXISTS video_studio_jobs (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    tasks JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_video_studio_jobs_video_id ON video_studio_jobs(video_id);
CREATE INDEX idx_video_studio_jobs_user_id ON video_studio_jobs(user_id);
CREATE INDEX idx_video_studio_jobs_status ON video_studio_jobs(status);

-- +goose Down
DROP TABLE IF EXISTS video_studio_jobs;
