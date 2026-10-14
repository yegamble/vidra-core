-- name: UpsertVideoRating :exec
-- Set or change a user's rating for a video.
INSERT INTO video_ratings (video_id, user_id, rating)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, video_id)
DO UPDATE SET rating = EXCLUDED.rating, updated_at = now();

-- name: DeleteVideoRating :exec
DELETE FROM video_ratings
WHERE user_id = $1 AND video_id = $2;

-- name: GetVideoRating :one
SELECT rating
FROM video_ratings
WHERE user_id = $1 AND video_id = $2;

-- name: CountVideoRatings :one
-- Local users' votes plus remote actors' (0178): both are rows, so the total is
-- derived here and never taken from a remote-supplied count.
--
-- A remote vote whose instance is blocked, whose actor (or owning account) is
-- blocked instance-wide, or whose actor the video's OWNER blocks is filtered
-- here, at read time, rather than purged on block: a block taken after the vote
-- stops it counting, and an unblock restores it exactly — the same READ-filter
-- choice the comment thread makes (comments.sql, remote_actor_block_reach).
SELECT
    COUNT(*) FILTER (WHERE r.rating = 'like')    AS likes,
    COUNT(*) FILTER (WHERE r.rating = 'dislike') AS dislikes
FROM (
    SELECT vr.rating FROM video_ratings vr WHERE vr.video_id = sqlc.arg('video_id')::uuid
    UNION ALL
    SELECT rr.rating FROM remote_video_ratings rr
    WHERE rr.video_id = sqlc.arg('video_id')::uuid
      AND NOT EXISTS (SELECT 1 FROM blocked_instances bi WHERE bi.domain = rr.domain)
      AND NOT EXISTS (
          SELECT 1 FROM remote_actor_block_reach rab
          WHERE rab.actor_url = rr.remote_actor_url
            AND (rab.blocker_id IS NULL OR rab.blocker_id = (
                SELECT ch.owner_id FROM videos v JOIN channels ch ON ch.id = v.channel_id
                WHERE v.id = sqlc.arg('video_id')::uuid))
      )
) r;

-- name: UpsertRemoteVideoRating :execrows
-- A remote actor's Like/Dislike of a local video: one row per (actor, video),
-- so a repeat is a no-op and a switch changes the row in place. A NEW row is
-- written only while the signer's domain has fewer than domain_cap new votes in
-- the last hour (0 rows affected = capped); changing an existing vote is never
-- capped.
INSERT INTO remote_video_ratings (video_id, remote_actor_url, rating, activity_url, domain)
SELECT sqlc.arg('video_id')::uuid, sqlc.arg('remote_actor_url')::text, sqlc.arg('rating')::text,
       sqlc.arg('activity_url')::text, sqlc.arg('domain')::text
WHERE EXISTS (
        SELECT 1 FROM remote_video_ratings e
        WHERE e.remote_actor_url = sqlc.arg('remote_actor_url')::text AND e.video_id = sqlc.arg('video_id')::uuid)
   OR (SELECT count(*) FROM remote_video_ratings d
       WHERE d.domain = sqlc.arg('domain')::text AND d.created_at > now() - interval '1 hour')
      < sqlc.arg('domain_cap')::bigint
ON CONFLICT (remote_actor_url, video_id)
DO UPDATE SET rating = EXCLUDED.rating, activity_url = EXCLUDED.activity_url, updated_at = now();

-- name: DeleteRemoteVideoRating :exec
-- Undo{Like|Dislike}: removes the actor's vote only when it is still the kind
-- being undone (an Undo{Like} after a switch to dislike changes nothing), or
-- when the Undo names the rating's own activity id.
DELETE FROM remote_video_ratings
WHERE remote_actor_url = sqlc.arg('remote_actor_url')
  AND ((video_id = sqlc.arg('video_id') AND rating = sqlc.arg('rating'))
       OR activity_url = sqlc.arg('activity_url'));

-- name: GetRemoteVideoUserRating :one
SELECT rating FROM remote_video_user_ratings
WHERE user_id = $1 AND remote_video_id = $2;

-- name: UpsertRemoteVideoUserRating :exec
-- A local user's vote on a remote video (0179); the origin holds the count.
INSERT INTO remote_video_user_ratings (remote_video_id, user_id, rating)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, remote_video_id)
DO UPDATE SET rating = EXCLUDED.rating, updated_at = now();

-- name: DeleteRemoteVideoUserRating :one
-- Returns the vote that was removed, so the caller can federate its Undo;
-- no row (pgx.ErrNoRows) means there was nothing to undo.
DELETE FROM remote_video_user_ratings
WHERE user_id = $1 AND remote_video_id = $2
RETURNING rating;
