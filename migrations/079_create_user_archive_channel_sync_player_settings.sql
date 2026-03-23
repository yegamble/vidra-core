-- +goose Up
-- User data exports
CREATE TABLE IF NOT EXISTS user_exports (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    state INTEGER NOT NULL DEFAULT 1,
    file_path TEXT NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '48 hours')
);
CREATE INDEX idx_user_exports_user_id ON user_exports(user_id);

-- User data imports
CREATE TABLE IF NOT EXISTS user_imports (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    state INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_user_imports_user_id ON user_imports(user_id);

-- Video channel syncs
CREATE TABLE IF NOT EXISTS video_channel_syncs (
    id BIGSERIAL PRIMARY KEY,
    channel_id TEXT NOT NULL,
    external_channel_url TEXT NOT NULL,
    state INTEGER NOT NULL DEFAULT 3,
    last_sync_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_video_channel_syncs_channel_id ON video_channel_syncs(channel_id);

-- Player settings
CREATE TABLE IF NOT EXISTS player_settings (
    id BIGSERIAL PRIMARY KEY,
    video_id TEXT,
    channel_handle TEXT,
    autoplay BOOLEAN NOT NULL DEFAULT true,
    loop BOOLEAN NOT NULL DEFAULT false,
    default_quality TEXT NOT NULL DEFAULT '720p',
    default_speed DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    subtitles_enabled BOOLEAN NOT NULL DEFAULT false,
    theatre BOOLEAN NOT NULL DEFAULT false,
    CONSTRAINT player_settings_target CHECK (video_id IS NOT NULL OR channel_handle IS NOT NULL)
);
CREATE UNIQUE INDEX idx_player_settings_video_id ON player_settings(video_id) WHERE video_id IS NOT NULL;
CREATE UNIQUE INDEX idx_player_settings_channel_handle ON player_settings(channel_handle) WHERE channel_handle IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS player_settings;
DROP TABLE IF EXISTS video_channel_syncs;
DROP TABLE IF EXISTS user_imports;
DROP TABLE IF EXISTS user_exports;
