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

-- name: ListReports :many
-- The moderation queue, newest first, with the reporter's username and the
-- target context (video title / comment body). When open_only is true, only
-- unresolved (status='open') reports are returned.
SELECT r.id, r.target_type, r.video_id, r.comment_id, r.reason, r.status,
       r.moderator_note, r.resolved_at, r.created_at,
       u.username AS reporter_username,
       v.title AS video_title,
       cm.body AS comment_body
FROM reports r
JOIN users u ON u.id = r.reporter_id
LEFT JOIN videos v ON v.id = r.video_id
LEFT JOIN comments cm ON cm.id = r.comment_id
WHERE (NOT sqlc.arg('open_only')::bool OR r.status = 'open')
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: ResolveReport :execrows
-- Mark a report accepted/rejected with a moderator note. Returns the number of
-- rows matched so the caller can 404 on an unknown id.
UPDATE reports
SET status         = sqlc.arg('status'),
    moderator_note = sqlc.arg('moderator_note'),
    resolved_by    = sqlc.arg('resolved_by'),
    resolved_at    = now(),
    updated_at     = now()
WHERE id = sqlc.arg('id');
