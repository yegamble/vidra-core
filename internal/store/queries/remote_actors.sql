-- Cached remote ActivityPub actors (migration 0036, .ralph/specs/federation.md §7).

-- name: GetRemoteActor :one
SELECT actor_url, actor_type, preferred_username, domain, inbox_url, shared_inbox_url,
       public_key_pem, followers_url, fetched_at, updated_at, attributed_to
FROM remote_actors
WHERE actor_url = $1;

-- name: UpsertRemoteActor :exec
-- attributed_to is the OWNING ACCOUNT of a Group actor, read off the actor
-- document's attributedTo (0142). It is what makes a block of an account reach
-- the channels that account owns: the rehearsal measured a viewer blocking
-- @name@domain and seeing nothing change, because the videos are attributed to
-- the Group and the block named the Person. It is '' for a Person and for any
-- Group whose document does not name an owner.
INSERT INTO remote_actors (
    actor_url, actor_type, preferred_username, domain, inbox_url,
    shared_inbox_url, public_key_pem, followers_url, attributed_to
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, sqlc.arg('attributed_to'))
ON CONFLICT (actor_url) DO UPDATE SET
    actor_type         = EXCLUDED.actor_type,
    preferred_username = EXCLUDED.preferred_username,
    domain             = EXCLUDED.domain,
    inbox_url          = EXCLUDED.inbox_url,
    shared_inbox_url   = EXCLUDED.shared_inbox_url,
    public_key_pem     = EXCLUDED.public_key_pem,
    followers_url      = EXCLUDED.followers_url,
    attributed_to      = EXCLUDED.attributed_to,
    updated_at         = now();

-- name: CountFederatedPeers :one
-- Distinct remote instances we have cached actors from — the "federated peers"
-- figure on the admin overview. One count per remote domain we have ever
-- fetched or interacted with (independent of block state; a blocked instance is
-- still a peer we know about).
SELECT count(DISTINCT domain) FROM remote_actors;

-- name: DeleteRemoteActor :execrows
-- Inbound actor Delete (remote-content §7): drop the cached actor; its remote
-- videos, remote-authored comments, and remote-channel follow edges cascade
-- away via their remote_actor_url foreign keys.
DELETE FROM remote_actors WHERE actor_url = $1;
