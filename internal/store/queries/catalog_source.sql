-- name: ListCatalogSourcePage :many
-- A NULL cursor includes every eligible ID, including zero so capture can fail
-- preflight explicitly. A sentinel zero cursor silently omits that invalid row.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.category, v.language, v.license, v.is_sensitive,
       v.created_at, v.updated_at, v.publish_at,
       c.handle AS channel_handle, c.display_name AS channel_name,
       c.owner_id,
       au.display_name AS owner_display_name,
       au.unlisted AS owner_unlisted,
       vm.duration_seconds,
       COALESCE(vc.views, 0)::bigint AS views,
       (SELECT count(*) FROM video_ratings r WHERE r.video_id = v.id AND r.rating = 'like')::bigint AS likes,
       COALESCE(
           (SELECT array_agg(t.tag ORDER BY t.tag) FROM video_tags t WHERE t.video_id = v.id),
           '{}'
       )::text[] AS tags
FROM videos v
JOIN channels c ON c.id = v.channel_id
JOIN users au ON au.id = c.owner_id
LEFT JOIN video_metadata vm ON vm.video_id = v.id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE v.privacy = 'public' AND v.state = 'published'
  AND NOT au.unlisted
  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
  AND (sqlc.narg('after')::uuid IS NULL OR v.id > sqlc.narg('after')::uuid)
ORDER BY v.id
LIMIT sqlc.arg('page_size');
