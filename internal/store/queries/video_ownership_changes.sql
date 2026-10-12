-- name: ResolveOwnershipRecipient :one
-- The account a video may be given to, by username (case-insensitive). No row
-- means "no such eligible user": unknown, deactivated or banned (is_active is
-- THE ban flag), deleted, or on either side of a block with the initiator. One
-- answer for all of them, so the give route cannot be used to tell them apart.
-- muted_initiator reports whether the recipient muted the initiator: the
-- request still stands, but no notification is pushed at someone who asked not
-- to hear from them (the FollowNotificationRecipient rule).
SELECT u.id,
       EXISTS (
           SELECT 1 FROM muted_accounts ma
           WHERE ma.muter_id = u.id AND ma.muted_id = sqlc.arg('initiator_id')::uuid
       ) AS muted_initiator
FROM users u
WHERE lower(u.username) = lower(sqlc.arg('username')::text)
  AND u.is_active
  AND u.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE (ub.blocker_id = u.id AND ub.blocked_id = sqlc.arg('initiator_id')::uuid)
         OR (ub.blocker_id = sqlc.arg('initiator_id')::uuid AND ub.blocked_id = u.id)
  );

-- name: CreateVideoOwnershipChange :one
-- A new pending request. A second pending request for the same video hits
-- video_ownership_changes_one_pending_idx and inserts nothing (pgx.ErrNoRows),
-- so two concurrent gives cannot both land.
INSERT INTO video_ownership_changes (video_id, initiator_id, next_owner_id)
VALUES (sqlc.arg('video_id'), sqlc.arg('initiator_id'), sqlc.arg('next_owner_id'))
ON CONFLICT (video_id) WHERE status = 'pending' DO NOTHING
RETURNING id, video_id, initiator_id, next_owner_id, status, created_at, updated_at;

-- name: GetVideoOwnershipChange :one
SELECT id, video_id, initiator_id, next_owner_id, status, created_at, updated_at
FROM video_ownership_changes
WHERE id = $1;

-- name: ListIncomingVideoOwnershipChanges :many
-- The requests addressed to a user, newest first, with what the recipient needs
-- to decide: which video, and who is offering it.
SELECT oc.id, oc.status, oc.created_at, oc.updated_at,
       oc.video_id, v.title AS video_title,
       iu.username AS initiator_username, iu.display_name AS initiator_display_name
FROM video_ownership_changes oc
JOIN videos v ON v.id = oc.video_id
JOIN users iu ON iu.id = oc.initiator_id
WHERE oc.next_owner_id = sqlc.arg('next_owner_id')
ORDER BY oc.created_at DESC, oc.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountIncomingVideoOwnershipChanges :one
SELECT count(*)::bigint
FROM video_ownership_changes
WHERE next_owner_id = $1;

-- name: AcceptVideoOwnershipChange :one
-- Accept AND move, in one statement, so a request can never read 'accepted'
-- for a video that did not move (or the reverse). The request flips only while
-- ALL of these hold at the moment of the write:
--   * it is still pending and addressed to next_owner_id;
--   * its initiator STILL owns the video (ownership may have changed since the
--     give — the request must not outlive it);
--   * the target channel belongs to next_owner_id.
-- No row means one of them failed. The move itself is the same channel_id
-- assignment a same-owner move makes (UpdateVideo); the owner follows the
-- channel. from_channel_id is where the video was, for the move hooks.
WITH req AS (
    UPDATE video_ownership_changes oc
    SET status = 'accepted', updated_at = now()
    FROM videos v
    JOIN channels vc ON vc.id = v.channel_id
    JOIN channels tc ON tc.id = sqlc.arg('channel_id')::uuid
    WHERE oc.id = sqlc.arg('id')
      AND oc.next_owner_id = sqlc.arg('next_owner_id')
      AND oc.status = 'pending'
      AND v.id = oc.video_id
      AND vc.owner_id = oc.initiator_id
      AND tc.owner_id = oc.next_owner_id
    RETURNING oc.video_id, v.channel_id AS from_channel_id
)
UPDATE videos
SET channel_id = sqlc.arg('channel_id')::uuid, updated_at = now()
FROM req
WHERE videos.id = req.video_id
RETURNING videos.id, req.from_channel_id;

-- name: RefuseVideoOwnershipChange :execrows
-- Refuse a pending request addressed to next_owner_id. 0 rows: not theirs, or
-- already answered.
UPDATE video_ownership_changes
SET status = 'refused', updated_at = now()
WHERE id = sqlc.arg('id')
  AND next_owner_id = sqlc.arg('next_owner_id')
  AND status = 'pending';
