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

-- name: ListBlockedVideos :many
-- Currently-blocked videos for the moderation block-list: newest block first,
-- with the video title + current privacy/state, the owning channel, the block
-- reason, who blocked it (NULL if that moderator's account was deleted), and when.
SELECT
    b.video_id,
    v.title,
    v.privacy,
    v.state,
    c.handle       AS channel_handle,
    c.display_name AS channel_display_name,
    b.reason,
    u.username     AS blocked_by_username,
    b.created_at   AS blocked_at
FROM video_blocks b
JOIN videos v   ON v.id = b.video_id
JOIN channels c ON c.id = v.channel_id
LEFT JOIN users u ON u.id = b.blocked_by
ORDER BY b.created_at DESC, b.video_id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
