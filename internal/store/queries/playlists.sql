-- name: CreatePlaylist :one
INSERT INTO playlists (owner_id, title, description, visibility)
VALUES ($1, $2, $3, $4)
RETURNING id, owner_id, title, description, visibility, created_at, updated_at;

-- name: GetPlaylistByID :one
-- A single playlist with its public+published video count (matches what
-- ListPlaylistItems returns, so the count and the listed items agree).
SELECT p.id, p.owner_id, p.title, p.description, p.visibility, p.created_at, p.updated_at,
       (SELECT count(*) FROM playlist_items pi
        JOIN videos v ON v.id = pi.video_id
        WHERE pi.playlist_id = p.id AND v.privacy = 'public' AND v.state = 'published')::bigint AS video_count
FROM playlists p
WHERE p.id = $1;

-- name: ListPlaylistsByOwner :many
-- The user's playlists, newest first, each with its public+published video count.
SELECT p.id, p.owner_id, p.title, p.description, p.visibility, p.created_at, p.updated_at,
       (SELECT count(*) FROM playlist_items pi
        JOIN videos v ON v.id = pi.video_id
        WHERE pi.playlist_id = p.id AND v.privacy = 'public' AND v.state = 'published')::bigint AS video_count
FROM playlists p
WHERE p.owner_id = $1
ORDER BY p.created_at DESC, p.id DESC;

-- name: UpdatePlaylist :one
-- Partial update: NULL args leave the column unchanged (COALESCE).
UPDATE playlists
SET title       = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    visibility  = COALESCE(sqlc.narg('visibility'), visibility),
    updated_at  = now()
WHERE id = sqlc.arg('id')
RETURNING id, owner_id, title, description, visibility, created_at, updated_at;

-- name: DeletePlaylist :exec
DELETE FROM playlists WHERE id = $1;

-- name: AddPlaylistItem :exec
-- Append a video to the end of a playlist (idempotent: re-adding is a no-op).
INSERT INTO playlist_items (playlist_id, video_id, position)
VALUES (
    sqlc.arg('playlist_id'),
    sqlc.arg('video_id'),
    (SELECT COALESCE(MAX(position), 0) + 1 FROM playlist_items WHERE playlist_id = sqlc.arg('playlist_id'))
)
ON CONFLICT (playlist_id, video_id) DO NOTHING;

-- name: RemovePlaylistItem :exec
DELETE FROM playlist_items
WHERE playlist_id = $1 AND video_id = $2;

-- name: ListPlaylistItems :many
-- A playlist's videos in order, as discovery cards (the same card data as the
-- main feed). Only public, published videos are returned.
SELECT v.id, v.channel_id, v.title, v.description, v.privacy, v.state,
       v.created_at, v.updated_at,
       COALESCE(vc.views, 0)::bigint AS views,
       EXISTS (
           SELECT 1 FROM video_files f
           WHERE f.video_id = v.id AND f.kind = 'thumbnail'
       ) AS has_thumbnail,
       c.handle AS channel_handle, c.display_name AS channel_display_name
FROM playlist_items pi
JOIN videos v ON v.id = pi.video_id
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE pi.playlist_id = $1
  AND v.privacy = 'public' AND v.state = 'published'
ORDER BY pi.position ASC, pi.added_at ASC;
