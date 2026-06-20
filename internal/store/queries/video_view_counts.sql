-- name: IncrementVideoViews :one
INSERT INTO video_view_counts (video_id, views, updated_at)
VALUES ($1, 1, now())
ON CONFLICT (video_id) DO UPDATE
SET views = video_view_counts.views + 1,
    updated_at = now()
RETURNING views;

-- name: GetVideoViews :one
SELECT views FROM video_view_counts WHERE video_id = $1;
