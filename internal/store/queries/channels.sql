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
