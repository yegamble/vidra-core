-- 0018: User notifications. A notification tells a recipient (user_id) that an
-- actor did something relevant to them — followed their channel, or commented on
-- their video. Context columns are nullable and depend on the type; read_at NULL
-- means unread. Rows are created as a side effect of the follow/comment flows.
CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    actor_id    UUID REFERENCES users (id) ON DELETE CASCADE,
    channel_id  UUID REFERENCES channels (id) ON DELETE CASCADE,
    video_id    UUID REFERENCES videos (id) ON DELETE CASCADE,
    comment_id  UUID REFERENCES comments (id) ON DELETE CASCADE,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Listing a user's notifications newest-first is the hot path.
CREATE INDEX notifications_user_created_idx ON notifications (user_id, created_at DESC);
-- Counting / filtering unread is cheap with a partial index.
CREATE INDEX notifications_user_unread_idx ON notifications (user_id) WHERE read_at IS NULL;
