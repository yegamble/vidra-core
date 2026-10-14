-- name: CreateComment :one
-- parent_id is NULL for a top-level comment, or the id of the comment being
-- replied to (the service validates it references a comment on the same video).
-- held_for_review (0161) is decided by the caller from the video's
-- comments_policy and who is posting; a held comment stays out of every public
-- read until a manager approves it.
INSERT INTO comments (video_id, user_id, body, parent_id, held_for_review)
VALUES (sqlc.arg('video_id'), sqlc.arg('user_id'), sqlc.arg('body'), sqlc.narg('parent_id'), sqlc.arg('held_for_review'))
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review;

-- name: CreateRemoteComment :one
-- Store an inbound federated Note as a comment (remote-content §6): attributed
-- to a remote actor (with a display-name snapshot) instead of a local user.
-- Idempotent by the Note's ActivityPub object id — a re-delivered or updated
-- Note refreshes the body — only on the comment's OWN video (no row otherwise),
-- so the hold decision always comes from the stored row's video (0161): passed
-- true there, a CHANGED body re-holds an approved comment; nothing releases one.
INSERT INTO comments (video_id, body, parent_id, remote_actor_url, remote_author_name, remote_object_url, held_for_review)
VALUES (sqlc.arg('video_id'), sqlc.arg('body'), sqlc.narg('parent_id'),
        sqlc.arg('remote_actor_url'), sqlc.arg('remote_author_name'), sqlc.arg('remote_object_url'),
        sqlc.arg('held_for_review'))
ON CONFLICT (remote_object_url) DO UPDATE
    SET body = EXCLUDED.body, updated_at = now(),
        held_for_review = comments.held_for_review
            OR (EXCLUDED.held_for_review AND comments.body IS DISTINCT FROM EXCLUDED.body)
    WHERE comments.video_id = EXCLUDED.video_id
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review;

-- name: ListCommentsByVideo :many
-- A video's comments, the creator-pinned one first then newest first, joined with
-- author identity for display. Each row carries the creator-heart flag and a
-- computed `pinned` boolean (whether it is this video's pinned_comment_id).
-- A comment is authored by a local user OR a remote actor (§6): the users join
-- is LEFT and remote rows carry remote_author_name + the origin domain. When
-- viewer_id is provided (an authenticated viewer), comments authored by an
-- account that viewer has muted OR blocked are hidden (§13), and remote
-- comments from instances that viewer muted are hidden too (§8); when NULL
-- (anonymous), only the admin instance blocklist filters (it hides for
-- everyone).
SELECT c.id, c.video_id, c.user_id, c.body, c.parent_id, c.created_at, c.updated_at, c.deleted_at, c.hearted,
       c.held_for_review,
       (c.id IS NOT DISTINCT FROM v.pinned_comment_id)::boolean AS pinned,
       COALESCE(u.username, '')::text AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name,
       c.remote_actor_url,
       COALESCE(c.remote_author_name, '')::text AS remote_author_name,
       COALESCE(ra.domain, '')::text AS author_domain
FROM comments c
JOIN videos v ON v.id = c.video_id
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
  -- The per-remote-ACCOUNT block, RETROACTIVELY (A29 parity, migration 0142).
  -- The rehearsal measured the gap here and nowhere else: blocking a remote
  -- account removed its remote VIDEOS from six viewer-facing queries, and left
  -- the comments it had already written sitting under the blocker's own video —
  -- against the settings page's own promise that "their replies stay off your
  -- videos". Hiding is a READ filter, so an unblock restores the thread exactly.
  -- remote_actor_block_reach carries both scopes: the viewer's own blocks and
  -- the admin's instance-wide per-actor blocks (blocker_id NULL, hidden for
  -- everyone including anonymous readers).
  AND NOT EXISTS (
      SELECT 1 FROM remote_actor_block_reach rab
      WHERE (rab.blocker_id = sqlc.narg('viewer_id') OR rab.blocker_id IS NULL)
        AND rab.actor_url = c.remote_actor_url
  )
  -- Held comments (0161): only their author, and callers the HTTP layer marked
  -- as reviewers (include_held), see them — in the rows and in the count.
  AND (NOT c.held_for_review
       OR sqlc.arg('include_held')::boolean
       OR c.user_id = sqlc.narg('viewer_id'))
