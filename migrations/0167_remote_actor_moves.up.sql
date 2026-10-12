-- 0167: inbound ActivityPub Move — one remote actor announcing it now lives at
-- another (Mastodon's account migration; ActivityPub `Move{object: old,
-- target: new}`). Local users following the old actor are moved to the new one.
--
-- One row per ORIGIN actor: the last Move this instance acted on and when. It
-- is the rate limit, not a history. A Move costs this instance an outbound
-- fetch of the target (SSRF-guarded) and a signed Follow + Undo per local
-- follower, so an origin may claim one at most once per cooldown window; the
-- claim is an atomic INSERT … ON CONFLICT DO UPDATE … WHERE (see
-- ClaimRemoteActorMove), which holds across restarts and API replicas where an
-- in-memory limiter would not.
--
-- No foreign key to remote_actors: the origin may be evicted from the actor
-- cache (an inbound Delete), and that must not reset its cooldown.
--
-- It is also the work queue for the move. state walks claimed → (verification
-- fails: the row is DELETED, releasing the claim, as Mastodon does) → moving →
-- done. A 'moving' row is drained by the federation worker in bounded batches,
-- round-robin by touched_at, so one large move cannot starve another.
--
-- Numbered 0167: 0164–0166 are claimed by PRs developed in parallel.
CREATE TABLE remote_actor_moves (
    origin_actor_url TEXT        PRIMARY KEY,
    target_actor_url TEXT        NOT NULL,
    claimed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    state            TEXT        NOT NULL DEFAULT 'claimed'
                                 CHECK (state IN ('claimed', 'moving', 'done')),
    touched_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX remote_actor_moves_moving_idx ON remote_actor_moves (touched_at) WHERE state = 'moving';
