-- name: CreateNotification :one
-- Record a notification for a recipient (user_id). Context columns are optional
-- and depend on the type.
--
-- The same statement records whether it is ALSO owed an email (migration 0157):
-- a notification_emails row when, and only when, the recipient explicitly
-- turned email on for this type. Email is opt-in, and the prefs API accepts an
-- email choice only for the types that offer it, so for every other type the
-- second insert is empty. One statement, so a notification can never exist
-- without the email decision that belongs to it (or the reverse).
WITH n AS (
    INSERT INTO notifications (user_id, type, actor_id, channel_id, video_id, comment_id, conversation_id, report_id)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    RETURNING id, user_id, type, actor_id, channel_id, video_id, comment_id, read_at, created_at, conversation_id, report_id, registration_request_id, remote_follow_id
), owed AS (
    INSERT INTO notification_emails (notification_id)
    SELECT n.id FROM n
    JOIN notification_prefs np ON np.user_id = n.user_id AND np.type = n.type
    WHERE np.email_enabled
)
SELECT id, user_id, type, actor_id, channel_id, video_id, comment_id, read_at, created_at, conversation_id, report_id, registration_request_id, remote_follow_id
FROM n;

-- name: NotifyFollowersOfNewVideo :one
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
--
-- The same statement queues the EMAILS (migration 0157): one notification_emails
-- row per notification it actually inserted whose recipient explicitly turned
-- new_video email on. Keyed off what the INSERT returned, so the no-op re-run
-- above queues nothing either; nothing is mailed here — the outbox drain sends
-- in the background, so a large follower list never mails inline. Returns how
-- many followers were notified (not how many emails were queued).
WITH ins AS (
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
ON CONFLICT (user_id, video_id) WHERE type = 'new_video' DO NOTHING
RETURNING id, user_id
), owed AS (
    INSERT INTO notification_emails (notification_id)
    SELECT ins.id FROM ins
    JOIN notification_prefs np ON np.user_id = ins.user_id AND np.type = 'new_video'
    WHERE np.email_enabled
)
SELECT count(*)::bigint FROM ins;

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
  -- not <>: a remote Flag report (0153) has a NULL reporter_id
  AND r.reporter_id IS DISTINCT FROM u.id
  AND u.is_active
  AND u.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM notification_prefs np
      WHERE np.user_id = u.id AND np.type = 'new_report' AND np.enabled = FALSE
  )
ON CONFLICT (user_id, report_id) WHERE type = 'new_report' DO NOTHING;

-- name: NotifyStaffOfNewRegistrationRequest :execrows
-- Staff push signal for the registration approval queue (migration 0156), fired
-- when a request is filed. Set-based like NotifyStaffOfNewReport. Recipients are
-- the roles that may work the queue (admin + moderator, matching the
-- /admin/registration-requests route gate), active and non-deleted, minus anyone
-- who turned the type off. The applicant has no account, so there is no actor.
-- Idempotent via notifications_new_registration_request_unique_idx.
INSERT INTO notifications (user_id, type, registration_request_id)
SELECT u.id, 'new_registration_request', rr.id
FROM registration_requests rr
JOIN users u ON u.role IN ('admin', 'moderator')
WHERE rr.id = sqlc.arg('request_id')
  AND rr.status = 'pending'
  AND u.is_active
  AND u.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM notification_prefs np
      WHERE np.user_id = u.id AND np.type = 'new_registration_request' AND np.enabled = FALSE
  )
ON CONFLICT (user_id, registration_request_id) WHERE type = 'new_registration_request' DO NOTHING;

-- name: NotifyStaffOfQuarantinedUpload :execrows
-- Staff push signal for the upload quarantine queue (migration 0156), fired when
-- a finished upload parks in 'quarantined'. Recipients are the roles behind
-- /admin/videos/quarantined (admin + moderator), minus the uploader (staff
-- uploads bypass the gate, but a moderator-owned video never self-notifies) and
-- anyone who turned the type off. The video must still be quarantined, so a
-- late fan-out for an already-approved video is a no-op. Idempotent via
-- notifications_upload_quarantined_unique_idx.
INSERT INTO notifications (user_id, type, actor_id, channel_id, video_id)
SELECT u.id, 'upload_quarantined', ch.owner_id, v.channel_id, v.id
FROM videos v
JOIN channels ch ON ch.id = v.channel_id
JOIN users u ON u.role IN ('admin', 'moderator')
WHERE v.id = sqlc.arg('video_id')
  AND v.state = 'quarantined'
  AND ch.owner_id <> u.id
  AND u.is_active
  AND u.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM notification_prefs np
      WHERE np.user_id = u.id AND np.type = 'upload_quarantined' AND np.enabled = FALSE
  )
