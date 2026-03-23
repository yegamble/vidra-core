-- +goose Up
-- +goose StatementBegin
CREATE TABLE video_blacklist (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    reason TEXT NOT NULL DEFAULT '',
    unfederated BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (video_id)
);

CREATE INDEX idx_video_blacklist_video_id ON video_blacklist(video_id);
CREATE INDEX idx_video_blacklist_created_at ON video_blacklist(created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS video_blacklist;
-- +goose StatementEnd
