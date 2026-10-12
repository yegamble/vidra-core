-- name: CreateVideoReport :one
-- Report a video (idempotent per reporter+video). Returns the new report's id;
-- a repeat report by the same reporter inserts nothing and yields no row
-- (pgx.ErrNoRows), which the service maps to "already reported" so the staff
-- fan-out fires only for genuinely new reports. The ::uuid casts keep a local
-- report's reporter_id required although the column is nullable since 0153.
INSERT INTO reports (reporter_id, target_type, video_id, reason)
VALUES (sqlc.arg('reporter_id')::uuid, 'video', sqlc.arg('video_id'), sqlc.arg('reason'))
ON CONFLICT (reporter_id, video_id) WHERE video_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateCommentReport :one
-- Report a comment (idempotent per reporter+comment; returns the new report's
-- id, no row on a repeat). A non-existent comment raises a foreign-key
-- violation, which the service maps to "invalid target".
INSERT INTO reports (reporter_id, target_type, comment_id, reason)
VALUES (sqlc.arg('reporter_id')::uuid, 'comment', sqlc.arg('comment_id'), sqlc.arg('reason'))
ON CONFLICT (reporter_id, comment_id) WHERE comment_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateAccountReport :one
-- Report an account (idempotent per reporter+account; returns the new report's
-- id, no row on a repeat). A non-existent target user raises a foreign-key
-- violation, which the service maps to "invalid target". Self-reports are
-- rejected in the service before insert.
INSERT INTO reports (reporter_id, target_type, reported_user_id, reason)
VALUES (sqlc.arg('reporter_id')::uuid, 'account', sqlc.arg('reported_user_id'), sqlc.arg('reason'))
ON CONFLICT (reporter_id, reported_user_id) WHERE reported_user_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateRemoteVideoReport :one
-- Report a federated remote video (remote-content §8; idempotent per
-- reporter+remote video; returns the new report's id, no row on a repeat). A
-- non-existent target raises a foreign-key violation, which the service maps
-- to "invalid target".
INSERT INTO reports (reporter_id, target_type, remote_video_id, reason)
VALUES (sqlc.arg('reporter_id')::uuid, 'remote_video', sqlc.arg('remote_video_id'), sqlc.arg('reason'))
ON CONFLICT (reporter_id, remote_video_id) WHERE remote_video_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateMessageReport :one
-- Report a direct-message message (product-decisions.md §14; idempotent per
-- reporter+message; returns the new report's id, no row on a repeat). The body
-- snapshot preserves the reported text for the moderator even after the sender
-- tombstones it. The caller has already verified the reporter is a participant
-- of the message's conversation.
INSERT INTO reports (reporter_id, target_type, message_id, message_body_snapshot, reason)
VALUES (sqlc.arg('reporter_id')::uuid, 'message', sqlc.arg('message_id'), sqlc.arg('message_body_snapshot'), sqlc.arg('reason'))
ON CONFLICT (reporter_id, message_id) WHERE message_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: CreateRemoteFlagReport :one
-- A remote Flag report (0153). No row for a repeat (Flag, target) or while the
-- domain has an open report on the target; an unknown target violates the FK.
INSERT INTO reports (target_type, video_id, comment_id, reported_user_id, reason,
                     remote_reporter_actor_url, remote_reporter_domain, remote_flag_activity_id)
VALUES (sqlc.arg('target_type'), sqlc.narg('video_id'), sqlc.narg('comment_id'), sqlc.narg('reported_user_id'),
        sqlc.arg('reason'), sqlc.arg('actor_url')::text, sqlc.arg('domain')::text, sqlc.arg('flag_activity_id')::text)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: ListReports :many
-- The moderation queue, newest first, with the reporter's username and the
-- target context (video title / comment body / reported account username /
-- remote video title+domain / message body snapshot); a remote Flag report
-- (0153) has reporter_username '' and the remote actor. status is the exact
-- lifecycle state to show, or NULL for all of them. It used to be an open_only
-- BOOLEAN fed by `?status == "open"` in the handler, so ?status=resolved
-- silently returned the whole queue.
SELECT r.id, r.target_type, r.video_id, r.comment_id, r.reported_user_id, r.remote_video_id,
       r.message_id, r.message_body_snapshot,
       r.reason, r.status,
       r.moderator_note, r.resolved_at, r.created_at,
       COALESCE(u.username, '')::text AS reporter_username,
       r.remote_reporter_actor_url, r.remote_reporter_domain,
       v.title AS video_title,
       cm.body AS comment_body,
       ru.username AS reported_username,
       rv.title AS remote_video_title,
       ra.domain AS remote_video_domain
FROM reports r
LEFT JOIN users u ON u.id = r.reporter_id
LEFT JOIN videos v ON v.id = r.video_id
LEFT JOIN comments cm ON cm.id = r.comment_id
LEFT JOIN users ru ON ru.id = r.reported_user_id
LEFT JOIN remote_videos rv ON rv.id = r.remote_video_id
LEFT JOIN remote_actors ra ON ra.actor_url = rv.remote_actor_url
WHERE (sqlc.narg('status')::text IS NULL
       -- 'resolved' is the queue's other half, not a stored value: reports.status
       -- is open/accepted/rejected, so "resolved" means "no longer open".
       OR (sqlc.narg('status')::text = 'resolved' AND r.status <> 'open')
       OR r.status = sqlc.narg('status')::text)
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountReports :one
-- How many rows ListReports would return for the same status filter, ignoring
-- pagination. The WHERE must stay identical to it (its joins are all LEFT).
SELECT count(*)::bigint
FROM reports r
WHERE (sqlc.narg('status')::text IS NULL
       -- 'resolved' is the queue's other half, not a stored value: reports.status
       -- is open/accepted/rejected, so "resolved" means "no longer open".
       OR (sqlc.narg('status')::text = 'resolved' AND r.status <> 'open')
       OR r.status = sqlc.narg('status')::text);

-- name: DeleteReport :execrows
-- Hard-delete a report row (admin purge). Notifications referencing it cascade
-- away (0042 FK). Returns rows deleted so the caller can log 0 vs 1; the
-- endpoint is idempotent either way.
DELETE FROM reports WHERE id = $1;

-- name: ResolveReport :one
-- Mark a report accepted/rejected with a moderator note. Returns the reporter
-- so the caller can notify them (best-effort; NULL for a remote Flag report);
-- an unknown id yields no rows, which the service maps to "not found".
UPDATE reports
SET status         = sqlc.arg('status'),
    moderator_note = sqlc.arg('moderator_note'),
    resolved_by    = sqlc.arg('resolved_by'),
    resolved_at    = now(),
    updated_at     = now()
WHERE id = sqlc.arg('id')
RETURNING reporter_id;

-- name: ListMyReports :many
-- A reporter's own reports, most recently active first (filing, resolution or
-- the newest thread message). Never the moderator's note; a video title only
-- while the video passes the public check (videos.sql: public, published, not
-- blocked). report_id narrows to one (NULL = all).
SELECT r.id, r.target_type, r.status, r.reason, r.created_at, r.resolved_at,
       r.video_id,
       COALESCE(CASE WHEN v.privacy = 'public' AND v.state = 'published'
                      AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
                     THEN v.title END, '')::text AS video_title,
       ru.username AS reported_username, rv.title AS remote_video_title,
       COALESCE(t.message_count, 0)::bigint AS message_count,
       GREATEST(r.created_at, r.resolved_at, t.last_message_at)::timestamptz AS last_activity_at
FROM reports r
LEFT JOIN videos v ON v.id = r.video_id
LEFT JOIN users ru ON ru.id = r.reported_user_id
LEFT JOIN remote_videos rv ON rv.id = r.remote_video_id
LEFT JOIN LATERAL (
    SELECT count(*) AS message_count, max(m.created_at) AS last_message_at
    FROM report_messages m WHERE m.report_id = r.id
) t ON TRUE
WHERE r.reporter_id = sqlc.arg('reporter_id')::uuid
  AND (sqlc.narg('report_id')::uuid IS NULL OR r.id = sqlc.narg('report_id')::uuid)
ORDER BY last_activity_at DESC, r.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountMyReports :one
-- ListMyReports' total (no report_id narrowing).
SELECT count(*)::bigint FROM reports WHERE reporter_id = sqlc.arg('reporter_id')::uuid;
