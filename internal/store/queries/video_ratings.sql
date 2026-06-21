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
SELECT
    COUNT(*) FILTER (WHERE rating = 'like')    AS likes,
    COUNT(*) FILTER (WHERE rating = 'dislike') AS dislikes
FROM video_ratings
WHERE video_id = $1;
