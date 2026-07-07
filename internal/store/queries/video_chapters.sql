-- name: ListVideoChapters :many
-- A video's chapters in playback order (ascending start). Empty when none.
SELECT start_seconds, title FROM video_chapters WHERE video_id = $1 ORDER BY start_seconds;

-- name: VideoHasChapters :one
-- Whether a video has at least one chapter (drives the detail has_chapters flag,
-- so the player fetches /chapters only when they exist).
SELECT EXISTS (SELECT 1 FROM video_chapters WHERE video_id = $1);

-- name: DeleteVideoChapters :exec
-- Clears a video's chapters (the first half of a whole-set replace).
DELETE FROM video_chapters WHERE video_id = $1;

-- name: InsertVideoChapters :exec
-- Bulk-inserts a video's chapter set from two parallel arrays (start_seconds[i]
-- pairs with title[i]). The service validates and orders the set first; the
-- arrays are guaranteed equal-length and non-overlapping, so no ON CONFLICT is
-- needed (a fresh DeleteVideoChapters runs immediately before).
INSERT INTO video_chapters (video_id, start_seconds, title)
SELECT sqlc.arg('video_id'),
       unnest(sqlc.arg('start_seconds')::int[]),
       unnest(sqlc.arg('title')::text[]);
