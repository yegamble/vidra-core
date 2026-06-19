-- 0005: channel follows. A follow links a follower (user) to a channel. The
-- composite primary key makes a follow idempotent (one row per user+channel);
-- the channel_id index supports follower-count and follower-list lookups.

CREATE TABLE channel_follows (
    follower_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel_id   UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, channel_id)
);

CREATE INDEX channel_follows_channel_id_idx ON channel_follows (channel_id);
