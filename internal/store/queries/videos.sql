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
SELECT id, channel_id, title, description, privacy, state, created_at, updated_at
FROM videos
WHERE channel_id = $1
ORDER BY created_at DESC;

-- name: ListPublicVideosByChannel :many
SELECT id, channel_id, title, description, privacy, state, created_at, updated_at
FROM videos
WHERE channel_id = $1 AND privacy = 'public' AND state = 'published'
ORDER BY created_at DESC;

-- name: ListPublicVideos :many
SELECT id, channel_id, title, description, privacy, state, created_at, updated_at
FROM videos
WHERE privacy = 'public' AND state = 'published'
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: SearchPublicVideos :many
SELECT id, channel_id, title, description, privacy, state, created_at, updated_at
FROM videos
WHERE privacy = 'public' AND state = 'published' AND title ILIKE '%' || sqlc.arg('query') || '%'
ORDER BY similarity(title, sqlc.arg('query')) DESC, created_at DESC, id DESC
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
