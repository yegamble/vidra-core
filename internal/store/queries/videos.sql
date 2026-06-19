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
