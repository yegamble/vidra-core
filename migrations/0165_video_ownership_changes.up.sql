-- 0165: video ownership changes (PeerTube parity: "give ownership" / "My
-- ownership changes").
--
-- A video's owner is derived from its channel (videos.channel_id ->
-- channels.owner_id), so handing a video to another local user is a channel
-- move into one of THEIR channels — which only they can choose. This table is
-- the request between the two steps: the owner names a recipient, the
-- recipient accepts into one of their channels or refuses.
--
--   * initiator_id is the owner at the time of the request. Accept re-checks,
--     in the same statement that moves the video, that the initiator STILL owns
--     it, so a request cannot outlive the ownership it was made under.
--   * every foreign key cascades: a request about a video or an account that no
--     longer exists has nothing left to transfer.
--   * one PENDING request per video is a database guarantee (partial unique
--     index), so two concurrent gives cannot both land. Answered requests stay
--     as history and do not block a new one. A pending request that went stale
--     (its giver no longer owns the video, or it outlived the offer window) is
--     marked 'expired' by the next give, so it never blocks the video's new
--     owner; the giver may also withdraw it ('cancelled').
--   * video_title is the title AT GIVE TIME. The recipient's list shows it for
--     every status, so what they see never follows the video after it has left
--     the request (a later owner's renames, or a private video's title).
--
-- notifications.type is unconstrained TEXT, so the new
-- 'video_ownership_request' type needs no schema change (see 0156/0158).
CREATE TABLE video_ownership_changes (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id      UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    initiator_id  UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    next_owner_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video_title   TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'accepted', 'refused', 'cancelled', 'expired')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (initiator_id <> next_owner_id)
);

CREATE UNIQUE INDEX video_ownership_changes_one_pending_idx
    ON video_ownership_changes (video_id)
    WHERE status = 'pending';

-- A recipient's requests, newest first (GET /me/video-ownership-changes).
CREATE INDEX video_ownership_changes_next_owner_idx
    ON video_ownership_changes (next_owner_id, created_at DESC, id DESC);

-- The re-offer cooldown: did an offer from this giver to this recipient end
-- recently, refused by the recipient or withdrawn by the giver?
CREATE INDEX video_ownership_changes_ended_pair_idx
    ON video_ownership_changes (initiator_id, next_owner_id, updated_at DESC)
    WHERE status IN ('refused', 'cancelled');