ON CONFLICT (user_id, video_id) WHERE type = 'upload_quarantined' DO NOTHING;

-- name: NotifyStaffOfFederationFollowerRequest :execrows
-- Staff push signal for the federation follower-approval queue (migration 0156),
-- fired when a remote actor's follow lands as 'pending'. The queue's routes are
-- admin-only (/admin/federation/follower-requests), so ONLY admins are told —
-- notifying a moderator of a queue they cannot open would be noise. The followed
-- channel is stored for display; the remote actor is not a local user, so there
-- is no actor. Idempotent via notifications_federation_follower_request_unique_idx.
INSERT INTO notifications (user_id, type, channel_id, remote_follow_id)
SELECT u.id, 'federation_follower_request', rf.channel_id, rf.id
FROM remote_follows rf
JOIN users u ON u.role = 'admin'
WHERE rf.id = sqlc.arg('remote_follow_id')
  AND rf.state = 'pending'
  AND u.is_active
  AND u.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM notification_prefs np
      WHERE np.user_id = u.id AND np.type = 'federation_follower_request' AND np.enabled = FALSE
  )
ON CONFLICT (user_id, remote_follow_id) WHERE type = 'federation_follower_request' DO NOTHING;

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
       -- The moderator's prose, and ONLY on the two notifications that exist to
       -- deliver it: the rejection note (0130) on video_rejected, and the block
       -- reason (video_blocks.reason, creator-facing since the A16 ruling) on
       -- video_blocked. Joining either unconditionally would hand it to every
       -- other video-linked notification about the same video (new_video fans
       -- out to followers), so the type is part of the predicate rather than
       -- something the Go layer is trusted to remember. A lifted block deletes
       -- its row, so an old video_blocked notice silently loses its reason and
       -- renders as the neutral notice — correct: the reason no longer applies.
       CASE WHEN n.type = 'video_rejected' THEN COALESCE(vr.note, '')
            WHEN n.type = 'video_blocked'  THEN COALESCE(vb.reason, '')
            ELSE '' END::text AS moderation_note,
       r.status AS report_status, r.target_type AS report_target_type
FROM notifications n
LEFT JOIN users a ON a.id = n.actor_id
LEFT JOIN channels c ON c.id = n.channel_id
LEFT JOIN videos v ON v.id = n.video_id
LEFT JOIN video_rejections vr ON vr.video_id = n.video_id
LEFT JOIN video_blocks vb ON vb.video_id = n.video_id
LEFT JOIN reports r ON r.id = n.report_id
WHERE n.user_id = sqlc.arg('user_id')
  AND (NOT sqlc.arg('unread_only')::bool OR n.read_at IS NULL)
ORDER BY n.created_at DESC, n.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountNotifications :one
-- How many rows ListNotifications would return for the same user and
-- ?unread filter, ignoring pagination. This is the LIST total; the
-- CountUnreadNotifications badge below is a different question and both are
-- returned side by side (an unread_count of 3 says nothing about how many
-- pages of read notifications sit behind it).
SELECT count(*)::bigint
FROM notifications n
WHERE n.user_id = sqlc.arg('user_id')
  AND (NOT sqlc.arg('unread_only')::bool OR n.read_at IS NULL);

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

