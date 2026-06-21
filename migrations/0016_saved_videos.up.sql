-- 0016: Saved videos ("watch later" / private library). One row per (user, video);
-- a user saves a video at most once. The list is a personal library, newest-saved
-- first. Named playlists with ordering are a later slice.
CREATE TABLE saved_videos (
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_id   UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);

-- Listing a user's saved videos newest-first is the hot path.
CREATE INDEX saved_videos_user_created_idx ON saved_videos (user_id, created_at DESC);
