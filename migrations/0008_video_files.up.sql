-- 0008: video_files. The stored blobs backing a video. For now this is just the
-- original uploaded file (kind='original'); transcoded renditions land with the
-- pipeline (kind='rendition'). storage_key is the backend object key, laid out
-- PeerTube-style (one dir per asset kind, see .ralph/specs/storage-layout.md;
-- e.g. web-videos/<video_id>.mp4) — opaque to the database.

CREATE TABLE video_files (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id      UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL DEFAULT 'original'
                      CHECK (kind IN ('original', 'rendition')),
    storage_key   TEXT NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    original_name TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX video_files_video_id_idx ON video_files (video_id);