-- name: CommentReplyRecipient :one
-- Who should be told that a comment is a REPLY to their comment — the parent
-- author, resolved with every exclusion applied IN SQL (the 0101/0103 idiom:
-- selection rules belong in the statement, not in an N+1 of Go lookups). No
-- row means nobody is notified, which is the normal answer for a top-level
-- comment.
--
-- Deliberately resolved only when ALL of these hold:
--   * the comment is a reply (parent_id set) and the parent still exists;
--   * the parent is authored by a LOCAL user — a federated parent has a NULL
--     user_id (0053) and no local inbox to deliver into;
--   * neither the reply nor the parent is a tombstone (0057): a deleted
--     account's comment must not pull its anonymised row back into someone's
--     inbox;
--   * the reply is not HELD for review (0161): until a manager approves it,
--     only they and its author may read it, so naming it in a third party's
--     inbox would leak it — approval re-fires this resolution;
--   * the parent's author is an active, non-deleted account;
--   * the replier is not the parent's author (no self-notification, matching
--     every other Notify* path);
--   * the parent author has not muted the replier, and neither side has
--     blocked the other — a blocked/muted account must not reach the blocker's
--     notification surface, the same exclusion NotifyFollowersOfNewVideo makes.
--
-- The reply's video is returned alongside so the notification can carry the
-- watch context without a second round trip.
SELECT p.user_id AS recipient_id, r.video_id AS video_id
FROM comments r
JOIN comments p ON p.id = r.parent_id
JOIN users pu ON pu.id = p.user_id
WHERE r.id = sqlc.arg('comment_id')
  AND r.deleted_at IS NULL
  AND p.deleted_at IS NULL
  AND NOT r.held_for_review
  AND r.user_id IS NOT NULL
  AND p.user_id IS DISTINCT FROM r.user_id
  AND pu.is_active
  AND pu.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts ma
      WHERE ma.muter_id = p.user_id AND ma.muted_id = r.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE (ub.blocker_id = p.user_id AND ub.blocked_id = r.user_id)
         OR (ub.blocker_id = r.user_id AND ub.blocked_id = p.user_id)
  );

-- name: CommentVideoOwnerRecipient :one
-- Who should be told that their VIDEO was commented on — the video's owner,
-- resolved with every exclusion applied IN SQL (the same 0101/0103 idiom
-- CommentReplyRecipient above and NotifyFollowersOfNewVideo already follow:
-- selection rules belong in the statement, not in an N+1 of Go lookups). No row
-- means nobody is notified.
--
-- Deliberately resolved only when ALL of these hold:
--   * the comment exists, is not a tombstone (0057), and was written by a LOCAL
--     user — a federated comment has a NULL user_id (0053) and no local actor to
--     name, and the inbound federation path does not raise this notification;
--   * the comment is not HELD for review (0161) — a held comment tells the
--     video's managers through comment_held instead;
--   * the video's owner is not the commenter (no self-notification, matching
--     every other Notify* path);
--   * the owner is an active, non-deleted account;
--   * the owner has not muted the commenter, and neither side has blocked the
--     other. This is the clause the reply path had from the start and this one
--     did not: muting an account promises its comments are hidden from you, and
--     an inbox row naming that account — carrying the comment id back to the
--     hidden content — is the same content arriving through another door.
--
-- The comment's video is returned alongside so the notification can carry the
-- watch context without a second round trip.
SELECT ch.owner_id AS recipient_id, c.video_id AS video_id
FROM comments c
JOIN videos v ON v.id = c.video_id
JOIN channels ch ON ch.id = v.channel_id
JOIN users ou ON ou.id = ch.owner_id
WHERE c.id = sqlc.arg('comment_id')
  AND c.deleted_at IS NULL
  AND NOT c.held_for_review
  AND c.user_id IS NOT NULL
  AND ch.owner_id IS DISTINCT FROM c.user_id
  AND ou.is_active
  AND ou.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts ma
      WHERE ma.muter_id = ch.owner_id AND ma.muted_id = c.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE (ub.blocker_id = ch.owner_id AND ub.blocked_id = c.user_id)
         OR (ub.blocker_id = c.user_id AND ub.blocked_id = ch.owner_id)
  );

