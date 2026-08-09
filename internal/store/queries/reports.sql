-- name: CreateVideoReport :one
-- Report a video (idempotent per reporter+video). Returns the new report's id;
-- a repeat report by the same reporter inserts nothing and yields no row
-- (pgx.ErrNoRows), which the service maps to "already reported" so the staff
-- fan-out fires only for genuinely new reports.
INSERT INTO reports (reporter_id, target_type, video_id, reason)
VALUES ($1, 'video', $2, $3)
ON CONFLICT (reporter_id, video_id) WHERE video_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateCommentReport :one
-- Report a comment (idempotent per reporter+comment; returns the new report's
-- id, no row on a repeat). A non-existent comment raises a foreign-key
-- violation, which the service maps to "invalid target".
INSERT INTO reports (reporter_id, target_type, comment_id, reason)
VALUES ($1, 'comment', $2, $3)
ON CONFLICT (reporter_id, comment_id) WHERE comment_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateAccountReport :one
-- Report an account (idempotent per reporter+account; returns the new report's
-- id, no row on a repeat). A non-existent target user raises a foreign-key
-- violation, which the service maps to "invalid target". Self-reports are
-- rejected in the service before insert.
INSERT INTO reports (reporter_id, target_type, reported_user_id, reason)
VALUES ($1, 'account', $2, $3)
ON CONFLICT (reporter_id, reported_user_id) WHERE reported_user_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateRemoteVideoReport :one
-- Report a federated remote video (remote-content §8; idempotent per
-- reporter+remote video; returns the new report's id, no row on a repeat). A
-- non-existent target raises a foreign-key violation, which the service maps
-- to "invalid target".
INSERT INTO reports (reporter_id, target_type, remote_video_id, reason)
VALUES ($1, 'remote_video', $2, $3)
ON CONFLICT (reporter_id, remote_video_id) WHERE remote_video_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateMessageReport :one
-- Report a direct-message message (product-decisions.md §14; idempotent per
-- reporter+message; returns the new report's id, no row on a repeat). The body
-- snapshot preserves the reported text for the moderator even after the sender
-- tombstones it. The caller has already verified the reporter is a participant
-- of the message's conversation.
INSERT INTO reports (reporter_id, target_type, message_id, message_body_snapshot, reason)
VALUES ($1, 'message', $2, $3, $4)
ON CONFLICT (reporter_id, message_id) WHERE message_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: ListReports :many
-- The moderation queue, newest first, with the reporter's username and the
-- target context (video title / comment body / reported account username /
-- remote video title+domain / message body snapshot). When open_only is true,
-- only unresolved (status='open') reports are returned.
SELECT r.id, r.target_type, r.video_id, r.comment_id, r.reported_user_id, r.remote_video_id,
       r.message_id, r.message_body_snapshot,
       r.reason, r.status,
       r.moderator_note, r.resolved_at, r.created_at,
       u.username AS reporter_username,
       v.title AS video_title,
       cm.body AS comment_body,
       ru.username AS reported_username,
       rv.title AS remote_video_title,
       ra.domain AS remote_video_domain
FROM reports r
JOIN users u ON u.id = r.reporter_id
LEFT JOIN videos v ON v.id = r.video_id
LEFT JOIN comments cm ON cm.id = r.comment_id
LEFT JOIN users ru ON ru.id = r.reported_user_id
LEFT JOIN remote_videos rv ON rv.id = r.remote_video_id
LEFT JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
WHERE (NOT sqlc.arg('open_only')::bool OR r.status = 'open')
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: DeleteReport :execrows
-- Hard-delete a report row (admin purge). Notifications referencing it cascade
-- away (0042 FK). Returns rows deleted so the caller can log 0 vs 1; the
-- endpoint is idempotent either way.
DELETE FROM reports WHERE id = $1;

-- name: ResolveReport :one
-- Mark a report accepted/rejected with a moderator note. Returns the reporter
-- so the caller can notify them (best-effort); an unknown id yields no rows,
-- which the service maps to "not found".
UPDATE reports
SET status         = sqlc.arg('status'),
    moderator_note = sqlc.arg('moderator_note'),
    resolved_by    = sqlc.arg('resolved_by'),
    resolved_at    = now(),
    updated_at     = now()
WHERE id = sqlc.arg('id')
RETURNING reporter_id;
