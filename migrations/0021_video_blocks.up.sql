-- 0021: Video blocks (moderation). A moderator/admin blocks a video, removing it
-- from all public surfaces (feed, search, channel listing, subscriptions, the
-- watch/detail endpoint, and public interactions). Unblocking restores it. One
-- row per blocked video; the reason and the acting moderator are recorded for the
-- audit trail.
CREATE TABLE video_blocks (
    video_id   UUID PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    reason     TEXT NOT NULL DEFAULT '',
    blocked_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
