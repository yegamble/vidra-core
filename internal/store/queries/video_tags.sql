-- name: DeleteVideoTags :exec
-- Clears a video's tags (the first half of a replace; tags are always written
-- as a full set).
DELETE FROM video_tags WHERE video_id = $1;

-- name: InsertVideoTags :exec
-- Inserts a set of (already normalized: lowercased, trimmed, deduped) tags for
-- a video. ON CONFLICT keeps the write idempotent should a duplicate slip in.
INSERT INTO video_tags (video_id, tag)
SELECT sqlc.arg('video_id'), unnest(sqlc.arg('tags')::text[])
ON CONFLICT DO NOTHING;

-- name: ListVideoTags :many
SELECT tag FROM video_tags WHERE video_id = $1 ORDER BY tag;
