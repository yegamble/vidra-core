-- Notification email outbox (migration 0157). A row means "this in-app
-- notification is also owed an email". It is deleted once the email is sent or
-- is no longer owed, so the table only ever holds outstanding work.

-- name: ClaimDueNotificationEmails :many
-- Claims up to claim_limit due rows, oldest first, under a lease (attempts + 1,
-- next_attempt_at pushed out by lease_seconds). FOR UPDATE SKIP LOCKED, so every
-- api instance can drain at once without two of them taking the same row.
-- Returns what the drain needs to address and render each email.
--
-- `deliverable` re-asks at SEND time what creation could not settle for good.
-- The recipient must still be an active, non-deleted account with a VERIFIED
-- address: an unproven address may be someone else's inbox, the one way this
-- feature could be aimed at a third party. Their explicit email opt-in AND the
-- in-app switch for the type must both still be on. They must not have since
-- muted or blocked the actor, or been blocked by them. A new_video recipient
-- must still follow the channel with the bell on 'all'. The video must still be
-- published and public — every email type is raised only on a public video
-- (comments are refused elsewhere), so anything else means it was hidden since
-- and its title must not be mailed — and not under a moderation block, and a
-- comment must still exist. A row that fails any of these is dropped, never
-- retried.
WITH due AS (
    SELECT ne.notification_id
    FROM notification_emails ne
    WHERE ne.next_attempt_at <= now()
    ORDER BY ne.next_attempt_at, ne.notification_id
    LIMIT sqlc.arg('claim_limit')
    FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE notification_emails ne
    SET attempts = ne.attempts + 1,
        next_attempt_at = now() + make_interval(secs => sqlc.arg('lease_seconds')::double precision)
    FROM due
    WHERE ne.notification_id = due.notification_id
    RETURNING ne.notification_id, ne.attempts, ne.created_at
)
SELECT c.notification_id, c.attempts, c.created_at AS queued_at, n.type, u.email,
       COALESCE(NULLIF(a.display_name, ''), a.username, '')::text AS actor_name,
       COALESCE(NULLIF(ch.display_name, ''), ch.handle, '')::text AS channel_name,
       n.video_id, COALESCE(v.title, '')::text AS video_title,
       COALESCE(
           u.is_active AND u.deleted_at IS NULL AND u.email_verified
           AND np.email_enabled IS TRUE AND np.enabled
           AND NOT EXISTS (SELECT 1 FROM video_blocks vb WHERE vb.video_id = n.video_id)
           AND (n.video_id IS NULL OR (v.state = 'published' AND v.privacy = 'public'))
           AND (n.type <> 'new_video' OR EXISTS (
               SELECT 1 FROM channel_follows cf
               WHERE cf.follower_id = n.user_id AND cf.channel_id = n.channel_id
                 AND cf.notification_setting = 'all'))
           AND (n.comment_id IS NULL OR EXISTS (
               SELECT 1 FROM comments cm WHERE cm.id = n.comment_id AND cm.deleted_at IS NULL))
           AND NOT EXISTS (
               SELECT 1 FROM muted_accounts ma
               WHERE ma.muter_id = n.user_id AND ma.muted_id = n.actor_id)
           AND NOT EXISTS (
               SELECT 1 FROM user_blocks ub
               WHERE (ub.blocker_id = n.user_id AND ub.blocked_id = n.actor_id)
                  OR (ub.blocker_id = n.actor_id AND ub.blocked_id = n.user_id)),
           FALSE)::bool AS deliverable
FROM claimed c
JOIN notifications n ON n.id = c.notification_id
JOIN users u ON u.id = n.user_id
LEFT JOIN notification_prefs np ON np.user_id = n.user_id AND np.type = n.type
LEFT JOIN users a ON a.id = n.actor_id
LEFT JOIN channels ch ON ch.id = n.channel_id
LEFT JOIN videos v ON v.id = n.video_id
ORDER BY c.notification_id;

-- name: DeleteNotificationEmail :exec
-- The email was sent, or is no longer owed (undeliverable, stale, or out of
-- retries).
DELETE FROM notification_emails WHERE notification_id = $1;

-- name: RescheduleNotificationEmail :exec
-- A send failed: the row is due again after the caller's backoff.
UPDATE notification_emails SET next_attempt_at = $2 WHERE notification_id = $1;

-- name: DeleteStaleNotificationEmails :execrows
-- Drop owed emails older than max_age_seconds without claiming them. Used while
-- the instance has no mail path: the outbox waits untouched for one to be
-- configured, but never longer than a day-old email is worth sending.
DELETE FROM notification_emails
WHERE created_at < now() - make_interval(secs => sqlc.arg('max_age_seconds')::double precision);
