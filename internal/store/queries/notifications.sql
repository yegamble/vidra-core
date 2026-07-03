-- name: CreateNotification :one
-- Record a notification for a recipient (user_id). Context columns are optional
-- and depend on the type.
INSERT INTO notifications (user_id, type, actor_id, channel_id, video_id, comment_id, conversation_id, report_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, user_id, type, actor_id, channel_id, video_id, comment_id, read_at, created_at, conversation_id, report_id;

-- name: ListNotifications :many
-- A user's notifications, newest first, joined with the actor's identity and the
-- context (channel handle/name for follows, video title for comments, report
-- status/target for report resolutions). The joined columns are nullable because
-- the context depends on the type. When unread_only is true, only unread
-- (read_at IS NULL) rows are returned.
SELECT n.id, n.type, n.actor_id, n.channel_id, n.video_id, n.comment_id,
       n.conversation_id, n.report_id, n.read_at, n.created_at,
       a.username AS actor_username, a.display_name AS actor_display_name,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       v.title AS video_title,
       r.status AS report_status, r.target_type AS report_target_type
FROM notifications n
LEFT JOIN users a ON a.id = n.actor_id
LEFT JOIN channels c ON c.id = n.channel_id
LEFT JOIN videos v ON v.id = n.video_id
LEFT JOIN reports r ON r.id = n.report_id
WHERE n.user_id = sqlc.arg('user_id')
  AND (NOT sqlc.arg('unread_only')::bool OR n.read_at IS NULL)
ORDER BY n.created_at DESC, n.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- name: MarkNotificationRead :execrows
-- Mark one of the user's notifications read (idempotent: already-read stays read).
-- Returns the number of rows matched so the caller can distinguish 404 (0 rows,
-- not found / not theirs) from success.
UPDATE notifications
SET read_at = COALESCE(read_at, now())
WHERE id = $1 AND user_id = $2;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;
