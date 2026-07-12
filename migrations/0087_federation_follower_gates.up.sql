-- 0087: Federation policy gates (config-parity W12).
--
-- (a) remote_follows gains a surrogate id so the admin follower-approval queue
--     (federation_follower_approval) can address one pending follow — the
--     natural key is the composite (channel_id, remote_actor_url) — plus a
--     partial index for the pending-queue listing. Existing rows keep their
--     'accepted' state; the gate is never retroactive.
--
-- (b) channel_follow_backs records instance-initiated follow-backs of channel
--     followers (federation_auto_follow_back). Vidra has NO instance-level AP
--     actor (parity ledger §11), so the follow-back is signed by the FOLLOWED
--     CHANNEL's actor — the only local actor with a relationship to the
--     follower. One follow-back per remote actor instance-wide (enforced at
--     insert time), state pending until the remote's Accept arrives.
ALTER TABLE remote_follows ADD COLUMN id UUID NOT NULL DEFAULT uuid_generate_v4();
CREATE UNIQUE INDEX remote_follows_id_key ON remote_follows (id);
CREATE INDEX remote_follows_pending_idx ON remote_follows (created_at DESC) WHERE state = 'pending';

CREATE TABLE channel_follow_backs (
    channel_id          UUID        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    remote_actor_url    TEXT        NOT NULL,
    follow_activity_url TEXT        NOT NULL,
    state               TEXT        NOT NULL DEFAULT 'pending',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, remote_actor_url)
);
CREATE INDEX channel_follow_backs_actor_idx ON channel_follow_backs (remote_actor_url);
