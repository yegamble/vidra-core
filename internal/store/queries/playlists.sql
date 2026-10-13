-- name: CreatePlaylist :one
INSERT INTO playlists (owner_id, title, description, visibility, channel_id)
VALUES ($1, $2, $3, $4, sqlc.narg('channel_id'))
RETURNING id, owner_id, title, description, visibility, created_at, updated_at, thumbnail_ext, channel_id;

-- name: GetPlaylistByID :one
-- A single playlist with its public+published video count (matches what
-- ListPlaylistItems returns, so the count and the listed items agree).
SELECT p.id, p.owner_id, p.title, p.description, p.visibility, p.thumbnail_ext, p.channel_id, p.created_at, p.updated_at,
       (SELECT count(*) FROM playlist_items pi
        JOIN videos v ON v.id = pi.video_id
        WHERE pi.playlist_id = p.id AND v.privacy = 'public' AND v.state = 'published')::bigint AS video_count
FROM playlists p
WHERE p.id = $1;

-- name: ListPlaylistsByOwner :many
-- The user's playlists, newest first, each with its public+published video count.
SELECT p.id, p.owner_id, p.title, p.description, p.visibility, p.thumbnail_ext, p.channel_id, p.created_at, p.updated_at,
       (SELECT count(*) FROM playlist_items pi
        JOIN videos v ON v.id = pi.video_id
        WHERE pi.playlist_id = p.id AND v.privacy = 'public' AND v.state = 'published')::bigint AS video_count
FROM playlists p
WHERE p.owner_id = sqlc.arg('owner_id')
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountPlaylistsByOwner :one
-- How many rows ListPlaylistsByOwner would return, ignoring pagination.
SELECT count(*)::bigint FROM playlists p WHERE p.owner_id = sqlc.arg('owner_id');

-- name: ListPlaylistsByChannel :many
-- A channel's playlists, newest first. Visibility is the playlist privacy rule:
-- everyone sees 'public'; unlisted and private are the owner's alone (unlisted
-- means "reachable by link", never "listed on a tab"). viewer_id is NULL for an
-- anonymous caller, which matches no owner. Twin of CountPlaylistsByChannel.
SELECT p.id, p.owner_id, p.title, p.description, p.visibility, p.thumbnail_ext, p.channel_id, p.created_at, p.updated_at,
       (SELECT count(*) FROM playlist_items pi
        JOIN videos v ON v.id = pi.video_id
        WHERE pi.playlist_id = p.id AND v.privacy = 'public' AND v.state = 'published')::bigint AS video_count
FROM playlists p
WHERE p.channel_id = sqlc.arg('channel_id')::uuid
  AND (p.visibility = 'public' OR p.owner_id = sqlc.narg('viewer_id'))
  -- The per-viewer mute/block clause the channel-videos list carries (A16
  -- ruling): a viewer who muted or blocked the owner sees none of their playlists.
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = p.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = p.owner_id
  )
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountPlaylistsByChannel :one
-- How many rows ListPlaylistsByChannel would return, ignoring pagination. The
-- visibility predicate must stay identical to it.
SELECT count(*)::bigint FROM playlists p
WHERE p.channel_id = sqlc.arg('channel_id')::uuid
  AND (p.visibility = 'public' OR p.owner_id = sqlc.narg('viewer_id'))
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = p.owner_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = p.owner_id
  );

-- name: ListPlaylistsByOwnerForExport :many
-- Every playlist a user owns, UNPAGINATED. The account data export is the one
-- caller that must see all of them — an archive that silently stopped at the UI
-- page size would be a broken export, not a large one. Kept separate from
-- ListPlaylistsByOwner so the UI list can never accidentally lose its LIMIT again.
SELECT p.id, p.owner_id, p.title, p.description, p.visibility, p.thumbnail_ext, p.channel_id, p.created_at, p.updated_at,
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
    -- COALESCE cannot express "clear to NULL", so the channel link has its own
    -- switch: set_channel=false leaves it alone; true writes channel_id as given
    -- (NULL unlinks). Ownership of the target is checked by the service.
    channel_id  = CASE WHEN sqlc.arg('set_channel')::bool THEN sqlc.narg('channel_id') ELSE channel_id END,
    updated_at  = now()
WHERE id = sqlc.arg('id')
RETURNING id, owner_id, title, description, visibility, created_at, updated_at, thumbnail_ext, channel_id;

-- name: SetPlaylistThumbnail :exec
-- Record the extension of an owner-uploaded playlist cover (blob stored at
-- playlist-thumbnails/<id>.<ext>). Owner authorization is enforced in the service.
UPDATE playlists SET thumbnail_ext = $2, updated_at = now() WHERE id = $1;

-- name: ClearPlaylistThumbnail :exec
UPDATE playlists SET thumbnail_ext = NULL, updated_at = now() WHERE id = $1;

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

