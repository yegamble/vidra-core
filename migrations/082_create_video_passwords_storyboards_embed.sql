-- +goose Up

-- Video passwords: password-protect individual videos
CREATE TABLE IF NOT EXISTS video_passwords (
    id BIGSERIAL PRIMARY KEY,
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_video_passwords_video_id ON video_passwords(video_id);

-- Video storyboards: sprite-sheet thumbnails for scrubbing
CREATE TABLE IF NOT EXISTS video_storyboards (
    id BIGSERIAL PRIMARY KEY,
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    total_height INTEGER NOT NULL DEFAULT 0,
    total_width INTEGER NOT NULL DEFAULT 0,
    sprite_height INTEGER NOT NULL DEFAULT 0,
    sprite_width INTEGER NOT NULL DEFAULT 0,
    sprite_duration DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_video_storyboards_video_id ON video_storyboards(video_id);

-- Video embed privacy: control where a video can be embedded
CREATE TABLE IF NOT EXISTS video_embed_privacy (
    video_id UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    status INTEGER NOT NULL DEFAULT 1
);

-- Allowed domains for embed whitelisting
CREATE TABLE IF NOT EXISTS video_embed_allowed_domains (
    id BIGSERIAL PRIMARY KEY,
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    domain TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_video_embed_allowed_domains_video_id ON video_embed_allowed_domains(video_id);

-- +goose Down
DROP TABLE IF EXISTS video_embed_allowed_domains;
DROP TABLE IF EXISTS video_embed_privacy;
DROP TABLE IF EXISTS video_storyboards;
DROP TABLE IF EXISTS video_passwords;
