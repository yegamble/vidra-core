-- 0178: Ratings cast on LOCAL videos by REMOTE actors (ActivityPub Like /
-- Dislike, PeerTube parity). video_ratings keys on a local user, so a fediverse
-- account's vote needs its own row: one per (remote actor, video), changed in
-- place by a later Like/Dislike and removed by the matching Undo. Counts are
-- always derived from these rows -- a remote-supplied total is never stored.
--
-- The actor FK cascades: a remote actor's Delete (or the purge of its cache
-- row) takes its votes with it, as PeerTube's account cascade does.
CREATE TABLE remote_video_ratings (
    video_id         UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    remote_actor_url TEXT        NOT NULL REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    rating           TEXT        NOT NULL CHECK (rating IN ('like', 'dislike')),
    -- The Like/Dislike activity id, so an Undo that names it by bare id (no
    -- embedded object) still finds the row.
    activity_url     TEXT        NOT NULL CHECK (char_length(activity_url) BETWEEN 1 AND 2048),
    -- The signer's host, as the inbox's instance-block gate reads it: the key
    -- for the per-domain cap on new votes and for the blocked-instance filter
    -- on the count.
    domain           TEXT        NOT NULL CHECK (char_length(domain) BETWEEN 1 AND 255),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (remote_actor_url, video_id)
);

CREATE INDEX remote_video_ratings_video_id_idx ON remote_video_ratings (video_id);
-- The per-domain cap counts a domain's recent new votes.
CREATE INDEX remote_video_ratings_domain_created_idx ON remote_video_ratings (domain, created_at);
