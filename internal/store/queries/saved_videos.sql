-- name: SaveVideo :exec
-- Add a video to the user's library (idempotent).
INSERT INTO saved_videos (user_id, video_id)
VALUES ($1, $2)
ON CONFLICT (user_id, video_id) DO NOTHING;

-- name: UnsaveVideo :exec
DELETE FROM saved_videos
WHERE user_id = $1 AND video_id = $2;

-- name: ListSavedVideos :many
-- The user's saved videos, newest-saved first, with the same discovery-card data
-- as the main feed. Only public, published videos are returned.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM saved_videos s
JOIN videos v ON v.id = s.video_id
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE s.user_id = sqlc.arg('user_id')
  AND v.privacy = 'public' AND v.state = 'published'
ORDER BY s.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
