-- +goose Up
-- +goose StatementBegin
-- Add category_id to videos table
ALTER TABLE videos
ADD COLUMN category_id UUID REFERENCES video_categories(id) ON DELETE SET NULL;

-- Create index for category lookups
CREATE INDEX idx_videos_category_id ON videos(category_id);

-- Set default category for existing videos (set to 'Other')
UPDATE videos
SET category_id = (SELECT id FROM video_categories WHERE slug = 'other')
WHERE category_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
