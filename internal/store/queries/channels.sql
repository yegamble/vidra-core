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
