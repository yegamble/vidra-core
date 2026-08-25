-- name: CreateChannel :one
INSERT INTO channels (owner_id, handle, display_name, description)
VALUES ($1, $2, $3, $4)
RETURNING id, owner_id, handle, display_name, description, created_at, updated_at, activitypub_enabled, atproto_enabled;

-- name: GetChannelByID :one
SELECT id, owner_id, handle, display_name, description, created_at, updated_at, activitypub_enabled, atproto_enabled
FROM channels
WHERE id = $1;

-- name: GetChannelByHandle :one
SELECT id, owner_id, handle, display_name, description, created_at, updated_at, activitypub_enabled, atproto_enabled
FROM channels
WHERE lower(handle) = lower($1);

-- name: ListChannelsByOwner :many
SELECT id, owner_id, handle, display_name, description, created_at, updated_at, activitypub_enabled, atproto_enabled
FROM channels
WHERE owner_id = $1
ORDER BY created_at;

-- name: CountChannelsByOwner :one
SELECT count(*) FROM channels WHERE owner_id = $1;

-- name: ListManagedChannels :many
-- GET /me/channels: the channels a user OWNS plus the ones shared with them as
-- an editor member (migration 0097), each tagged with the caller's role, owned
-- first. This replaces a Go-side merge of ListChannelsByOwner and
-- ListChannelsForMember, which could not be paginated and which then issued one
-- CountChannelFollowers per row — an N+1 that grew with the user's channel
-- count. The follower count comes back inline as one scalar subquery per row.
SELECT managed.*,
       (SELECT count(*) FROM channel_follows cf WHERE cf.channel_id = managed.id)::bigint AS follower_count
FROM (
    SELECT c.id, c.owner_id, c.handle, c.display_name, c.description,
           c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
           'owner'::text AS role
    FROM channels c
    WHERE c.owner_id = sqlc.arg('user_id')

    UNION ALL

    SELECT c.id, c.owner_id, c.handle, c.display_name, c.description,
           c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
           cm.role
    FROM channel_members cm
    JOIN channels c ON c.id = cm.channel_id
    WHERE cm.user_id = sqlc.arg('user_id')
) managed
ORDER BY (managed.role = 'owner') DESC, managed.created_at, managed.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountManagedChannels :one
-- How many rows ListManagedChannels would return, ignoring pagination.
SELECT count(*)::bigint
FROM (
    SELECT c.id, c.owner_id, c.handle, c.display_name, c.description,
           c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
           'owner'::text AS role
    FROM channels c
    WHERE c.owner_id = sqlc.arg('user_id')

    UNION ALL

    SELECT c.id, c.owner_id, c.handle, c.display_name, c.description,
           c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
           cm.role
    FROM channel_members cm
    JOIN channels c ON c.id = cm.channel_id
    WHERE cm.user_id = sqlc.arg('user_id')
) managed;

-- name: UpdateChannel :one
UPDATE channels
SET display_name        = COALESCE(sqlc.narg('display_name'), display_name),
    description         = COALESCE(sqlc.narg('description'), description),
    activitypub_enabled = COALESCE(sqlc.narg('activitypub_enabled'), activitypub_enabled),
    atproto_enabled     = COALESCE(sqlc.narg('atproto_enabled'), atproto_enabled),
    updated_at          = now()
WHERE id = sqlc.arg('id')
RETURNING id, owner_id, handle, display_name, description, created_at, updated_at, activitypub_enabled, atproto_enabled;

-- name: DeleteChannel :exec
DELETE FROM channels WHERE id = $1;

-- name: SearchPublicChannels :many
-- Channel search (GET /api/v1/search/channels), backed by core's own Postgres
-- rather than vidra-search. The search service indexes VIDEOS; a channel exists
-- there only as denormalised columns on video rows, so a channel that has not
-- published anything is invisible to it. That blind spot is exactly the case a
-- channel search has to get right, which is why this is a local trigram query.
--
-- Matching is an unanchored, case-insensitive substring over the handle and the
-- display name; 0003 indexes the handle for trigrams and 0118 the display name,
-- so the OR is a BitmapOr of two index scans rather than a seq scan.
--
-- Visibility. A channel is a public publishing identity, so there is no
-- per-channel privacy flag to honour — the gate is on its OWNER:
--   * is_active — a deactivated or hard-deleted account (both set is_active
--     FALSE) takes its channels out of discovery with it.
--   * NOT unlisted — the account-level discovery opt-out (§16). Every other
--     discovery surface already excludes unlisted owners (see
--     SearchPublicVideos and ListPublicVideosSorted); a channel search is a
--     discovery surface, so it does too. Direct /channels/{handle} URLs keep
--     serving, which is the whole point of the flag.
-- profile_public is deliberately NOT consulted here: it governs whether the
-- ACCOUNT page exists, not whether the account's channels do.
--
-- The viewer's own mutes and blocks are applied the same one-directional way as
-- every other list (their block hides the other party from them; it does not
-- hide them from the other party).
SELECT c.id, c.owner_id, c.handle, c.display_name, c.description,
       c.created_at, c.updated_at, c.activitypub_enabled, c.atproto_enabled,
       (SELECT count(*) FROM channel_follows cf WHERE cf.channel_id = c.id)::bigint AS follower_count
FROM channels c
JOIN users u ON u.id = c.owner_id
WHERE u.is_active = TRUE
  AND NOT u.unlisted
  AND (c.handle ILIKE '%' || sqlc.arg('query')::text || '%'
       OR c.display_name ILIKE '%' || sqlc.arg('query')::text || '%')
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
  )
ORDER BY greatest(similarity(c.handle, sqlc.arg('query')),
                  similarity(c.display_name, sqlc.arg('query'))) DESC,
         follower_count DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountSearchPublicChannels :one
-- How many rows SearchPublicChannels would return for the same query and
-- viewer, ignoring pagination. Every predicate is repeated VERBATIM — the count
-- is per-viewer, so counting raw channels would promise a muted viewer more
-- pages than they can ever reach. Only the ORDER BY is dropped; ordering cannot
-- change a count.
SELECT count(*)::bigint
FROM channels c
JOIN users u ON u.id = c.owner_id
WHERE u.is_active = TRUE
  AND NOT u.unlisted
  AND (c.handle ILIKE '%' || sqlc.arg('query')::text || '%'
       OR c.display_name ILIKE '%' || sqlc.arg('query')::text || '%')
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.owner_id
  );
