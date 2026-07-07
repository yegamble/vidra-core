-- name: ListVideoPasswords :many
-- A video's passwords for the owner listing: id + created_at only, NEVER the
-- hash. Oldest first (stable display order).
SELECT id, created_at FROM video_passwords WHERE video_id = $1 ORDER BY created_at, id;

-- name: ListVideoPasswordHashes :many
-- The bcrypt hashes for a video, used by the unlock endpoint to verify a
-- submitted plaintext. Never leaves the service layer.
SELECT password_hash FROM video_passwords WHERE video_id = $1;

-- name: CountVideoPasswords :one
-- How many passwords a video has (drives the "privacy=password needs >=1" rule
-- and the last-password 409 guard).
SELECT count(*) FROM video_passwords WHERE video_id = $1;

-- name: InsertVideoPassword :one
-- Stores one bcrypt hash and returns the new row's public fields (id, created_at).
INSERT INTO video_passwords (video_id, password_hash)
VALUES ($1, $2)
RETURNING id, created_at;

-- name: VideoPasswordExists :one
-- Whether a given password id belongs to a video (existence check before delete,
-- so an unknown id is 404 and a foreign id cannot be deleted cross-video).
SELECT EXISTS (SELECT 1 FROM video_passwords WHERE id = sqlc.arg('id') AND video_id = sqlc.arg('video_id'));

-- name: DeleteVideoPassword :execrows
-- Deletes one password by id, scoped to its video. Returns the affected-row count
-- so the caller can distinguish a real delete (1) from an unknown id (0 -> 404).
DELETE FROM video_passwords WHERE id = sqlc.arg('id') AND video_id = sqlc.arg('video_id');

-- name: DeleteVideoPasswords :exec
-- Clears a video's whole password set (the first half of a replace-all).
DELETE FROM video_passwords WHERE video_id = $1;

-- name: InsertVideoPasswords :exec
-- Bulk-inserts a video's password hashes from a parallel array (the second half
-- of a replace-all; the service validates count/length and hashes each first).
INSERT INTO video_passwords (video_id, password_hash)
SELECT sqlc.arg('video_id'), unnest(sqlc.arg('password_hash')::text[]);

-- name: GetVideoEmbedPrivacy :one
-- A video's embed-privacy policy (status + allow-listed hostnames).
SELECT embed_privacy, embed_allowed_domains FROM videos WHERE id = $1;

-- name: SetVideoEmbedPrivacy :exec
-- Replaces a video's embed-privacy policy (owner-only, validated in the service).
UPDATE videos SET embed_privacy = sqlc.arg('embed_privacy'),
                  embed_allowed_domains = sqlc.arg('embed_allowed_domains')::text[]
WHERE id = sqlc.arg('id');
