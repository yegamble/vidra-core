-- name: CreateComment :one
-- parent_id is NULL for a top-level comment, or the id of the comment being
-- replied to (the service validates it references a comment on the same video).
INSERT INTO comments (video_id, user_id, body, parent_id)
VALUES (sqlc.arg('video_id'), sqlc.arg('user_id'), sqlc.arg('body'), sqlc.narg('parent_id'))
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id;

-- name: ListCommentsByVideo :many
-- A video's comments, newest first, joined with author identity for display.
-- When viewer_id is provided (an authenticated viewer), comments authored by an
-- account that viewer has muted OR blocked are hidden (§13: blocking hides the
-- blocked account's content from the blocker); when NULL (anonymous), nothing
-- is filtered — a NULL viewer matches no muted_accounts/user_blocks row.
SELECT c.id, c.video_id, c.user_id, c.body, c.parent_id, c.created_at, c.updated_at,
       u.username AS author_username, u.display_name AS author_display_name
FROM comments c
JOIN users u ON u.id = c.user_id
WHERE c.video_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.user_id
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: GetComment :one
SELECT id, video_id, user_id, body, created_at, updated_at, parent_id
FROM comments
WHERE id = $1;

-- name: UpdateComment :one
-- Edit a comment's body (author-only; enforced in the service). Bumps updated_at
-- so clients can show an "edited" marker (updated_at > created_at).
UPDATE comments
SET body = sqlc.arg('body'), updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id;

-- name: DeleteComment :exec
DELETE FROM comments
WHERE id = $1;

-- name: CountComments :one
-- Total comments — the "local comments" count NodeInfo advertises.
SELECT count(*) FROM comments;

-- name: ListAdminComments :many
-- The admin/moderator comments overview: ALL comments newest first, with the
-- author's identity and the video they're on. An optional case-insensitive body
-- filter (NULL = no filter).
SELECT c.id, c.video_id, c.body, c.created_at,
       u.username AS author_username, u.display_name AS author_display_name,
       v.title AS video_title
FROM comments c
JOIN users u ON u.id = c.user_id
JOIN videos v ON v.id = c.video_id
WHERE (sqlc.narg('query')::text IS NULL OR c.body ILIKE '%' || sqlc.narg('query') || '%')
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
