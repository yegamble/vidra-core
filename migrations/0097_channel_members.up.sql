-- 0097: Channel collaborators. A channel owner may invite other local users as
-- EDITORS: they manage the channel's content (upload/import videos, edit/delete/
-- replace videos + thumbnails/captions/chapters, run live streams, view channel
-- stats) but never the channel itself (no PATCH/DELETE, avatar/banner, member
-- management, protocol flags, or sync — those stay owner-only). PeerTube shipped
-- channel collaborators late (v8.0); we model them first-class.

CREATE TABLE channel_members (
    channel_id UUID NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'editor',
    invited_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, user_id)
);

-- Reverse lookup: "which channels is this user a member of" (GET /me/channels
-- "shared with you", and the content-management authorization check).
CREATE INDEX channel_members_user_id_idx ON channel_members (user_id);
