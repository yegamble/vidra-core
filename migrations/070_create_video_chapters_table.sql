-- +goose Up
CREATE TABLE video_chapters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    timecode INTEGER NOT NULL CHECK (timecode >= 0),
    title TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_video_chapters_video_id ON video_chapters(video_id);
CREATE INDEX idx_video_chapters_position ON video_chapters(video_id, position);

-- +goose Down
DROP TABLE IF EXISTS video_chapters;