ORDER BY (c.id IS NOT DISTINCT FROM v.pinned_comment_id) DESC, c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountCommentsByVideo :one
-- How many comments ListCommentsByVideo would return for the same viewer,
-- ignoring pagination. Every mute/block/instance predicate is repeated verbatim:
-- the count is per-VIEWER, so counting the raw comments rows would tell a viewer
-- there are more comments than they can ever page to.
SELECT count(*)::bigint
FROM comments c
JOIN videos v ON v.id = c.video_id
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
  -- The per-remote-ACCOUNT block, RETROACTIVELY (A29 parity, migration 0142).
  -- The rehearsal measured the gap here and nowhere else: blocking a remote
  -- account removed its remote VIDEOS from six viewer-facing queries, and left
  -- the comments it had already written sitting under the blocker's own video —
  -- against the settings page's own promise that "their replies stay off your
  -- videos". Hiding is a READ filter, so an unblock restores the thread exactly.
  -- remote_actor_block_reach carries both scopes: the viewer's own blocks and
  -- the admin's instance-wide per-actor blocks (blocker_id NULL, hidden for
  -- everyone including anonymous readers).
  AND NOT EXISTS (
      SELECT 1 FROM remote_actor_block_reach rab
      WHERE (rab.blocker_id = sqlc.narg('viewer_id') OR rab.blocker_id IS NULL)
        AND rab.actor_url = c.remote_actor_url
  )
  -- Held comments (0161): only their author, and callers the HTTP layer marked
  -- as reviewers (include_held), see them — in the rows and in the count.
  AND (NOT c.held_for_review
       OR sqlc.arg('include_held')::boolean
       OR c.user_id = sqlc.narg('viewer_id'));

-- name: GetComment :one
SELECT id, video_id, user_id, body, created_at, updated_at, parent_id,
       remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review
FROM comments
WHERE id = $1;

-- name: GetCommentWithMeta :one
-- A single comment with its author identity, remote origin, current heart flag,
-- and whether it is its video's pinned comment — the projection the pin/heart
-- endpoints return after acting. No viewer-mute filtering: the caller is the
-- video owner (or staff), authorized in the HTTP layer.
SELECT c.id, c.video_id, c.user_id, c.body, c.parent_id, c.created_at, c.updated_at, c.deleted_at, c.hearted,
       c.held_for_review,
       (c.id IS NOT DISTINCT FROM v.pinned_comment_id)::boolean AS pinned,
       COALESCE(u.username, '')::text AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name,
       c.remote_actor_url,
       COALESCE(c.remote_author_name, '')::text AS remote_author_name,
       COALESCE(ra.domain, '')::text AS author_domain
FROM comments c
JOIN videos v ON v.id = c.video_id
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
WHERE c.id = sqlc.arg('id');

-- name: SetCommentHearted :one
-- Toggle the creator-heart flag on a comment (video-owner/staff action;
-- authorized in the HTTP layer). Local metadata only — it never bumps updated_at
-- (so the "edited" marker is unaffected) and never federates. Works on
-- remote-authored comments too. Returns the updated row.
UPDATE comments
SET hearted = sqlc.arg('hearted')
WHERE id = sqlc.arg('id')
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review;

-- name: ApproveComment :one
-- Release a held comment (0161). Conditional on the row STILL being held, so
-- repeated or concurrent approvals have exactly one winner (no row = nothing to
-- do) and the withheld side effects fire once. Never bumps updated_at.
-- expected_updated_at is the version the reviewer saw: an edit since then
-- (updated_at moved) is not approved.
UPDATE comments
SET held_for_review = false
WHERE id = sqlc.arg('id') AND held_for_review AND updated_at = sqlc.arg('expected_updated_at')
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review;

