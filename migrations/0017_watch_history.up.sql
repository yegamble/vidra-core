-- 0017: Watch history with resume position. One row per (user, video): the
-- viewer's most recent watch of a video and where they left off, so the UI can
-- list history newest-watched-first and resume playback. updated_at is bumped on
-- every progress report; created_at records the first watch.
CREATE TABLE watch_history (
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_id         UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    position_seconds INTEGER NOT NULL DEFAULT 0 CHECK (position_seconds >= 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);

-- Listing a user's history most-recently-watched first is the hot path.
CREATE INDEX watch_history_user_updated_idx ON watch_history (user_id, updated_at DESC);
