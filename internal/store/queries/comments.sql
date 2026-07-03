-- name: CreateComment :one
-- parent_id is NULL for a top-level comment, or the id of the comment being
-- replied to (the service validates it references a comment on the same video).
INSERT INTO comments (video_id, user_id, body, parent_id)
VALUES (sqlc.arg('video_id'), sqlc.arg('user_id'), sqlc.arg('body'), sqlc.narg('parent_id'))
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url;

-- name: CreateRemoteComment :one
-- Store an inbound federated Note as a comment (remote-content §6): attributed
-- to a remote actor (with a display-name snapshot) instead of a local user.
-- Idempotent by the Note's ActivityPub object id — a re-delivered or updated
-- Note refreshes the body.
INSERT INTO comments (video_id, body, parent_id, remote_actor_url, remote_author_name, remote_object_url)
VALUES (sqlc.arg('video_id'), sqlc.arg('body'), sqlc.narg('parent_id'),
        sqlc.arg('remote_actor_url'), sqlc.arg('remote_author_name'), sqlc.arg('remote_object_url'))
ON CONFLICT (remote_object_url) DO UPDATE
    SET body = EXCLUDED.body, updated_at = now()
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url;

-- name: ListCommentsByVideo :many
-- A video's comments, newest first, joined with author identity for display.
-- A comment is authored by a local user OR a remote actor (§6): the users join
-- is LEFT and remote rows carry remote_author_name + the origin domain. When
-- viewer_id is provided (an authenticated viewer), comments authored by an
-- account that viewer has muted OR blocked are hidden (§13), and remote
-- comments from instances that viewer muted are hidden too (§8); when NULL
-- (anonymous), only the admin instance blocklist filters (it hides for
-- everyone).
SELECT c.id, c.video_id, c.user_id, c.body, c.parent_id, c.created_at, c.updated_at,
       COALESCE(u.username, '')::text AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name,
       c.remote_actor_url,
       COALESCE(c.remote_author_name, '')::text AS remote_author_name,
       COALESCE(ra.domain, '')::text AS author_domain
FROM comments c
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
WHERE c.video_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM blocked_instances bi WHERE bi.domain = ra.domain
  )
  AND NOT EXISTS (
      SELECT 1 FROM muted_instances mi
      WHERE mi.muter_id = sqlc.narg('viewer_id') AND mi.domain = ra.domain
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: GetComment :one
SELECT id, video_id, user_id, body, created_at, updated_at, parent_id,
       remote_actor_url, remote_author_name, remote_object_url
FROM comments
WHERE id = $1;

-- name: GetCommentByRemoteObjectURL :one
-- Look a federated comment up by its Note's ActivityPub object id — the key
-- inbound Update{Note}/Delete use (§6/§7). Local comments never match (their
-- remote_object_url is NULL).
SELECT id, video_id, user_id, body, created_at, updated_at, parent_id,
       remote_actor_url, remote_author_name, remote_object_url
FROM comments
WHERE remote_object_url = sqlc.arg('remote_object_url')::text;

-- name: UpdateComment :one
-- Edit a comment's body (author-only; enforced in the service). Bumps updated_at
-- so clients can show an "edited" marker (updated_at > created_at).
UPDATE comments
SET body = sqlc.arg('body'), updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url;

-- name: DeleteComment :exec
DELETE FROM comments
WHERE id = $1;

-- name: CountComments :one
-- LOCAL comments only — the "local comments" count NodeInfo advertises.
-- Federated (remote-authored) comments are excluded.
SELECT count(*) FROM comments WHERE user_id IS NOT NULL;

-- name: ListAdminComments :many
-- The admin/moderator comments overview: ALL comments newest first, with the
-- author's identity (local user or remote actor + domain, §6) and the video
-- they're on. An optional case-insensitive body filter (NULL = no filter).
SELECT c.id, c.video_id, c.body, c.created_at,
       COALESCE(u.username, '')::text AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name,
       c.remote_actor_url,
       COALESCE(c.remote_author_name, '')::text AS remote_author_name,
       COALESCE(ra.domain, '')::text AS author_domain,
       v.title AS video_title
FROM comments c
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
JOIN videos v ON v.id = c.video_id
WHERE (sqlc.narg('query')::text IS NULL OR c.body ILIKE '%' || sqlc.narg('query') || '%')
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