-- name: GetCommentByRemoteObjectURL :one
-- Look a federated comment up by its Note's ActivityPub object id — the key
-- inbound Update{Note}/Delete use (§6/§7). Local comments never match (their
-- remote_object_url is NULL).
SELECT id, video_id, user_id, body, created_at, updated_at, parent_id,
       remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review
FROM comments
WHERE remote_object_url = sqlc.arg('remote_object_url')::text;
-- NOTE: keep the column list of the two queries above in lock-step; both project
-- the full comments row so sqlc maps them to the Comment model.

-- name: UpdateComment :one
-- Edit a comment's body (author-only; enforced in the service). Bumps updated_at
-- so clients can show an "edited" marker (updated_at > created_at). hold
-- (0161): a body CHANGE that must be reviewed (a non-reviewer's edit on a
-- requires_approval video) sends the comment back to held; never releases one.
UPDATE comments
SET held_for_review = held_for_review OR (sqlc.arg('hold')::boolean AND body IS DISTINCT FROM sqlc.arg('body')),
    body = sqlc.arg('body'), updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, video_id, user_id, body, created_at, updated_at, parent_id,
          remote_actor_url, remote_author_name, remote_object_url, deleted_at, hearted, held_for_review;

-- name: DeleteComment :exec
DELETE FROM comments
WHERE id = $1;

-- name: GetCommentAuthorIDByUsername :one
-- Resolve a username to its account id for the bulk comment removal. Unlike
-- GetUserByUsername this does NOT filter on is_active: a moderator cleaning up
-- after a spam wave is usually doing so for an account already disabled.
SELECT id FROM users WHERE lower(username) = lower($1);

-- name: DeleteCommentsByAuthor :many
-- Set-based form of DeleteComment for the moderator bulk removal: every comment
-- authored by one local account (user_id) OR one remote actor
-- (remote_actor_url), in one statement. It is the same hard DELETE as
-- DeleteComment, so the same FKs fire per row: replies (parent_id), reports,
-- notifications and watched-word matches CASCADE, and videos.pinned_comment_id
-- is SET NULL. RETURNING hands the caller the ids the rows no longer hold, so
-- LOCAL authors' deletions can still be federated.
DELETE FROM comments
WHERE (sqlc.narg('user_id')::uuid IS NOT NULL AND user_id = sqlc.narg('user_id')::uuid)
   OR (sqlc.narg('remote_actor_url')::text IS NOT NULL AND remote_actor_url = sqlc.narg('remote_actor_url')::text)
RETURNING id, video_id, user_id;

-- name: ListManagedVideoComments :many
-- The review queue (0161): comments on every video the caller manages (owned or
-- edited channels), newest first, with author + video title; held filters
-- (NULL = both). Deliberately NO viewer mute/block filtering: a held comment
-- hidden from its only reviewers would wait forever.
SELECT c.id, c.video_id, c.user_id, c.body, c.parent_id, c.created_at, c.updated_at, c.deleted_at, c.hearted,
       c.held_for_review,
       (c.id IS NOT DISTINCT FROM v.pinned_comment_id)::boolean AS pinned,
       COALESCE(u.username, '')::text AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name,
       c.remote_actor_url,
       COALESCE(c.remote_author_name, '')::text AS remote_author_name,
       COALESCE(ra.domain, '')::text AS author_domain,
       v.title AS video_title
