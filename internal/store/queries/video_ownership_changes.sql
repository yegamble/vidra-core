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
-- video_title is the title at give time (see 0165).
INSERT INTO video_ownership_changes (video_id, initiator_id, next_owner_id, video_title)
VALUES (sqlc.arg('video_id'), sqlc.arg('initiator_id'), sqlc.arg('next_owner_id'), sqlc.arg('video_title'))
ON CONFLICT (video_id) WHERE status = 'pending' DO NOTHING
RETURNING id, video_id, initiator_id, next_owner_id, video_title, status, created_at, updated_at;

-- name: GetVideoOwnershipChange :one
SELECT id, video_id, initiator_id, next_owner_id, video_title, status, created_at, updated_at
FROM video_ownership_changes
WHERE id = $1;

-- name: ExpireStaleVideoOwnershipChanges :execrows
-- Marks a video's pending request 'expired' when it can no longer be honoured:
-- its giver no longer owns the video, or it was made at or before
-- expired_before (the offer window). Run before a give, so a stale request
-- never blocks the video's current owner from offering it.
UPDATE video_ownership_changes oc
SET status = 'expired', updated_at = now()
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE oc.video_id = sqlc.arg('video_id')
  AND v.id = oc.video_id
  AND oc.status = 'pending'
  AND (c.owner_id <> oc.initiator_id OR oc.created_at <= sqlc.arg('expired_before'));

-- name: HasPendingVideoOwnershipChange :one
SELECT EXISTS (
    SELECT 1 FROM video_ownership_changes
    WHERE video_id = $1 AND status = 'pending'
);

-- name: HasRecentOwnershipRefusal :one
-- Whether an offer from initiator_id to next_owner_id (any video) ENDED after
-- refused_after — refused by the recipient, or withdrawn by the giver: the
-- re-offer cooldown. Counting withdrawals too is what stops offer → withdraw →
-- offer again from pushing the recipient a fresh notification each time.
SELECT EXISTS (
    SELECT 1 FROM video_ownership_changes
    WHERE initiator_id = sqlc.arg('initiator_id')
      AND next_owner_id = sqlc.arg('next_owner_id')
      AND status IN ('refused', 'cancelled')
      AND updated_at > sqlc.arg('refused_after')
);

-- name: CancelVideoOwnershipChange :execrows
-- The giver withdraws their pending offer of a video. 0 rows: no pending
-- request for that video was made by them.
UPDATE video_ownership_changes
SET status = 'cancelled', updated_at = now()
WHERE video_id = sqlc.arg('video_id')
  AND initiator_id = sqlc.arg('initiator_id')
  AND status = 'pending';

-- name: VideoMediaWorkInFlight :one
-- Whether media work on the video is unfinished: an open upload or
-- replacement session (an 'active' one only until it expires), or a pending or
-- running finalize, import or transcode job. An ownership change waits for it:
-- its result must land with the owner who started it, and its bytes must be
-- known before the recipient's quota is checked.
SELECT (
    EXISTS (SELECT 1 FROM upload_sessions us
            WHERE us.video_id = $1
              AND (us.state IN ('queued', 'processing')
                   OR (us.state = 'active' AND us.expires_at > now())))
    OR EXISTS (SELECT 1 FROM upload_finalize_jobs fj
               WHERE fj.video_id = $1 AND fj.state IN ('pending', 'running'))
    OR EXISTS (SELECT 1 FROM import_jobs ij
               WHERE ij.video_id = $1 AND ij.state IN ('pending', 'running'))
    OR EXISTS (SELECT 1 FROM transcode_jobs tj
               WHERE tj.video_id = $1 AND tj.state IN ('pending', 'running'))
)::bool AS in_flight;

-- name: ListIncomingVideoOwnershipChanges :many
-- The requests addressed to a user, newest first, with what the recipient needs
-- to decide: which video, and who is offering it. The title is the snapshot
-- taken at give time, never the video's live one: once a request is answered
-- the video may pass to someone else, and the old recipient must not keep a
-- window onto it. Every request that can no longer be answered — withdrawn by
-- the giver, older than the offer window, or one whose giver no longer owns the
-- video, even before a give has marked it — reads the same 'expired', so the
-- recipient learns "no longer available" and never which of those it was.
SELECT oc.id,
       (CASE WHEN oc.status IN ('cancelled', 'expired')
                  OR (oc.status = 'pending'
                      AND (c.owner_id <> oc.initiator_id OR oc.created_at <= sqlc.arg('expired_before')))
             THEN 'expired' ELSE oc.status END)::text AS status,
       oc.created_at, oc.updated_at,
       oc.video_id, oc.video_title,
       iu.username AS initiator_username, iu.display_name AS initiator_display_name
FROM video_ownership_changes oc
JOIN videos v ON v.id = oc.video_id
JOIN channels c ON c.id = v.channel_id
JOIN users iu ON iu.id = oc.initiator_id
WHERE oc.next_owner_id = sqlc.arg('next_owner_id')
ORDER BY oc.created_at DESC, oc.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountIncomingVideoOwnershipChanges :one
SELECT count(*)::bigint
FROM video_ownership_changes
WHERE next_owner_id = $1;

-- name: LockOwnershipChangeVideo :one
-- Takes the row lock on the video a request is about. The accept runs in the
-- same transaction right after it, so its snapshot is taken only once nothing
-- else holding that lock (a session being opened, a replacement being swapped
-- in) is still in flight.
SELECT v.id
FROM videos v
JOIN video_ownership_changes oc ON oc.video_id = v.id
WHERE oc.id = $1
FOR UPDATE OF v;

-- name: AcceptVideoOwnershipChange :one
-- Accept AND move, in one statement, so a request can never read 'accepted'
-- for a video that did not move (or the reverse). The request flips only while
-- ALL of these hold at the moment of the write:
--   * it is still pending, addressed to next_owner_id, and made after
--     expired_before (the offer window);
--   * its initiator STILL owns the video (ownership may have changed since the
--     give — the request must not outlive it);
--   * the target channel belongs to next_owner_id;
--   * no media work on the video is unfinished (VideoMediaWorkInFlight, the
--     same four clauses inline).
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
      AND oc.created_at > sqlc.arg('expired_before')
      AND vc.owner_id = oc.initiator_id
      AND tc.owner_id = oc.next_owner_id
      AND NOT EXISTS (SELECT 1 FROM upload_sessions us
                      WHERE us.video_id = v.id
                        AND (us.state IN ('queued', 'processing')
                             OR (us.state = 'active' AND us.expires_at > now())))
      AND NOT EXISTS (SELECT 1 FROM upload_finalize_jobs fj
                      WHERE fj.video_id = v.id AND fj.state IN ('pending', 'running'))
      AND NOT EXISTS (SELECT 1 FROM import_jobs ij
                      WHERE ij.video_id = v.id AND ij.state IN ('pending', 'running'))
      AND NOT EXISTS (SELECT 1 FROM transcode_jobs tj
                      WHERE tj.video_id = v.id AND tj.state IN ('pending', 'running'))
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
