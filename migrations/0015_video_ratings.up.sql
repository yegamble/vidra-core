-- 0015: Video ratings (like / dislike). One row per (user, video); a user has at
-- most one rating per video, which they can change or clear. Counts are derived.
CREATE TABLE video_ratings (
    video_id   UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rating     TEXT NOT NULL CHECK (rating IN ('like', 'dislike')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);

-- Counting a video's likes/dislikes is the hot path.
CREATE INDEX video_ratings_video_id_idx ON video_ratings (video_id);
