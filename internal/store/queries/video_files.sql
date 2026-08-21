-- name: CreateVideoFile :one
-- sha256 is the lowercase hex digest of the bytes just stored, computed in the
-- same pass that uploaded them. Empty when the write path could not produce one;
-- the backfill worker picks those rows up (see migration 0106).
INSERT INTO video_files (video_id, kind, storage_key, content_type, original_name, size_bytes, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at, sha256;

-- name: ListVideoFiles :many
SELECT id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at, sha256
FROM video_files
WHERE video_id = $1
ORDER BY created_at;

-- name: GetVideoFileByKind :one
SELECT id, video_id, kind, storage_key, content_type, original_name, size_bytes, created_at, sha256
FROM video_files
WHERE video_id = $1 AND kind = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: ListUnhashedVideoFiles :many
-- The content-hash backfill's scan: the oldest rows still carrying the empty
-- (not-computed) state, in batches. Served by video_files_unhashed_idx. A plain
-- SELECT with no lease or claim is correct here because the worker driving it is
-- leader-gated, so exactly one instance is ever reading this.
SELECT id, storage_key
FROM video_files
WHERE sha256 = ''
ORDER BY created_at
LIMIT $1;

-- name: SetVideoFileSHA256 :exec
-- Records a computed digest (or the 'missing' sentinel). The still-empty guard
-- makes it a no-op against a row something else already hashed, so a backfill
-- racing a re-upload can never overwrite a fresher digest with a staler one.
UPDATE video_files SET sha256 = $2 WHERE id = $1 AND sha256 = '';

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

-- name: SumAllStorageUsage :one
-- Instance-wide media storage: the total stored bytes of every video file
-- (originals, renditions, thumbnails) across all accounts — the "media stored"
-- figure on the admin overview. The instance-wide counterpart of
-- SumUserStorageUsage; computed live over the same authoritative column.
SELECT COALESCE(SUM(size_bytes), 0)::bigint AS used_bytes
FROM video_files;
