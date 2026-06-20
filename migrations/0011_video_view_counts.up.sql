-- 0011: video_view_counts. Aggregate view tally per video, in a 1:1 side table
-- so the core videos row and its queries stay untouched (mirrors video_metadata).
-- Recording is deduped per viewer per window in Redis before this is bumped.

CREATE TABLE video_view_counts (
    video_id   UUID PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    views      BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
