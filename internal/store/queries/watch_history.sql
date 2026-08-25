-- name: UpsertWatchProgress :one
-- Record (or update) the caller's resume position for a video. Bumps updated_at
-- so the video moves to the top of the history list.
INSERT INTO watch_history (user_id, video_id, position_seconds)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, video_id) DO UPDATE
SET position_seconds = EXCLUDED.position_seconds,
    updated_at       = now()
RETURNING user_id, video_id, position_seconds, created_at, updated_at;

-- name: GetWatchProgress :one
-- The caller's saved resume position for a single video (miss => no progress).
SELECT user_id, video_id, position_seconds, created_at, updated_at
FROM watch_history
WHERE user_id = $1 AND video_id = $2;

-- name: ListWatchHistory :many
-- The user's watch history, most-recently-watched first, with the same
-- discovery-card data as the main feed plus the saved resume position and the
-- time last watched. Only public, published videos are returned.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive, v.sensitive_reason,
       wh.position_seconds, wh.updated_at AS watched_at
FROM watch_history wh
JOIN videos v ON v.id = wh.video_id
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE wh.user_id = sqlc.arg('user_id')
  AND v.privacy = 'public' AND v.state = 'published'
ORDER BY wh.updated_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountWatchHistory :one
-- How many rows ListWatchHistory would return, ignoring pagination. The
-- public+published predicate must stay identical: history entries for videos
-- since made private are not listed and must not be counted.
SELECT count(*)::bigint
FROM watch_history wh
JOIN videos v ON v.id = wh.video_id
JOIN channels c ON c.id = v.channel_id
WHERE wh.user_id = sqlc.arg('user_id')
  AND v.privacy = 'public' AND v.state = 'published';

-- name: ListWatchHistoryInProgress :many
-- The user's IN-PROGRESS watch history (the "Continue watching" shelf): same
-- shape and ordering as ListWatchHistory, but excludes entries that are barely
-- started (< RESUME_MIN_SECONDS = 5s) or effectively finished (>= 95% of the
-- known duration). Videos with no/zero known duration keep the lower-bound-only
-- behaviour (position >= 5) so they still surface as resumable.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       au.display_name AS author_display_name,
       vm.duration_seconds, v.is_sensitive, v.sensitive_reason,
       wh.position_seconds, wh.updated_at AS watched_at
FROM watch_history wh
JOIN videos v ON v.id = wh.video_id
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE wh.user_id = sqlc.arg('user_id')
  AND v.privacy = 'public' AND v.state = 'published'
  AND wh.position_seconds >= 5
  AND (vm.duration_seconds IS NULL
       OR vm.duration_seconds <= 0
       OR wh.position_seconds::float / vm.duration_seconds::float < 0.95)
ORDER BY wh.updated_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountWatchHistoryInProgress :one
-- How many rows ListWatchHistoryInProgress would return, ignoring pagination.
-- The started/not-finished window must stay identical to it.
SELECT count(*)::bigint
FROM watch_history wh
JOIN videos v ON v.id = wh.video_id
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
WHERE wh.user_id = sqlc.arg('user_id')
  AND v.privacy = 'public' AND v.state = 'published'
  AND wh.position_seconds >= 5
  AND (vm.duration_seconds IS NULL
       OR vm.duration_seconds <= 0
       OR wh.position_seconds::float / vm.duration_seconds::float < 0.95);

-- name: DeleteWatchHistoryEntry :exec
-- Remove a single video from the user's history (idempotent). No public-video
-- check so a user can always clean up an entry.
DELETE FROM watch_history
WHERE user_id = $1 AND video_id = $2;

-- name: ClearWatchHistory :exec
-- Remove the user's entire watch history (idempotent).
DELETE FROM watch_history
WHERE user_id = $1;
