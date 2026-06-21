-- name: CreateComment :one
INSERT INTO comments (video_id, user_id, body)
VALUES ($1, $2, $3)
RETURNING id, video_id, user_id, body, created_at, updated_at;

-- name: ListCommentsByVideo :many
-- A video's comments, newest first, joined with author identity for display.
SELECT c.id, c.video_id, c.user_id, c.body, c.created_at, c.updated_at,
       u.username AS author_username, u.display_name AS author_display_name
FROM comments c
JOIN users u ON u.id = c.user_id
WHERE c.video_id = $1
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: GetComment :one
SELECT id, video_id, user_id, body, created_at, updated_at
FROM comments
WHERE id = $1;

-- name: DeleteComment :exec
DELETE FROM comments
WHERE id = $1;
