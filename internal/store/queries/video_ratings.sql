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
SELECT
    COUNT(*) FILTER (WHERE r.rating = 'like')    AS likes,
    COUNT(*) FILTER (WHERE r.rating = 'dislike') AS dislikes
FROM (
    SELECT vr.rating FROM video_ratings vr WHERE vr.video_id = sqlc.arg('video_id')::uuid
    UNION ALL
    SELECT rr.rating FROM remote_video_ratings rr WHERE rr.video_id = sqlc.arg('video_id')::uuid
) r;

-- name: UpsertRemoteVideoRating :exec
-- A remote actor's Like/Dislike of a local video: one row per (actor, video),
-- so a repeat is a no-op and a switch changes the row in place.
INSERT INTO remote_video_ratings (video_id, remote_actor_url, rating, activity_url)
VALUES ($1, $2, $3, $4)
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
