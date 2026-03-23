-- +goose Up
CREATE TABLE IF NOT EXISTS video_ownership_changes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id      TEXT NOT NULL,
    initiator_id  TEXT NOT NULL,
    next_owner_id TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'accepted', 'refused')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_video_ownership_changes_video ON video_ownership_changes(video_id);
CREATE INDEX IF NOT EXISTS idx_video_ownership_changes_next_owner ON video_ownership_changes(next_owner_id, status);

-- +goose Down
DROP TABLE IF EXISTS video_ownership_changes;
