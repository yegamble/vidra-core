-- 0034: Live streams. A channel owner creates a live stream (metadata + a private
-- stream key); RTMP ingestion and HLS output are a later integration boundary. The
-- stream key is a bearer credential the streamer puts into OBS, so only its SHA-256
-- hash is stored (never the raw key) — the raw key is returned once on create and on
-- regeneration. `permanent` distinguishes a reusable/recurring live (key persists
-- across sessions) from a one-shot live. `state` is flipped by the ingest boundary
-- when it lands; it starts offline.
CREATE TABLE live_streams (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id      UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    privacy         TEXT NOT NULL DEFAULT 'public' CHECK (privacy IN ('public', 'unlisted', 'private')),
    state           TEXT NOT NULL DEFAULT 'offline' CHECK (state IN ('offline', 'live', 'ended')),
    permanent       BOOLEAN NOT NULL DEFAULT false,
    stream_key_hash TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- List a channel's live streams newest-first.
CREATE INDEX live_streams_channel_idx ON live_streams (channel_id, created_at DESC);
-- The ingest boundary will look a stream up by its key hash — one stream per key.
CREATE UNIQUE INDEX live_streams_key_hash_idx ON live_streams (stream_key_hash);
