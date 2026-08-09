-- name: CreateNotification :one
-- Record a notification for a recipient (user_id). Context columns are optional
-- and depend on the type.
INSERT INTO notifications (user_id, type, actor_id, channel_id, video_id, comment_id, conversation_id, report_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, user_id, type, actor_id, channel_id, video_id, comment_id, read_at, created_at, conversation_id, report_id;

-- name: NotifyFollowersOfNewVideo :execrows
-- The "new video from a channel you follow" fan-out (migration 0101), fired by
-- the publish transition. ONE set-based statement rather than a row-per-follower
-- loop: a channel's whole follower set is notified in a single round trip, and
-- every exclusion below is a join condition instead of an N+1 lookup.
--
-- Deliberately notified only when ALL of these hold:
--   * the video is genuinely live and PUBLIC — an unlisted / private /
--     password-protected video must never be announced to a follower list, and a
--     video still in draft/processing/transcoding/scheduled/quarantined/failed
--     state is not published yet;
--   * the video is not under a moderation block;
--   * the follower's bell for this channel is 'all';
--   * the follower has not turned the 'new_video' type off globally (ABSENCE of
--     a notification_prefs row = enabled, matching the rest of the prefs model);
--   * the follower has not muted the channel owner, and neither side has blocked
--     the other;
--   * the follower is not the channel owner (no self-notification).
--
-- ON CONFLICT rides notifications_new_video_unique_idx, so a publish hook that
-- fires twice for the same video is a no-op the second time.
INSERT INTO notifications (user_id, type, actor_id, channel_id, video_id)
SELECT cf.follower_id, 'new_video', ch.owner_id, v.channel_id, v.id
FROM videos v
JOIN channels ch ON ch.id = v.channel_id
JOIN channel_follows cf ON cf.channel_id = v.channel_id
WHERE v.id = sqlc.arg('video_id')
  AND v.state = 'published'
  AND v.privacy = 'public'
  AND cf.notification_setting = 'all'
  AND cf.follower_id <> ch.owner_id
  AND NOT EXISTS (
      SELECT 1 FROM video_blocks vb WHERE vb.video_id = v.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM notification_prefs np
      WHERE np.user_id = cf.follower_id AND np.type = 'new_video' AND np.enabled = FALSE
  )
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts ma
      WHERE ma.muter_id = cf.follower_id AND ma.muted_id = ch.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE (ub.blocker_id = cf.follower_id AND ub.blocked_id = ch.owner_id)
         OR (ub.blocker_id = ch.owner_id AND ub.blocked_id = cf.follower_id)
  )
ON CONFLICT (user_id, video_id) WHERE type = 'new_video' DO NOTHING;

-- name: NotifyStaffOfNewReport :execrows
-- The "new abuse report" staff fan-out (migration 0103), fired when a report is
-- filed. ONE set-based statement, mirroring NotifyFollowersOfNewVideo: every
-- active admin and moderator is notified in a single round trip. The reporter
-- is stored as the actor — staff see the reporter's identity in the moderation
-- queue anyway, and it lets the notification read "bob reported a video".
--
-- Deliberately notified only when ALL of these hold:
--   * the recipient is an active, non-deleted admin or moderator;
--   * the recipient is not the reporter (a staff member filing a report must
--     not be told about their own filing);
--   * the recipient has not turned the 'new_report' type off globally (ABSENCE
--     of a notification_prefs row = enabled, matching the rest of the prefs
--     model).
--
-- ON CONFLICT rides notifications_new_report_unique_idx, so a fan-out that
-- fires twice for the same report is a no-op the second time.
INSERT INTO notifications (user_id, type, actor_id, report_id)
SELECT u.id, 'new_report', r.reporter_id, r.id
FROM reports r
JOIN users u ON u.role IN ('admin', 'moderator')
WHERE r.id = sqlc.arg('report_id')
  AND u.id <> r.reporter_id
  AND u.is_active
  AND u.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM notification_prefs np
      WHERE np.user_id = u.id AND np.type = 'new_report' AND np.enabled = FALSE
  )
ON CONFLICT (user_id, report_id) WHERE type = 'new_report' DO NOTHING;

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
