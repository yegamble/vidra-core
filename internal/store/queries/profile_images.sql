-- name: UpsertUserImage :one
INSERT INTO user_images (user_id, kind, storage_key, content_type, size_bytes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, kind) DO UPDATE
SET storage_key  = EXCLUDED.storage_key,
    content_type = EXCLUDED.content_type,
    size_bytes   = EXCLUDED.size_bytes,
    updated_at   = now()
RETURNING user_id, kind, storage_key, content_type, size_bytes, created_at, updated_at;

-- name: GetUserImage :one
SELECT user_id, kind, storage_key, content_type, size_bytes, created_at, updated_at
FROM user_images
WHERE user_id = $1 AND kind = $2;

-- name: DeleteUserImage :execrows
DELETE FROM user_images WHERE user_id = $1 AND kind = $2;

-- name: UpsertChannelImage :one
INSERT INTO channel_images (channel_id, kind, storage_key, content_type, size_bytes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (channel_id, kind) DO UPDATE
SET storage_key  = EXCLUDED.storage_key,
    content_type = EXCLUDED.content_type,
    size_bytes   = EXCLUDED.size_bytes,
    updated_at   = now()
RETURNING channel_id, kind, storage_key, content_type, size_bytes, created_at, updated_at;

-- name: GetChannelImage :one
SELECT channel_id, kind, storage_key, content_type, size_bytes, created_at, updated_at
FROM channel_images
WHERE channel_id = $1 AND kind = $2;

-- name: DeleteChannelImage :execrows
DELETE FROM channel_images WHERE channel_id = $1 AND kind = $2;
