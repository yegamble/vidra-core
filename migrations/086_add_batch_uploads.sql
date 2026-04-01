-- +goose Up
-- +goose StatementBegin

-- Create batch_uploads table for batch upload orchestration
CREATE TABLE IF NOT EXISTS batch_uploads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    total_videos INTEGER NOT NULL CHECK (total_videos > 0),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_batch_uploads_user_id ON batch_uploads(user_id);

-- Trigger to update updated_at
DROP TRIGGER IF EXISTS update_batch_uploads_updated_at ON batch_uploads;
CREATE TRIGGER update_batch_uploads_updated_at
    BEFORE UPDATE ON batch_uploads
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Add batch_id column to upload_sessions (nullable for backward compatibility)
ALTER TABLE upload_sessions ADD COLUMN IF NOT EXISTS batch_id UUID REFERENCES batch_uploads(id) ON DELETE SET NULL;

-- Index for batch status queries
CREATE INDEX IF NOT EXISTS idx_upload_sessions_batch_id ON upload_sessions(batch_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_upload_sessions_batch_id;
ALTER TABLE upload_sessions DROP COLUMN IF EXISTS batch_id;
DROP TRIGGER IF EXISTS update_batch_uploads_updated_at ON batch_uploads;
DROP TABLE IF EXISTS batch_uploads;

-- +goose StatementEnd
