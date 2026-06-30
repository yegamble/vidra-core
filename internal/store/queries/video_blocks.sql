-- name: BlockVideo :execrows
-- Block a video (idempotent: re-blocking updates the reason + acting moderator).
-- A non-existent video raises a foreign-key violation (SQLSTATE 23503), which the
-- service maps to "video not found".
INSERT INTO video_blocks (video_id, reason, blocked_by)
VALUES ($1, $2, $3)
ON CONFLICT (video_id) DO UPDATE
    SET reason = EXCLUDED.reason, blocked_by = EXCLUDED.blocked_by, created_at = now();

-- name: UnblockVideo :execrows
-- Unblock a video (idempotent). Returns rows deleted (0 = it was not blocked).
DELETE FROM video_blocks WHERE video_id = $1;

-- name: IsVideoBlocked :one
-- Whether a video is currently blocked.
SELECT EXISTS (SELECT 1 FROM video_blocks WHERE video_id = $1) AS blocked;
