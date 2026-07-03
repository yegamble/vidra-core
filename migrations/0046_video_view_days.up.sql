-- 0046: per-day view rollup (product-decisions.md §8, creator statistics).
-- One row per (video, UTC day), incremented alongside the existing deduped
-- video_view_counts write — a cheap upsert, no backfill (days before this
-- migration simply have no rows and render as zero). Feeds the owner-only
-- 30-day daily-views series on GET /videos/{id}/stats and
-- GET /channels/{handle}/stats.
CREATE TABLE video_view_days (
    video_id UUID   NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    day      DATE   NOT NULL,
    views    BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (video_id, day)
);
