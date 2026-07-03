-- name: CreateVideoReport :execrows
-- Report a video (idempotent per reporter+video). Returns rows inserted
-- (1 = new, 0 = already reported).
INSERT INTO reports (reporter_id, target_type, video_id, reason)
VALUES ($1, 'video', $2, $3)
ON CONFLICT (reporter_id, video_id) WHERE video_id IS NOT NULL DO NOTHING;

-- name: CreateCommentReport :execrows
-- Report a comment (idempotent per reporter+comment). A non-existent comment
-- raises a foreign-key violation, which the service maps to "invalid target".
INSERT INTO reports (reporter_id, target_type, comment_id, reason)
VALUES ($1, 'comment', $2, $3)
ON CONFLICT (reporter_id, comment_id) WHERE comment_id IS NOT NULL DO NOTHING;

-- name: CreateAccountReport :execrows
-- Report an account (idempotent per reporter+account). A non-existent target
-- user raises a foreign-key violation, which the service maps to "invalid
-- target". Self-reports are rejected in the service before insert.
INSERT INTO reports (reporter_id, target_type, reported_user_id, reason)
VALUES ($1, 'account', $2, $3)
ON CONFLICT (reporter_id, reported_user_id) WHERE reported_user_id IS NOT NULL DO NOTHING;

-- name: ListReports :many
-- The moderation queue, newest first, with the reporter's username and the
-- target context (video title / comment body / reported account username). When
-- open_only is true, only unresolved (status='open') reports are returned.
SELECT r.id, r.target_type, r.video_id, r.comment_id, r.reported_user_id, r.reason, r.status,
       r.moderator_note, r.resolved_at, r.created_at,
       u.username AS reporter_username,
       v.title AS video_title,
       cm.body AS comment_body,
       ru.username AS reported_username
FROM reports r
JOIN users u ON u.id = r.reporter_id
LEFT JOIN videos v ON v.id = r.video_id
LEFT JOIN comments cm ON cm.id = r.comment_id
LEFT JOIN users ru ON ru.id = r.reported_user_id
WHERE (NOT sqlc.arg('open_only')::bool OR r.status = 'open')
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

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
