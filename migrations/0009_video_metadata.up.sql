-- 0009: video_metadata. Technical media metadata extracted from a video's
-- original by the probe step (FFprobe), kept in a 1:1 side table so the core
-- videos row and its queries are untouched. All measures are nullable: a probe
-- may not determine every field (e.g. audio-only has no dimensions).

CREATE TABLE video_metadata (
    video_id         UUID PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    duration_seconds INTEGER,
    width            INTEGER,
    height           INTEGER,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
