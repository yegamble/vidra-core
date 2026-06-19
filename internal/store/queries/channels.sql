-- name: CreateChannel :one
INSERT INTO channels (owner_id, handle, display_name, description)
VALUES ($1, $2, $3, $4)
RETURNING id, owner_id, handle, display_name, description, created_at, updated_at;

-- name: GetChannelByID :one
SELECT id, owner_id, handle, display_name, description, created_at, updated_at
FROM channels
WHERE id = $1;

-- name: GetChannelByHandle :one
SELECT id, owner_id, handle, display_name, description, created_at, updated_at
FROM channels
WHERE lower(handle) = lower($1);

-- name: ListChannelsByOwner :many
SELECT id, owner_id, handle, display_name, description, created_at, updated_at
FROM channels
WHERE owner_id = $1
ORDER BY created_at;

-- name: CountChannelsByOwner :one
SELECT count(*) FROM channels WHERE owner_id = $1;
