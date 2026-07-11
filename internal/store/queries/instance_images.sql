-- name: ListInstanceImages :many
-- Every stored instance branding image, ordered by kind. The profileimage
-- service loads these at boot (and after each write) into its in-memory cache
-- so the hot GET /instance branding block never round-trips to the database.
SELECT kind, storage_key, content_type, size_bytes, created_at, updated_at
FROM instance_images
ORDER BY kind;

-- name: UpsertInstanceImage :one
INSERT INTO instance_images (kind, storage_key, content_type, size_bytes)
VALUES ($1, $2, $3, $4)
ON CONFLICT (kind) DO UPDATE
SET storage_key  = EXCLUDED.storage_key,
    content_type = EXCLUDED.content_type,
    size_bytes   = EXCLUDED.size_bytes,
    updated_at   = now()
RETURNING kind, storage_key, content_type, size_bytes, created_at, updated_at;

-- name: GetInstanceImage :one
SELECT kind, storage_key, content_type, size_bytes, created_at, updated_at
FROM instance_images
WHERE kind = $1;

-- name: DeleteInstanceImage :execrows
DELETE FROM instance_images WHERE kind = $1;
