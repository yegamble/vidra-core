-- +goose Up
CREATE TABLE IF NOT EXISTS live_stream_sessions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id     UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at      TIMESTAMPTZ,
    peak_viewers  INTEGER,
    total_seconds INTEGER,
    avg_viewers   INTEGER
);

CREATE INDEX IF NOT EXISTS idx_live_stream_sessions_stream_id ON live_stream_sessions(stream_id);

-- +goose Down
DROP TABLE IF EXISTS live_stream_sessions;