-- name: NotifyCommentMentions :execrows
-- Tell every LOCAL account @-mentioned in a comment, in one round trip (the
-- 0101/0103 idiom: selection rules live in the statement, not in an N+1 of Go
-- lookups). `usernames` is the already-parsed, lower-cased list; unknown names
-- simply match no row.
--
-- A mention notifies only when ALL of these hold:
--   * the comment is live (not a tombstone, 0057) and on a public, published,
--     unblocked video — a mention must not leak a private video's existence;
--   * the recipient is an active, non-deleted account and not the author;
--   * the recipient has not muted the author and neither side blocked the other
--     (for a federated comment user_id is NULL, so these are vacuously true);
--   * the recipient has not turned 'comment_mention' off (absence of a prefs
--     row = enabled);
--   * the recipient is not ALREADY being told about this comment as a reply or
--     as the video owner — one comment, one notification.
-- The author is the actor; a federated comment has none (the UI falls back to
-- comments.remote_author_name). ON CONFLICT rides
-- notifications_comment_mention_unique_idx, so re-running is a no-op; a comment
-- never holds more than 10 comment_mention rows in total.
WITH cand AS (
    SELECT u.id AS user_id, c.user_id AS actor_id, c.video_id, c.id AS comment_id,
           row_number() OVER (ORDER BY u.id) AS rn
    FROM comments c
    JOIN videos v ON v.id = c.video_id
    JOIN users u ON lower(u.username) = ANY(sqlc.arg('usernames')::text[])
    LEFT JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
    WHERE c.id = sqlc.arg('comment_id')::uuid
      AND c.deleted_at IS NULL
      AND v.privacy = 'public' AND v.state = 'published'
      AND NOT EXISTS (SELECT 1 FROM video_blocks vb WHERE vb.video_id = v.id)
      AND c.user_id IS DISTINCT FROM u.id
      AND u.is_active
      AND u.deleted_at IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM muted_accounts ma
          WHERE ma.muter_id = u.id AND ma.muted_id = c.user_id
      )
      AND NOT EXISTS (
          SELECT 1 FROM user_blocks ub
          WHERE (ub.blocker_id = u.id AND ub.blocked_id = c.user_id)
             OR (ub.blocker_id = c.user_id AND ub.blocked_id = u.id)
      )
      -- A federated comment has no local author, so the checks above are vacuous
      -- for it. These are the SAME predicates ListCommentsByVideo hides it with:
      -- instance blocks, the recipient's instance mutes, and remote-actor blocks
      -- (the recipient's own plus the admin's instance-wide ones).
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain)
      AND NOT EXISTS (
          SELECT 1 FROM muted_instances mi
          WHERE mi.muter_id = u.id AND mi.domain = ra.domain
      )
      AND NOT EXISTS (
          SELECT 1 FROM remote_actor_block_reach rab
          WHERE (rab.blocker_id = u.id OR rab.blocker_id IS NULL)
            AND rab.actor_url = c.remote_actor_url
      )
      AND NOT EXISTS (
          SELECT 1 FROM notification_prefs np
          WHERE np.user_id = u.id AND np.type = 'comment_mention' AND np.enabled = FALSE
      )
      AND NOT EXISTS (
          SELECT 1 FROM notifications n
          WHERE n.user_id = u.id AND n.comment_id = c.id
            AND n.type IN ('comment', 'comment_reply', 'comment_mention')
      )
)
INSERT INTO notifications (user_id, type, actor_id, video_id, comment_id)
SELECT user_id, 'comment_mention', actor_id, video_id, comment_id
FROM cand
-- At most 10 comment_mention rows per comment IN TOTAL, so repeated edits cannot
-- keep fanning out fresh batches.
WHERE rn <= 10 - (SELECT count(*) FROM notifications x
                  WHERE x.comment_id = sqlc.arg('comment_id')::uuid AND x.type = 'comment_mention')
ON CONFLICT (user_id, comment_id) WHERE type = 'comment_mention' DO NOTHING;

-- name: FollowNotificationRecipient :one
-- Who should be told that their channel was followed — the channel's owner,
-- resolved with every exclusion applied IN SQL (the same 0101/0103 idiom
-- CommentReplyRecipient and CommentVideoOwnerRecipient follow: selection rules
-- belong in the statement, not in an N+1 of Go lookups). No row means nobody is
-- notified.
--
-- Deliberately resolved only when ALL of these hold:
--   * the channel exists;
--   * the owner is not the follower (no self-notification, matching every other
--     Notify* path);
--   * the owner is an active, non-deleted account;
--   * the owner has not muted the follower, and neither side has blocked the
--     other. This clause is why the query exists. A follow is the one action a
--     muted or blocked account could still take to put its username in the
--     muter's inbox, and it is repeatable — unfollow and follow again and the
--     handler's `created` flag is true again, so an account the viewer has
--     BLOCKED could ping them without limit. Every other notification path
--     already excluded them; this one simply never did.
SELECT ch.owner_id AS recipient_id
FROM channels ch
JOIN users ou ON ou.id = ch.owner_id
WHERE ch.id = sqlc.arg('channel_id')
  AND ch.owner_id IS DISTINCT FROM sqlc.arg('follower_id')
  AND ou.is_active
  AND ou.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts ma
      WHERE ma.muter_id = ch.owner_id AND ma.muted_id = sqlc.arg('follower_id')
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE (ub.blocker_id = ch.owner_id AND ub.blocked_id = sqlc.arg('follower_id'))
         OR (ub.blocker_id = sqlc.arg('follower_id') AND ub.blocked_id = ch.owner_id)
  );
