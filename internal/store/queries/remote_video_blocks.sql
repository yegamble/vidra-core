-- Remote-video blocks (migration 0054, remote-content §8): the admin per-video
-- hide for federated content, mirroring video_blocks.

-- name: BlockRemoteVideo :execrows
-- Block a remote video (idempotent: re-blocking updates the reason + acting
-- moderator). A non-existent remote video raises a foreign-key violation
-- (SQLSTATE 23503), which the service maps to "remote video not found".
INSERT INTO remote_video_blocks (remote_video_id, reason, blocked_by)
VALUES ($1, $2, $3)
ON CONFLICT (remote_video_id) DO UPDATE
    SET reason = EXCLUDED.reason, blocked_by = EXCLUDED.blocked_by, created_at = now();

-- name: UnblockRemoteVideo :execrows
-- Unblock a remote video (idempotent). Returns rows deleted (0 = not blocked).
DELETE FROM remote_video_blocks WHERE remote_video_id = $1;

-- name: ListBlockedRemoteVideos :many
-- Currently-blocked remote videos for the moderation block-list: newest block
-- first, with the video title, origin identity, the block reason, who blocked
-- it (NULL if that moderator's account was deleted), and when.
SELECT
    b.remote_video_id,
    rv.title,
    rv.object_url,
    rv.watch_url,
    (ra.preferred_username || '@' || ra.domain)::text AS channel_handle,
    ra.domain,
    b.reason,
    u.username   AS blocked_by_username,
    b.created_at AS blocked_at
FROM remote_video_blocks b
JOIN remote_videos rv ON rv.id = b.remote_video_id
JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
LEFT JOIN users u ON u.id = b.blocked_by
ORDER BY b.created_at DESC, b.remote_video_id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountBlockedRemoteVideos :one
-- How many rows ListBlockedRemoteVideos would return, ignoring pagination. The
-- inner JOINs are part of the predicate and must stay identical.
SELECT count(*)::bigint
FROM remote_video_blocks b
JOIN remote_videos rv ON rv.id = b.remote_video_id
JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url;
