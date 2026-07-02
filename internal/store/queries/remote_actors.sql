-- Cached remote ActivityPub actors (migration 0036, .ralph/specs/federation.md §7).

-- name: GetRemoteActor :one
SELECT actor_url, actor_type, preferred_username, domain, inbox_url, shared_inbox_url,
       public_key_pem, followers_url, fetched_at, updated_at
FROM remote_actors
WHERE actor_url = $1;

-- name: UpsertRemoteActor :exec
INSERT INTO remote_actors (
    actor_url, actor_type, preferred_username, domain, inbox_url,
    shared_inbox_url, public_key_pem, followers_url
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (actor_url) DO UPDATE SET
    actor_type         = EXCLUDED.actor_type,
    preferred_username = EXCLUDED.preferred_username,
    domain             = EXCLUDED.domain,
    inbox_url          = EXCLUDED.inbox_url,
    shared_inbox_url   = EXCLUDED.shared_inbox_url,
    public_key_pem     = EXCLUDED.public_key_pem,
    followers_url      = EXCLUDED.followers_url,
    updated_at         = now();
