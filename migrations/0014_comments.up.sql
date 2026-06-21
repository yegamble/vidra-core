-- 0014: Video comments (flat). A comment is a user's text on a video. Threading
-- (parent_id) and moderation hooks land in later slices (PT-COMMENTS).
CREATE TABLE comments (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id    UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Listing a video's comments newest-first is the hot path.
CREATE INDEX comments_video_id_created_idx ON comments (video_id, created_at DESC);
