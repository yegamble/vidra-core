-- 0037: Inbound federation — remote follows of local channels, and an inbox
-- dedup log. A remote actor cannot be a local user, so remote follows live in
-- their own table (not channel_follows, whose follower_id references users).
-- Channels are open, so an inbound Follow is auto-accepted locally (state
-- 'accepted'); actually telling the remote (sending Accept) is the delivery slice.
-- See .ralph/specs/federation.md §6,8.
CREATE TABLE remote_follows (
    channel_id          UUID        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    remote_actor_url    TEXT        NOT NULL,
    state               TEXT        NOT NULL DEFAULT 'accepted',
    follow_activity_url TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, remote_actor_url)
);
CREATE INDEX remote_follows_channel_idx ON remote_follows (channel_id);

-- Idempotency log: an inbound activity id is processed at most once.
CREATE TABLE federation_inbox_activities (
    activity_id TEXT        PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
