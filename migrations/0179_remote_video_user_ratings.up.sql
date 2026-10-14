-- 0179: A local user's Like/Dislike of a REMOTE video. The vote is federated
-- to the video's origin (which owns the count, PeerTube parity) and kept here so
-- the user sees their own rating and an Undo can be sent when they change or
-- clear it. One row per (user, remote video).
CREATE TABLE remote_video_user_ratings (
    remote_video_id UUID        NOT NULL REFERENCES remote_videos (id) ON DELETE CASCADE,
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rating          TEXT        NOT NULL CHECK (rating IN ('like', 'dislike')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, remote_video_id)
);