FROM comments c
JOIN videos v ON v.id = c.video_id
JOIN channels ch ON ch.id = v.channel_id
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
WHERE (ch.owner_id = sqlc.arg('user_id')
       OR EXISTS (SELECT 1 FROM channel_members m WHERE m.channel_id = ch.id AND m.user_id = sqlc.arg('user_id')))
  AND (sqlc.narg('held')::boolean IS NULL OR c.held_for_review = sqlc.narg('held')::boolean)
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountManagedVideoComments :one
-- How many rows ListManagedVideoComments would return for the same caller and
-- held filter, ignoring pagination. Same predicate, verbatim.
SELECT count(*)::bigint
FROM comments c
JOIN videos v ON v.id = c.video_id
JOIN channels ch ON ch.id = v.channel_id
WHERE (ch.owner_id = sqlc.arg('user_id')
       OR EXISTS (SELECT 1 FROM channel_members m WHERE m.channel_id = ch.id AND m.user_id = sqlc.arg('user_id')))
  AND (sqlc.narg('held')::boolean IS NULL OR c.held_for_review = sqlc.narg('held')::boolean);

-- name: CountComments :one
-- LOCAL published comments only — the "local comments" count NodeInfo
-- advertises. Federated (remote-authored) and held (0161) comments are excluded.
SELECT count(*) FROM comments WHERE user_id IS NOT NULL AND NOT held_for_review;

-- name: ListAdminComments :many
-- The admin/moderator comments overview: ALL comments newest first, with the
-- author's identity (local user or remote actor + domain, §6) and the video
-- they're on. An optional case-insensitive body filter and an optional
-- automatic-tag filter (0177); NULL = no filter.
SELECT c.id, c.video_id, c.body, c.created_at, c.deleted_at,
       COALESCE(u.username, '')::text AS author_username,
       COALESCE(u.display_name, '')::text AS author_display_name,
       c.remote_actor_url,
       COALESCE(c.remote_author_name, '')::text AS remote_author_name,
       COALESCE(ra.domain, '')::text AS author_domain,
       v.title AS video_title,
       COALESCE((SELECT array_agg(t.tag ORDER BY t.tag) FROM comment_automatic_tags t WHERE t.comment_id = c.id), '{}')::text[] AS automatic_tags
FROM comments c
LEFT JOIN users u ON u.id = c.user_id
LEFT JOIN remote_actors ra ON ra.actor_url = c.remote_actor_url
JOIN videos v ON v.id = c.video_id
WHERE (sqlc.narg('query')::text IS NULL OR c.body ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('auto_tag')::text IS NULL OR EXISTS (
        SELECT 1 FROM comment_automatic_tags t WHERE t.comment_id = c.id AND lower(t.tag) = lower(sqlc.narg('auto_tag')::text)))
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountAdminComments :one
-- How many rows ListAdminComments would return for the same ?q filter, ignoring
-- pagination. CountComments above answers a DIFFERENT question (local comments
-- for NodeInfo, remote-authored ones excluded) and must not be reused here: it
-- would under-report the admin queue by exactly the federated comments a
-- moderator most needs to see. The videos JOIN is part of the predicate.
SELECT count(*)::bigint
FROM comments c
JOIN videos v ON v.id = c.video_id
WHERE (sqlc.narg('query')::text IS NULL OR c.body ILIKE '%' || sqlc.narg('query') || '%')
  AND (sqlc.narg('auto_tag')::text IS NULL OR EXISTS (
        SELECT 1 FROM comment_automatic_tags t WHERE t.comment_id = c.id AND lower(t.tag) = lower(sqlc.narg('auto_tag')::text)));

-- name: TombstoneUserComments :exec
-- §1 hard delete: a deleted account's comments become tombstones — the body is
-- emptied and deleted_at stamped, but the rows (and so the reply threads under
-- them) are preserved. Views render "[deleted]" for tombstoned comments.
-- Their automatic tags (0177) are derived from the erased body, so they go in
-- the same statement.
WITH gone AS (
    DELETE FROM comment_automatic_tags
    WHERE comment_id IN (SELECT k.id FROM comments k WHERE k.user_id = sqlc.arg('user_id') AND k.deleted_at IS NULL)
)
UPDATE comments
SET body = '', deleted_at = now(), updated_at = now()
WHERE comments.user_id = sqlc.arg('user_id') AND comments.deleted_at IS NULL;
