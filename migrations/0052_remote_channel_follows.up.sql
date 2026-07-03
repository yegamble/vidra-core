-- 0052: Outbound remote-channel follows + account-actor delivery signing
-- (.ralph/specs/federation-remote-content.md §3).
--
-- remote_channel_follows: a LOCAL user following a REMOTE channel (PeerTube
-- subscription model). Kept separate from channel_follows (whose channel_id
-- references local channels) — same side-table rationale as Slice 2a. The row
-- starts 'pending' when the signed Follow is enqueued; an inbound
-- Accept{Follow} matching follow_activity_url (and signed by the followed
-- actor) flips it to 'accepted'; a Reject — or a local unfollow — deletes it.
-- `id` is a surrogate unique key for the REST surface
-- (DELETE /api/v1/me/remote-follows/{id}); the natural PK stays
-- (user_id, remote_actor_url) per the spec.
CREATE TABLE remote_channel_follows (
    id                  UUID        NOT NULL UNIQUE DEFAULT uuid_generate_v4(),
    user_id             UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    remote_actor_url    TEXT        NOT NULL REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    state               TEXT        NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'accepted')),
    follow_activity_url TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, remote_actor_url)
);

-- Inbound Accept/Reject match by our Follow activity id.
CREATE INDEX remote_channel_follows_activity_idx ON remote_channel_follows (follow_activity_url);
-- The ingestion gate asks "does anyone here follow this actor (accepted)?".
CREATE INDEX remote_channel_follows_actor_idx ON remote_channel_follows (remote_actor_url) WHERE state = 'accepted';

-- The outbound delivery queue can now sign as a local ACCOUNT actor too (the
-- user's Follow/Undo are attributed to their Person actor, not a channel).
-- Exactly one signer is set per row: channel columns (existing) or these.
ALTER TABLE federation_deliveries
    ADD COLUMN signing_user_id  UUID REFERENCES users (id) ON DELETE CASCADE,
    ADD COLUMN signing_username TEXT NOT NULL DEFAULT '';
