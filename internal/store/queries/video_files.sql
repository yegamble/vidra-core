-- name: CreateVideoFile :one
INSERT INTO video_files (video_id, kind, storage_key, content_type, original_name, size_bytes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at;

-- name: ListVideoFiles :many
SELECT id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at
FROM video_files
WHERE video_id = $1
ORDER BY created_at;

-- name: DeleteVideoFilesByVideoAndKind :exec
DELETE FROM video_files WHERE video_id = $1 AND kind = $2;
