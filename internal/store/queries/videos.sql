-- name: CreateVideo :one
INSERT INTO videos (channel_id, title, description, privacy)
VALUES ($1, $2, $3, $4)
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at;

-- name: GetVideoByID :one
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state, v.created_at, v.updated_at,
       c.owner_id
FROM videos v
JOIN channels c ON c.id = v.channel_id
WHERE v.id = $1;

-- name: ListVideosByChannel :many
-- A channel's videos (owner view, all states) with discovery-card data.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.channel_id = $1
ORDER BY v.created_at DESC;

-- name: ListPublicVideosByChannel :many
-- A channel's public, published videos with discovery-card data.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.channel_id = $1 AND v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
ORDER BY v.created_at DESC;

-- name: ListPublicVideosSorted :many
-- The public feed, joined with view counts and thumbnail availability so cards
-- have what they need, ordered by the requested mode:
--   recent   -> newest first (the NULL CASE terms fall through to created_at)
--   popular  -> most all-time views first
--   trending -> views decayed by age (Hacker-News-style gravity)
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
ORDER BY
    CASE WHEN sqlc.arg('sort')::text = 'popular' THEN COALESCE(vc.views, 0) END DESC,
    CASE WHEN sqlc.arg('sort')::text = 'trending'
         THEN COALESCE(vc.views, 0)::float8
              / power(EXTRACT(EPOCH FROM (now() - v.created_at)) / 3600.0 + 2.0, 1.5)
    END DESC,
    v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: ListSubscriptionVideos :many
-- The "subscriptions" feed: public, published videos from the channels the given
-- user follows, newest first, with the same discovery-card data as the main feed.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.arg('follower_id') AND m.muted_id = c.owner_id
  )
  AND v.channel_id IN (
      SELECT channel_id FROM channel_follows WHERE follower_id = sqlc.arg('follower_id')
  )
ORDER BY v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: SearchPublicVideos :many
-- Public, published title search with discovery-card data.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = c.owner_id
  )
  AND v.title ILIKE '%' || sqlc.arg('query') || '%'
ORDER BY similarity(v.title, sqlc.arg('query')) DESC, v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: UpdateVideo :one
UPDATE videos
SET title       = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    privacy     = COALESCE(sqlc.narg('privacy'), privacy),
    updated_at  = now()
WHERE id = sqlc.arg('id')
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at;

-- name: SetVideoState :one
UPDATE videos
SET state      = sqlc.arg('state'),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, channel_id, title, description, privacy, state, created_at, updated_at;

-- name: DeleteVideo :exec
DELETE FROM videos WHERE id = $1;

-- name: ListAdminVideos :many
-- The admin/moderator videos overview: ALL videos (any privacy/state) newest
-- first, with the owning channel, view count, and whether the video is currently
-- blocked. An optional case-insensitive title filter (NULL = no filter).
SELECT v.id, v.title, v.privacy, v.state,
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       COALESCE(vc.views, 0)::bigint AS views,
       v.created_at,
       EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id) AS blocked
FROM videos v
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE (sqlc.narg('query')::text IS NULL OR v.title ILIKE '%' || sqlc.narg('query') || '%')
ORDER BY v.created_at DESC, v.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
