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
-- Numbered 0167: 0164–0166 are claimed by PRs developed in parallel.
CREATE TABLE remote_actor_moves (
    origin_actor_url TEXT        PRIMARY KEY,
    target_actor_url TEXT        NOT NULL,
    claimed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