-- name: ListPlaylistItemVideoIDs :many
-- Every item's video id in current order, UNFILTERED by video privacy/state, so
-- the service can validate a reorder request covers exactly the playlist's items
-- (ListPlaylistItems only returns public+published cards and can't be used here).
SELECT video_id FROM playlist_items
WHERE playlist_id = $1
ORDER BY position ASC, added_at ASC, video_id ASC;

-- name: ReorderPlaylistItems :exec
-- Rewrite item positions to the given order: each video's position becomes its
-- 1-based index in video_ids. One atomic statement — items not listed keep their
-- position (callers pass the full set), and there is no UNIQUE(position) to
-- transiently collide with mid-update.
--
-- `locked` takes the row locks in video_id order, the same order
-- MovePlaylistItem uses. Without it the UPDATE locked rows in plan or client
-- order, so a reorder racing a move could each hold a row the other needed and
-- PostgreSQL aborted one as a deadlock. Joining through `locked` means no row
-- is written before it — and every lower video_id — has been locked.
WITH locked AS MATERIALIZED (
    SELECT video_id
    FROM playlist_items
    WHERE playlist_id = sqlc.arg('playlist_id')
    ORDER BY video_id
    FOR UPDATE
)
UPDATE playlist_items pi
SET position = src.rn
FROM (
    SELECT vid, rn::int AS rn
    FROM unnest(sqlc.arg('video_ids')::uuid[]) WITH ORDINALITY AS t(vid, rn)
) src
JOIN locked l ON l.video_id = src.vid
WHERE pi.playlist_id = sqlc.arg('playlist_id') AND pi.video_id = src.vid;

-- name: MovePlaylistItem :execrows
-- Move ONE item to directly after another (after_video_id), or to the front
-- when after_video_id is NULL, and renumber the whole playlist 1..n in the same
-- statement — so it needs no client-side copy of the full list (a paged client
-- only ever holds one page) and leaves no gap or duplicate behind. Anchoring on
-- a neighbour, not an index, is what keeps it right when private/unpublished
-- items the client cannot see sit between the visible ones.
--
-- Concurrency: `cur` locks every item row, in video_id order, BEFORE the order
-- is computed. A second mover blocks there until the first commits, and
-- READ COMMITTED then hands it each row's LATEST version, so it renumbers from
-- the committed result instead of from its own stale snapshot (which would
-- lose the first move). One lock order means two movers cannot deadlock. Every
-- row is written — never "only rows whose position changed": that filter would
-- be judged against the stale snapshot and skip rows the other mover changed.
--
-- 0 rows means nothing was moved: video_id or after_video_id is not an item.
WITH cur AS MATERIALIZED (
    SELECT video_id, position, added_at
    FROM playlist_items
    WHERE playlist_id = sqlc.arg('playlist_id')
    ORDER BY video_id
    FOR UPDATE
),
ranked AS (
    SELECT video_id, row_number() OVER (ORDER BY position, added_at, video_id) AS rn
    FROM cur
),
anchor AS (
    -- 0 = the front; NULL = after_video_id is not in the playlist.
    SELECT CASE WHEN sqlc.narg('after_video_id')::uuid IS NULL THEN 0::bigint
                ELSE (SELECT rn FROM ranked WHERE video_id = sqlc.narg('after_video_id')::uuid)
           END AS rn
),
renum AS (
    -- Every other item keeps slot 2*rn; the moved one takes 2*anchor+1, i.e.
    -- directly after its anchor. row_number() turns the slots back into 1..n.
    SELECT r.video_id,
           row_number() OVER (ORDER BY CASE WHEN r.video_id = sqlc.arg('video_id')::uuid
                                            THEN a.rn * 2 + 1 ELSE r.rn * 2 END)::int AS pos
    FROM ranked r CROSS JOIN anchor a
    WHERE a.rn IS NOT NULL
      AND EXISTS (SELECT 1 FROM ranked WHERE video_id = sqlc.arg('video_id')::uuid)
)
UPDATE playlist_items pi
SET position = renum.pos
FROM renum
WHERE pi.playlist_id = sqlc.arg('playlist_id') AND pi.video_id = renum.video_id;

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
       c.handle AS channel_handle, c.display_name AS channel_display_name,
       v.short_code
FROM playlist_items pi
JOIN videos v ON v.id = pi.video_id
JOIN channels c ON c.id = v.channel_id
LEFT JOIN video_view_counts vc ON vc.video_id = v.id
WHERE pi.playlist_id = sqlc.arg('playlist_id')
  AND v.privacy = 'public' AND v.state = 'published'
-- video_id ends the ORDER BY so the order is TOTAL: positions can tie (two
-- racing appends both take MAX+1) and so can added_at, and a tie is free to
-- sort differently per LIMIT/OFFSET, showing one item on two pages and
-- skipping another.
ORDER BY pi.position ASC, pi.added_at ASC, pi.video_id ASC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountPlaylistItems :one
-- How many rows ListPlaylistItems would return, ignoring pagination. The
-- public+published predicate must stay identical: an item whose video went
-- private is not listed and must not be counted.
SELECT count(*)::bigint
FROM playlist_items pi
JOIN videos v ON v.id = pi.video_id
JOIN channels c ON c.id = v.channel_id
WHERE pi.playlist_id = sqlc.arg('playlist_id')
  AND v.privacy = 'public' AND v.state = 'published';
