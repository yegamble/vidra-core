-- Locally-authored comments on remote videos (migration 0147). The home instance
-- OWNS these rows: it stores, moderates and federates them (a Create{Note}
-- inReplyTo the remote video) — the reverse of the read-only mirror in
-- remote_video_comments (0140).

-- name: CreateAuthoredRemoteComment :one
-- The id and object_url are minted by the service (object_url embeds the id), so
-- both are passed explicitly rather than defaulted.
INSERT INTO authored_remote_comments (
    id, remote_video_id, user_id, body, object_url, in_reply_to
) VALUES (
    sqlc.arg('id'), sqlc.arg('remote_video_id'), sqlc.arg('user_id'),
    sqlc.arg('body'), sqlc.arg('object_url'), sqlc.arg('in_reply_to')
)
RETURNING id, remote_video_id, user_id, body, object_url, in_reply_to,
          delivery_state, last_error, attempts, edited, created_at, updated_at;

-- name: GetAuthoredRemoteComment :one
-- One authored remote comment by id — the federation build reads its object_url,
-- in_reply_to, user_id and remote_video_id; the HTTP layer reads user_id for the
-- author-only authorization check.
SELECT id, remote_video_id, user_id, body, object_url, in_reply_to,
       delivery_state, last_error, attempts, edited, created_at, updated_at
FROM authored_remote_comments
WHERE id = $1;

-- name: ListAuthoredRemoteCommentsByVideo :many
-- One remote video's locally-authored comments, oldest first, with the local
-- author's identity. When viewer_id is provided, comments by an account the
-- viewer has muted OR blocked are hidden (parity with ListCommentsByVideo);
-- anonymous callers (viewer_id NULL) see all.
SELECT c.id, c.remote_video_id, c.user_id, c.body, c.object_url, c.in_reply_to,
       c.delivery_state, c.last_error, c.attempts, c.edited,
       c.created_at, c.updated_at,
       COALESCE(u.username, '')::text     AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name
FROM authored_remote_comments c
LEFT JOIN users u ON u.id = c.user_id
WHERE c.remote_video_id = sqlc.arg('remote_video_id')
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.user_id
  )
ORDER BY c.created_at, c.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountAuthoredRemoteCommentsByVideo :one
-- How many rows ListAuthoredRemoteCommentsByVideo would return for the same
-- viewer (same filters, so the count never promises rows the list won't serve).
SELECT count(*)::bigint
FROM authored_remote_comments c
WHERE c.remote_video_id = sqlc.arg('remote_video_id')
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.user_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = c.user_id
  );

-- name: UpdateAuthoredRemoteCommentBody :one
-- Edit the body (author only; the HTTP layer authorizes). Marks edited and RESETS
-- delivery_state to pending: an edit re-federates as an Update{Note}, so the
-- author's status badge must go back to pending until that lands.
UPDATE authored_remote_comments
SET body = sqlc.arg('body'), edited = TRUE, delivery_state = 'pending',
    last_error = '', updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, remote_video_id, user_id, body, object_url, in_reply_to,
          delivery_state, last_error, attempts, edited, created_at, updated_at;

-- name: DeleteAuthoredRemoteComment :execrows
-- Hard delete (author or admin; the HTTP layer authorizes). The federation Delete
-- is enqueued from the captured ids before/after this — the row is gone, so its
-- queued Delete's authored_remote_comment_id is NULLed by the FK.
DELETE FROM authored_remote_comments WHERE id = $1;

-- name: SetAuthoredRemoteCommentDeliveryState :exec
-- Reflect the delivery queue's result onto the authored comment's status badge.
-- Called from DrainDeliveries when a delivery that carries an
-- authored_remote_comment_id is marked delivered/failed. The comment stays
-- displayed locally regardless — this only moves the badge.
UPDATE authored_remote_comments
SET delivery_state = sqlc.arg('delivery_state'),
    last_error = sqlc.arg('last_error'),
    attempts = sqlc.arg('attempts'),
    updated_at = now()
WHERE id = sqlc.arg('id');
