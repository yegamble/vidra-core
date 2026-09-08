-- One handle namespace for accounts and channels, and the aliases a rename
-- leaves behind (migration 0142, A29 parity).

-- name: GetChannelHandleAlias :one
-- Resolve a handle that is no longer a channel's own handle. Two callers with
-- two different expiry rules, which is why the row is returned rather than a
-- boolean: the HUMAN redirect (`/channels/<old>` → 301) honours expires_at,
-- while the ActivityPub actor id (is_actor_id) never expires — a federated id is
-- a promise to other servers.
SELECT a.handle_lower, a.channel_id, a.is_actor_id, a.expires_at,
       c.handle AS current_handle
FROM channel_handle_aliases a
JOIN channels c ON c.id = a.channel_id
WHERE a.handle_lower = lower($1);

-- name: GetChannelActorAlias :one
-- The frozen federated identity of one channel, or no row when it was never
-- renamed (the overwhelmingly common case, where the actor id derives from the
-- live handle exactly as it always did).
SELECT handle_lower FROM channel_handle_aliases
WHERE channel_id = $1 AND is_actor_id;

-- name: GetAccountHandleReservation :one
-- Does this handle belong to an ACCOUNT? The WebFinger both-links answer needs
-- to know which kinds hold the name, and the conflict message needs to say which
-- kind refused.
SELECT handle_lower, user_id, channel_id FROM actor_handles
WHERE handle_lower = lower($1);

-- name: BlockRemoteActorInstanceWide :exec
-- The ADMIN half of the per-remote-account block (0142). An instance block is a
-- sledgehammer in the other direction: an admin who wants ONE remote person gone
-- for everyone had to defederate that person's entire server. Idempotent; a
-- re-block keeps the first reason unless a new one is given.
INSERT INTO blocked_remote_actors (remote_actor_url, blocked_by, reason)
VALUES ($1, sqlc.narg('blocked_by'), sqlc.arg('reason'))
ON CONFLICT (remote_actor_url) DO UPDATE SET
    reason = CASE WHEN EXCLUDED.reason <> '' THEN EXCLUDED.reason
                  ELSE blocked_remote_actors.reason END;

-- name: UnblockRemoteActorInstanceWide :execrows
DELETE FROM blocked_remote_actors WHERE remote_actor_url = $1;

-- name: ListBlockedRemoteActors :many
-- The admin surface, newest first. An actor this instance has never cached still
-- lists — showing the URL rather than a handle — because the alternative is a
-- block the admin cannot see or lift.
SELECT b.remote_actor_url, b.reason, b.created_at,
       COALESCE(ra.preferred_username, '')::text AS preferred_username,
       COALESCE(ra.domain, '')::text AS domain
FROM blocked_remote_actors b
LEFT JOIN remote_actors ra ON ra.actor_url = b.remote_actor_url
ORDER BY b.created_at DESC, b.remote_actor_url
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountBlockedRemoteActors :one
SELECT count(*)::bigint FROM blocked_remote_actors;

-- name: IsRemoteActorBlockedInstanceWide :one
-- Reaches the account's channels through the same view every read filter uses,
-- so an admin block of an account refuses inbound activity from its Groups too.
SELECT EXISTS (
    SELECT 1 FROM remote_actor_block_reach
    WHERE blocker_id IS NULL AND actor_url = $1
);
