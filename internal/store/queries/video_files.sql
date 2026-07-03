-- name: CreateVideoFile :one
INSERT INTO video_files (video_id, kind, storage_key, content_type, original_name, size_bytes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at;

-- name: ListVideoFiles :many
SELECT id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at
FROM video_files
WHERE video_id = $1
ORDER BY created_at;

-- name: GetVideoFileByKind :one
SELECT id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at
FROM video_files
WHERE video_id = $1 AND kind = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: DeleteVideoFilesByVideoAndKind :exec
DELETE FROM video_files WHERE video_id = $1 AND kind = $2;

-- name: SumUserStorageUsage :one
-- A user's storage usage: the total stored bytes of every video file
-- (originals, renditions, and thumbnails) across the videos owned via their
-- channels. Computed live — correctness over a denormalized counter.
SELECT COALESCE(SUM(vf.size_bytes), 0)::bigint AS used_bytes
FROM video_files vf
JOIN videos v ON v.id = vf.video_id
JOIN channels c ON c.id = v.channel_id
WHERE c.owner_id = $1;
