-- 009_alter_videos_add_outputs.sql

ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS output_paths JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS thumbnail_path TEXT,
    ADD COLUMN IF NOT EXISTS preview_path TEXT;

