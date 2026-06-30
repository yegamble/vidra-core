-- name: CreateComment :one
INSERT INTO comments (video_id, user_id, body)
VALUES ($1, $2, $3)
RETURNING id, video_id, user_id, body, created_at, updated_at;

-- name: ListCommentsByVideo :many
-- A video's comments, newest first, joined with author identity for display.
-- When viewer_id is provided (an authenticated viewer), comments authored by an
-- account that viewer has muted are hidden; when NULL (anonymous), nothing is
-- filtered — a NULL muter_id matches no muted_accounts row.
SELECT c.id, c.video_id, c.user_id, c.body, c.created_at, c.updated_at,
       u.username AS author_username, u.display_name AS author_display_name
FROM comments c
JOIN users u ON u.id = c.user_id
WHERE c.video_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.user_id
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: GetComment :one
SELECT id, video_id, user_id, body, created_at, updated_at
FROM comments
WHERE id = $1;

-- name: DeleteComment :exec
DELETE FROM comments
WHERE id = $1;
